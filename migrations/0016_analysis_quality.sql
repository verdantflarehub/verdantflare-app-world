-- Admission pins the internal pipeline. Legacy tasks/requests retain version
-- zero, frozen hashes, operation addresses and original prompt-first order.
ALTER TABLE studio.world_analysis_tasks ADD COLUMN pipeline_version integer NOT NULL DEFAULT 0 CHECK(pipeline_version IN (0,2));
ALTER TABLE studio.world_analysis_tasks ADD CONSTRAINT world_analysis_typed_pipeline CHECK(pipeline_version=0 OR include_typed_results);
ALTER TABLE studio.world_analysis_requests ADD COLUMN pipeline_version integer NOT NULL DEFAULT 0 CHECK(pipeline_version IN (0,2));
CREATE TABLE studio.world_analysis_prompt_plans (
 organization_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 task_id uuid NOT NULL,
 plan_sha256 text NOT NULL CHECK(plan_sha256 ~ '^[0-9a-f]{64}$'),
 batch_count integer NOT NULL CHECK(batch_count>=0 AND batch_count<=16384),
 PRIMARY KEY(organization_id,owner_id,task_id),
 FOREIGN KEY(organization_id,owner_id,task_id) REFERENCES studio.world_analysis_tasks(organization_id,owner_id,task_id)
);
CREATE TRIGGER world_analysis_prompt_plans_immutable BEFORE UPDATE OR DELETE ON studio.world_analysis_prompt_plans FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_analysis_prompt_plans_no_truncate BEFORE TRUNCATE ON studio.world_analysis_prompt_plans FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_prompt_plans FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_prompt_plans ENABLE ALWAYS TRIGGER world_handoff_gate;

CREATE OR REPLACE FUNCTION studio.world_analysis_request_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.lease_id IS NOT NULL OR NEW.error_code<>'' THEN RAISE EXCEPTION 'invalid admission'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'immutable analysis request'; END IF;
 IF (NEW.organization_id,NEW.owner_id,NEW.task_id,NEW.draft_id,NEW.revision_id,NEW.file_id,NEW.source_sha256,NEW.request_sha256,NEW.request,NEW.created_at,NEW.pipeline_version) IS DISTINCT FROM
    (OLD.organization_id,OLD.owner_id,OLD.task_id,OLD.draft_id,OLD.revision_id,OLD.file_id,OLD.source_sha256,OLD.request_sha256,OLD.request,OLD.created_at,OLD.pipeline_version) THEN RAISE EXCEPTION 'immutable analysis request'; END IF;
 IF NEW.state IS DISTINCT FROM OLD.state AND NOT ((OLD.state='pending' AND NEW.state='preparing') OR (OLD.state='preparing' AND NEW.state IN ('queued','failed'))) THEN RAISE EXCEPTION 'invalid admission transition'; END IF;
 IF NEW.state='queued' AND NOT EXISTS(SELECT 1 FROM studio.world_analysis_tasks t WHERE t.organization_id=NEW.organization_id AND t.owner_id=NEW.owner_id AND t.task_id=NEW.task_id AND t.pipeline_version=NEW.pipeline_version AND t.save_learning AND t.include_typed_results AND t.total_budget=NEW.request->>'budget_limit') THEN RAISE EXCEPTION 'admission requires complete task'; END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION studio.world_analysis_result_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE enabled boolean; ready boolean; targets jsonb; pipeline integer;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT t.include_typed_results,a.state='association_ready',t.prompt_targets,t.pipeline_version INTO enabled,ready,targets,pipeline
   FROM studio.world_analysis_tasks t JOIN studio.world_analysis_associations a USING(organization_id,owner_id,task_id)
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
   FROM studio.world_analysis_tasks t JOIN studio.world_analysis_associations a USING(organization_id,owner_id,task_id)
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

CREATE FUNCTION studio.world_analysis_prompt_plan_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE pipeline integer; count_batches integer;
BEGIN
 SELECT pipeline_version INTO pipeline FROM studio.world_analysis_tasks WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id;
 IF pipeline IS DISTINCT FROM 2 OR NOT EXISTS(SELECT 1 FROM studio.world_analysis_result_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id) OR EXISTS(SELECT 1 FROM studio.world_analysis_result_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'result_ready') THEN RAISE EXCEPTION 'plan requires typed results'; END IF;
 SELECT count(*) INTO count_batches FROM studio.world_analysis_prompt_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id;
 IF count_batches<>NEW.batch_count OR EXISTS(SELECT 1 FROM studio.world_analysis_prompt_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND request->'batch'->>'plan_sha256' IS DISTINCT FROM NEW.plan_sha256) THEN RAISE EXCEPTION 'incomplete prompt plan'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_analysis_prompt_plan_identity BEFORE INSERT ON studio.world_analysis_prompt_plans FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_prompt_plan_guard();
