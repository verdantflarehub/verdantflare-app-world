-- Legacy tasks retain their original execution scope and digest.
ALTER TABLE studio.world_analysis_tasks ADD COLUMN include_typed_results boolean NOT NULL DEFAULT false;
CREATE TABLE studio.world_analysis_result_batches (
 organization_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 task_id uuid NOT NULL,
 batch_key text NOT NULL CHECK(batch_key ~ '^results-v1-(character|scene|prop|shot|sound_cue|music_structure|script_unit|style|method)-[0-9]{4}$'),
 batch_index integer NOT NULL CHECK(batch_index>=0 AND batch_index<2048),
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','dispatching','running','acceptance_unknown','result_ready','failed')),
 call_id uuid,
 native_result jsonb,
 error_code text,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,owner_id,task_id,batch_key),
 UNIQUE(organization_id,owner_id,task_id,batch_index),
 FOREIGN KEY(organization_id,owner_id,task_id) REFERENCES studio.world_analysis_tasks(organization_id,owner_id,task_id),
 CHECK(state NOT IN ('running','result_ready') OR call_id IS NOT NULL),
 CHECK(state<>'planned' OR call_id IS NULL),
 CHECK(state IN ('result_ready','failed') OR (native_result IS NULL AND error_code IS NULL)),
 CHECK(state<>'result_ready' OR (native_result IS NOT NULL AND error_code IS NULL)),
 CHECK((state='failed')=(error_code IS NOT NULL))
);
CREATE FUNCTION studio.world_analysis_result_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE enabled boolean; ready boolean; targets jsonb;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT t.include_typed_results,a.state='association_ready',t.prompt_targets INTO enabled,ready,targets
   FROM studio.world_analysis_tasks t JOIN studio.world_analysis_associations a USING(organization_id,owner_id,task_id)
   WHERE t.organization_id=NEW.organization_id AND t.owner_id=NEW.owner_id AND t.task_id=NEW.task_id;
  IF COALESCE(ready,false)=false OR COALESCE(enabled,false)=false
   OR NEW.request->'batch'->>'batch_key' IS DISTINCT FROM NEW.batch_key
   OR NEW.request->>'task_id' IS DISTINCT FROM NEW.task_id::text
   OR NEW.state<>'planned' OR NEW.revision<>1 THEN
   RAISE EXCEPTION 'result batch requires ready association and frozen task option';
  END IF;
  IF jsonb_array_length(targets)>0 AND (
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
CREATE TRIGGER world_analysis_result_identity BEFORE INSERT OR UPDATE OR DELETE ON studio.world_analysis_result_batches FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_result_guard();
CREATE TRIGGER world_analysis_result_batches_no_truncate BEFORE TRUNCATE ON studio.world_analysis_result_batches FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_result_batches FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_result_batches ENABLE ALWAYS TRIGGER world_handoff_gate;
