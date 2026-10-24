-- Append-only association attempts; the original terminal record is never updated.
CREATE TABLE studio.world_analysis_association_retries (LIKE studio.world_analysis_associations INCLUDING DEFAULTS INCLUDING CONSTRAINTS,
 attempt integer NOT NULL CHECK(attempt BETWEEN 1 AND 2),
 retry_id uuid NOT NULL,
 PRIMARY KEY(organization_id,owner_id,task_id,attempt),
 UNIQUE(organization_id,owner_id,retry_id),
 FOREIGN KEY(organization_id,owner_id,task_id) REFERENCES studio.world_analysis_associations(organization_id,owner_id,task_id),
 CHECK(((request->'retry'->>'number')::integer=attempt AND request->'retry'->>'id'=retry_id::text) IS TRUE)
);
CREATE VIEW studio.world_analysis_current_associations AS
 SELECT DISTINCT ON(organization_id,owner_id,task_id) organization_id,owner_id,task_id,request_sha256,request,revision,state,call_id,native_result,error_code,updated_at
 FROM (SELECT organization_id,owner_id,task_id,request_sha256,request,revision,state,call_id,native_result,error_code,updated_at,0 AS attempt FROM studio.world_analysis_associations
 UNION ALL SELECT organization_id,owner_id,task_id,request_sha256,request,revision,state,call_id,native_result,error_code,updated_at,attempt FROM studio.world_analysis_association_retries) records
 ORDER BY organization_id,owner_id,task_id,attempt DESC;
CREATE FUNCTION studio.world_analysis_association_retry_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior record;
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM 1 FROM studio.world_analysis_tasks WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND pipeline_version=2 FOR NO KEY UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'retry requires versioned owner task'; END IF;
  IF EXISTS(SELECT 1 FROM studio.world_analysis_current_steps WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'observation_ready')
   OR EXISTS(SELECT 1 FROM studio.world_analysis_result_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id)
   OR EXISTS(SELECT 1 FROM studio.world_analysis_prompt_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id)
   THEN RAISE EXCEPTION 'association retry must precede downstream work'; END IF;
  SELECT * INTO prior FROM studio.world_analysis_current_associations WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id;
  IF prior.state IS DISTINCT FROM 'failed' OR prior.error_code IS DISTINCT FROM 'association_invalid' OR prior.native_result IS NULL OR prior.call_id IS NULL
   OR NEW.state<>'planned' OR NEW.revision<>1 OR NEW.attempt<>COALESCE((prior.request->'retry'->>'number')::integer,0)+1
   OR NEW.request->'retry'->>'previous_request_sha256' IS DISTINCT FROM prior.request_sha256
   OR NEW.request->'retry'->>'previous_call_id' IS DISTINCT FROM prior.call_id::text
   OR ((NEW.request->'payload')-'prompt') IS DISTINCT FROM ((prior.request->'payload')-'prompt')
   OR (NEW.request-'retry'-'idempotency_key'-'payload') IS DISTINCT FROM (prior.request-'retry'-'idempotency_key'-'payload') THEN
   RAISE EXCEPTION 'retry requires the original failed association';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP<>'UPDATE' OR NEW.attempt<>OLD.attempt OR NEW.retry_id<>OLD.retry_id THEN RAISE EXCEPTION 'immutable retry identity'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_analysis_retry_admission BEFORE INSERT OR UPDATE OR DELETE ON studio.world_analysis_association_retries FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_association_retry_guard();
CREATE TRIGGER world_analysis_step_identity BEFORE UPDATE OR DELETE ON studio.world_analysis_association_retries FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_association_guard();
CREATE TRIGGER world_analysis_retries_no_truncate BEFORE TRUNCATE ON studio.world_analysis_association_retries FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_association_retries FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_association_retries ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE FUNCTION studio.world_analysis_retry_id_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended(NEW.organization_id::text||NEW.owner_id::text||NEW.retry_id::text,9018));
 IF EXISTS(SELECT 1 FROM studio.world_analysis_step_retries WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND retry_id=NEW.retry_id)
 OR EXISTS(SELECT 1 FROM studio.world_analysis_association_retries WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND retry_id=NEW.retry_id) THEN RAISE EXCEPTION 'analysis retry id already used'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_analysis_retry_id BEFORE INSERT ON studio.world_analysis_step_retries FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_retry_id_guard();
CREATE TRIGGER world_analysis_retry_id BEFORE INSERT ON studio.world_analysis_association_retries FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_retry_id_guard();
CREATE OR REPLACE FUNCTION studio.world_analysis_result_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE enabled boolean; ready boolean; targets jsonb; pipeline integer;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT t.include_typed_results,a.state='association_ready',t.prompt_targets,t.pipeline_version INTO enabled,ready,targets,pipeline
   FROM studio.world_analysis_tasks t JOIN studio.world_analysis_current_associations a USING(organization_id,owner_id,task_id)
   WHERE t.organization_id=NEW.organization_id AND t.owner_id=NEW.owner_id AND t.task_id=NEW.task_id;
  IF COALESCE(ready,false)=false OR COALESCE(enabled,false)=false
   OR NEW.request->'batch'->>'batch_key' IS DISTINCT FROM NEW.batch_key
   OR NEW.request->>'task_id' IS DISTINCT FROM NEW.task_id::text
   OR NEW.state<>'planned' OR NEW.revision<>1 THEN
   RAISE EXCEPTION 'result batch requires ready association and frozen task option';
  END IF;
  IF COALESCE((NEW.request->'batch'->>'schema_version')::integer,0)<>(CASE WHEN pipeline=2 THEN 2 ELSE 1 END) THEN RAISE EXCEPTION 'result pipeline mismatch'; END IF;
  IF pipeline=0 AND jsonb_array_length(targets)>0 AND (
   NOT EXISTS(SELECT 1 FROM studio.world_analysis_prompt_batches p WHERE p.organization_id=NEW.organization_id AND p.owner_id=NEW.owner_id AND p.task_id=NEW.task_id)
   OR EXISTS(SELECT 1 FROM studio.world_analysis_prompt_batches p WHERE p.organization_id=NEW.organization_id AND p.owner_id=NEW.owner_id AND p.task_id=NEW.task_id AND p.state<>'prompt_ready')) THEN
   RAISE EXCEPTION 'result batch requires configured prompts';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'immutable World result record'; END IF;
 IF (NEW.organization_id,NEW.owner_id,NEW.task_id,NEW.batch_key,NEW.batch_index,NEW.request_sha256,NEW.request) IS DISTINCT FROM (OLD.organization_id,OLD.owner_id,OLD.task_id,OLD.batch_key,OLD.batch_index,OLD.request_sha256,OLD.request) THEN
  RAISE EXCEPTION 'immutable World result request';
 END IF;
 IF OLD.state IN ('result_ready','failed') OR NEW.revision<>OLD.revision+1 OR (OLD.call_id IS NOT NULL AND NEW.call_id IS DISTINCT FROM OLD.call_id) THEN
  RAISE EXCEPTION 'invalid World result revision';
 END IF;
 IF (OLD.state='planned' AND NEW.state<>'dispatching') OR (OLD.state<>'planned' AND NEW.state IN ('planned','dispatching')) THEN
  RAISE EXCEPTION 'invalid World result transition';
 END IF;
 RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION studio.world_analysis_prompt_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE targets jsonb; ready boolean; pipeline integer;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT t.prompt_targets,a.state='association_ready',t.pipeline_version INTO targets,ready,pipeline
   FROM studio.world_analysis_tasks t JOIN studio.world_analysis_current_associations a USING(organization_id,owner_id,task_id)
   WHERE t.organization_id=NEW.organization_id AND t.owner_id=NEW.owner_id AND t.task_id=NEW.task_id;
  IF COALESCE(ready,false)=false OR COALESCE(jsonb_array_length(targets),0)<1
   OR NEW.request->'batch'->'targets' IS DISTINCT FROM targets
   OR NEW.request->'batch'->>'batch_key' IS DISTINCT FROM NEW.batch_key
   OR NEW.request->>'task_id' IS DISTINCT FROM NEW.task_id::text
   OR NEW.state<>'planned' OR NEW.revision<>1 THEN
   RAISE EXCEPTION 'prompt batch requires ready association and frozen task targets';
  END IF;
  IF COALESCE((NEW.request->'batch'->>'schema_version')::integer,0)<>(CASE WHEN pipeline=2 THEN 2 ELSE 1 END) THEN RAISE EXCEPTION 'prompt pipeline mismatch'; END IF;
  IF pipeline=2 AND (
   NOT EXISTS(SELECT 1 FROM studio.world_analysis_result_batches r WHERE r.organization_id=NEW.organization_id AND r.owner_id=NEW.owner_id AND r.task_id=NEW.task_id)
   OR EXISTS(SELECT 1 FROM studio.world_analysis_result_batches r WHERE r.organization_id=NEW.organization_id AND r.owner_id=NEW.owner_id AND r.task_id=NEW.task_id AND r.state<>'result_ready')) THEN RAISE EXCEPTION 'prompts require complete typed results'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'immutable World prompt record'; END IF;
 IF (NEW.organization_id,NEW.owner_id,NEW.task_id,NEW.batch_key,NEW.batch_index,NEW.request_sha256,NEW.request) IS DISTINCT FROM (OLD.organization_id,OLD.owner_id,OLD.task_id,OLD.batch_key,OLD.batch_index,OLD.request_sha256,OLD.request) THEN
  RAISE EXCEPTION 'immutable World prompt request';
 END IF;
 IF OLD.state IN ('prompt_ready','failed') OR NEW.revision<>OLD.revision+1 OR (OLD.call_id IS NOT NULL AND NEW.call_id IS DISTINCT FROM OLD.call_id) THEN
  RAISE EXCEPTION 'invalid World prompt revision';
 END IF;
 IF (OLD.state='planned' AND NEW.state<>'dispatching') OR (OLD.state<>'planned' AND NEW.state IN ('planned','dispatching')) THEN
  RAISE EXCEPTION 'invalid World prompt transition';
 END IF;
 RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION studio.world_analysis_budget_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cap text; reserved numeric; amount numeric;
BEGIN
 IF OLD.state<>'planned' OR NEW.state<>'dispatching' THEN RETURN NEW; END IF;
 SELECT total_budget INTO cap FROM studio.world_analysis_tasks WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id FOR NO KEY UPDATE;
 IF cap='' THEN RETURN NEW; END IF;
 IF cap IS NULL OR NEW.request->>'budget_limit' IS NULL THEN RAISE EXCEPTION 'invalid analysis budget'; END IF;
 amount := (NEW.request->>'budget_limit')::numeric;
 SELECT COALESCE(sum((r.request->>'budget_limit')::numeric),0) INTO reserved FROM (
  SELECT request FROM studio.world_analysis_steps WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
  UNION ALL SELECT request FROM studio.world_analysis_step_retries WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
  UNION ALL SELECT request FROM studio.world_analysis_association_retries WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
  UNION ALL SELECT request FROM studio.world_analysis_associations WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
  UNION ALL SELECT request FROM studio.world_analysis_prompt_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
  UNION ALL SELECT request FROM studio.world_analysis_result_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
 ) r;
 IF amount<=0 OR reserved+amount>cap::numeric THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='WORLD_ANALYSIS_BUDGET_EXHAUSTED'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_analysis_budget BEFORE UPDATE ON studio.world_analysis_association_retries FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_budget_guard();
