-- Internal observation execution. No Project ownership and no asset completion.
CREATE TABLE studio.world_analysis_tasks (
 organization_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 task_id uuid NOT NULL,
 draft_id uuid NOT NULL,
 revision_id uuid NOT NULL,
 file_id uuid NOT NULL,
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,owner_id,task_id),
 UNIQUE(organization_id,task_id),
 FOREIGN KEY(organization_id,draft_id,revision_id) REFERENCES studio.world_draft_revisions(organization_id,draft_id,revision_id)
);
CREATE TABLE studio.world_analysis_steps (
 organization_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 task_id uuid NOT NULL,
 step_key text NOT NULL,
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','dispatching','running','acceptance_unknown','observation_ready','failed')),
 call_id uuid,
 native_result jsonb,
 report jsonb,
 error_code text,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,owner_id,task_id,step_key),
 FOREIGN KEY(organization_id,owner_id,task_id) REFERENCES studio.world_analysis_tasks(organization_id,owner_id,task_id),
 CHECK(state NOT IN ('running','observation_ready') OR call_id IS NOT NULL),
 CHECK(state<>'planned' OR call_id IS NULL),
 CHECK(state IN ('observation_ready','failed') OR (native_result IS NULL AND report IS NULL AND error_code IS NULL)),
 CHECK(state<>'observation_ready' OR (native_result IS NOT NULL AND report IS NOT NULL AND report @> '{"passed":true,"issues":[]}'::jsonb AND error_code IS NULL)),
 CHECK((state='failed')=(error_code IS NOT NULL))
);
CREATE INDEX world_analysis_pending ON studio.world_analysis_steps(updated_at) WHERE state NOT IN ('observation_ready','failed');
CREATE TRIGGER world_analysis_tasks_immutable BEFORE UPDATE OR DELETE ON studio.world_analysis_tasks FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_analysis_tasks_no_truncate BEFORE TRUNCATE ON studio.world_analysis_tasks FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE FUNCTION studio.world_analysis_step_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'immutable World observation record'; END IF;
 IF (NEW.organization_id,NEW.owner_id,NEW.task_id,NEW.step_key,NEW.request_sha256,NEW.request) IS DISTINCT FROM (OLD.organization_id,OLD.owner_id,OLD.task_id,OLD.step_key,OLD.request_sha256,OLD.request) THEN
  RAISE EXCEPTION 'immutable World observation request';
 END IF;
 IF OLD.state IN ('observation_ready','failed') OR NEW.revision<>OLD.revision+1 OR (OLD.call_id IS NOT NULL AND NEW.call_id IS DISTINCT FROM OLD.call_id) THEN
  RAISE EXCEPTION 'invalid World observation revision';
 END IF;
 IF (OLD.state='planned' AND NEW.state<>'dispatching') OR (OLD.state<>'planned' AND NEW.state IN ('planned','dispatching')) THEN
  RAISE EXCEPTION 'invalid World observation transition';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_analysis_step_identity BEFORE UPDATE OR DELETE ON studio.world_analysis_steps FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_step_guard();
CREATE TRIGGER world_analysis_steps_no_truncate BEFORE TRUNCATE ON studio.world_analysis_steps FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_tasks FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_tasks ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_steps FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_steps ENABLE ALWAYS TRIGGER world_handoff_gate;
