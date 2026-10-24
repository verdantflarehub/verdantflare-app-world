-- Append-only observation attempts; the original terminal record is never updated.
CREATE TABLE studio.world_analysis_step_retries (LIKE studio.world_analysis_steps INCLUDING DEFAULTS INCLUDING CONSTRAINTS,
 attempt integer NOT NULL CHECK(attempt BETWEEN 1 AND 2),
 retry_id uuid NOT NULL,
 PRIMARY KEY(organization_id,owner_id,task_id,step_key,attempt),
 UNIQUE(organization_id,owner_id,retry_id),
 FOREIGN KEY(organization_id,owner_id,task_id,step_key) REFERENCES studio.world_analysis_steps(organization_id,owner_id,task_id,step_key),
 CHECK(((request->'retry'->>'number')::integer=attempt AND request->'retry'->>'id'=retry_id::text) IS TRUE)
);
CREATE VIEW studio.world_analysis_current_steps AS
 SELECT DISTINCT ON(organization_id,owner_id,task_id,step_key) organization_id,owner_id,task_id,step_key,request_sha256,request,revision,state,call_id,native_result,report,error_code,updated_at
 FROM (SELECT organization_id,owner_id,task_id,step_key,request_sha256,request,revision,state,call_id,native_result,report,error_code,updated_at,0 AS attempt FROM studio.world_analysis_steps
 UNION ALL SELECT organization_id,owner_id,task_id,step_key,request_sha256,request,revision,state,call_id,native_result,report,error_code,updated_at,attempt FROM studio.world_analysis_step_retries) records
 ORDER BY organization_id,owner_id,task_id,step_key,attempt DESC;
CREATE FUNCTION studio.world_analysis_retry_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior record;
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM 1 FROM studio.world_analysis_tasks WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND pipeline_version=2 FOR NO KEY UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'retry requires versioned owner task'; END IF;
  SELECT * INTO prior FROM studio.world_analysis_current_steps WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND step_key=NEW.step_key;
  IF prior.state IS DISTINCT FROM 'failed' OR prior.error_code IS DISTINCT FROM 'observation_invalid' OR prior.native_result IS NULL OR prior.call_id IS NULL
   OR NEW.state<>'planned' OR NEW.revision<>1 OR NEW.attempt<>COALESCE((prior.request->'retry'->>'number')::integer,0)+1
   OR NEW.request->'retry'->>'previous_request_sha256' IS DISTINCT FROM prior.request_sha256
   OR NEW.request->'retry'->>'previous_call_id' IS DISTINCT FROM prior.call_id::text
   OR (NEW.request-'retry'-'idempotency_key'-'payload') IS DISTINCT FROM (prior.request-'retry'-'idempotency_key'-'payload') THEN
   RAISE EXCEPTION 'retry requires the original failed observation';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP<>'UPDATE' OR NEW.attempt<>OLD.attempt OR NEW.retry_id<>OLD.retry_id THEN RAISE EXCEPTION 'immutable retry identity'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_analysis_retry_admission BEFORE INSERT OR UPDATE OR DELETE ON studio.world_analysis_step_retries FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_retry_guard();
CREATE TRIGGER world_analysis_step_identity BEFORE UPDATE OR DELETE ON studio.world_analysis_step_retries FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_step_guard();
CREATE TRIGGER world_analysis_retries_no_truncate BEFORE TRUNCATE ON studio.world_analysis_step_retries FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_step_retries FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_step_retries ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE OR REPLACE FUNCTION studio.world_analysis_association_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected integer; ready integer;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT jsonb_array_length(plan->'steps') INTO expected FROM studio.world_analysis_tasks
   WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id;
  SELECT count(*) INTO ready FROM studio.world_analysis_current_steps
   WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state='observation_ready';
  IF COALESCE(expected,0)<1 OR ready<>expected OR NEW.state<>'planned' OR NEW.revision<>1 THEN
   RAISE EXCEPTION 'association requires all observations and a new planned request';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'immutable World association record'; END IF;
 IF (NEW.organization_id,NEW.owner_id,NEW.task_id,NEW.request_sha256,NEW.request) IS DISTINCT FROM (OLD.organization_id,OLD.owner_id,OLD.task_id,OLD.request_sha256,OLD.request) THEN
  RAISE EXCEPTION 'immutable World association request';
 END IF;
 IF OLD.state IN ('association_ready','failed') OR NEW.revision<>OLD.revision+1 OR (OLD.call_id IS NOT NULL AND NEW.call_id IS DISTINCT FROM OLD.call_id) THEN
  RAISE EXCEPTION 'invalid World association revision';
 END IF;
 IF (OLD.state='planned' AND NEW.state<>'dispatching') OR (OLD.state<>'planned' AND NEW.state IN ('planned','dispatching')) THEN
  RAISE EXCEPTION 'invalid World association transition';
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
  UNION ALL SELECT request FROM studio.world_analysis_associations WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
  UNION ALL SELECT request FROM studio.world_analysis_prompt_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
  UNION ALL SELECT request FROM studio.world_analysis_result_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
 ) r;
 IF amount<=0 OR reserved+amount>cap::numeric THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='WORLD_ANALYSIS_BUDGET_EXHAUSTED'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_analysis_budget BEFORE UPDATE ON studio.world_analysis_step_retries FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_budget_guard();
