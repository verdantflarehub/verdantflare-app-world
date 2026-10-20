-- Prompt targets are frozen with new tasks; legacy tasks request no new calls.
ALTER TABLE studio.world_analysis_tasks ADD COLUMN prompt_targets jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(prompt_targets)='array' AND jsonb_array_length(prompt_targets)<=16);
CREATE TABLE studio.world_analysis_prompt_batches (
 organization_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 task_id uuid NOT NULL,
 batch_key text NOT NULL CHECK(batch_key ~ '^prompts-v1-[0-9]{4}$'),
 batch_index integer NOT NULL CHECK(batch_index>=0 AND batch_index<2048),
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','dispatching','running','acceptance_unknown','prompt_ready','failed')),
 call_id uuid,
 native_result jsonb,
 error_code text,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,owner_id,task_id,batch_key),
 UNIQUE(organization_id,owner_id,task_id,batch_index),
 CHECK(batch_key='prompts-v1-'||lpad(batch_index::text,4,'0')),
 FOREIGN KEY(organization_id,owner_id,task_id) REFERENCES studio.world_analysis_tasks(organization_id,owner_id,task_id),
 CHECK(state NOT IN ('running','prompt_ready') OR call_id IS NOT NULL),
 CHECK(state<>'planned' OR call_id IS NULL),
 CHECK(state IN ('prompt_ready','failed') OR (native_result IS NULL AND error_code IS NULL)),
 CHECK(state<>'prompt_ready' OR (native_result IS NOT NULL AND error_code IS NULL)),
 CHECK((state='failed')=(error_code IS NOT NULL))
);
CREATE FUNCTION studio.world_analysis_prompt_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE targets jsonb; ready boolean;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT t.prompt_targets,a.state='association_ready' INTO targets,ready
   FROM studio.world_analysis_tasks t JOIN studio.world_analysis_associations a USING(organization_id,owner_id,task_id)
   WHERE t.organization_id=NEW.organization_id AND t.owner_id=NEW.owner_id AND t.task_id=NEW.task_id;
  IF COALESCE(ready,false)=false OR COALESCE(jsonb_array_length(targets),0)<1
   OR NEW.request->'batch'->'targets' IS DISTINCT FROM targets
   OR NEW.request->'batch'->>'batch_key' IS DISTINCT FROM NEW.batch_key
   OR NEW.request->>'task_id' IS DISTINCT FROM NEW.task_id::text
   OR NEW.state<>'planned' OR NEW.revision<>1 THEN
   RAISE EXCEPTION 'prompt batch requires ready association and frozen task targets';
  END IF;
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
CREATE TRIGGER world_analysis_prompt_identity BEFORE INSERT OR UPDATE OR DELETE ON studio.world_analysis_prompt_batches FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_prompt_guard();
CREATE TRIGGER world_analysis_prompt_batches_no_truncate BEFORE TRUNCATE ON studio.world_analysis_prompt_batches FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_prompt_batches FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_prompt_batches ENABLE ALWAYS TRIGGER world_handoff_gate;
