-- One immutable association request per complete observation task.
CREATE TABLE studio.world_analysis_associations (
 organization_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 task_id uuid NOT NULL,
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 state text NOT NULL DEFAULT 'planned' CHECK(state IN ('planned','dispatching','running','acceptance_unknown','association_ready','failed')),
 call_id uuid,
 native_result jsonb,
 error_code text,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,owner_id,task_id),
 FOREIGN KEY(organization_id,owner_id,task_id) REFERENCES studio.world_analysis_tasks(organization_id,owner_id,task_id),
 CHECK(state NOT IN ('running','association_ready') OR call_id IS NOT NULL),
 CHECK(state<>'planned' OR call_id IS NULL),
 CHECK(state IN ('association_ready','failed') OR (native_result IS NULL AND error_code IS NULL)),
 CHECK(state<>'association_ready' OR (native_result IS NOT NULL AND error_code IS NULL)),
 CHECK((state='failed')=(error_code IS NOT NULL))
);
CREATE FUNCTION studio.world_analysis_association_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected integer; ready integer;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT jsonb_array_length(plan->'steps') INTO expected FROM studio.world_analysis_tasks
   WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id;
  SELECT count(*) INTO ready FROM studio.world_analysis_steps
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
CREATE TRIGGER world_analysis_association_identity BEFORE INSERT OR UPDATE OR DELETE ON studio.world_analysis_associations FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_association_guard();
CREATE TRIGGER world_analysis_associations_no_truncate BEFORE TRUNCATE ON studio.world_analysis_associations FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_associations FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_associations ENABLE ALWAYS TRIGGER world_handoff_gate;
