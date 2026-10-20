ALTER TABLE studio.world_analysis_tasks ADD COLUMN save_learning boolean NOT NULL DEFAULT false;
ALTER TABLE studio.world_analysis_tasks ADD CONSTRAINT world_learning_requires_results CHECK(NOT save_learning OR include_typed_results);
CREATE TABLE studio.world_analysis_learning (
 organization_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 task_id uuid NOT NULL,
 assembly_version text NOT NULL CHECK(assembly_version='1'),
 document_sha256 text NOT NULL CHECK(document_sha256 ~ '^[0-9a-f]{64}$'),
 create_id uuid NOT NULL,
 write_id uuid NOT NULL,
 commit_id uuid NOT NULL,
 file_id uuid NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','saved','conflict','failed')),
 result jsonb,
 error_code text,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,owner_id,task_id),
 UNIQUE(organization_id,create_id),
 UNIQUE(organization_id,write_id),
 UNIQUE(organization_id,commit_id),
 FOREIGN KEY(organization_id,owner_id,task_id) REFERENCES studio.world_analysis_tasks(organization_id,owner_id,task_id),
 CHECK((state='saved')=(result IS NOT NULL)),
 CHECK(result IS NULL OR jsonb_typeof(result)='object'),
 CHECK((state IN ('conflict','failed'))=(error_code IS NOT NULL))
);
CREATE FUNCTION studio.world_analysis_learning_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NOT EXISTS(SELECT 1 FROM studio.world_analysis_tasks t WHERE t.organization_id=NEW.organization_id AND t.owner_id=NEW.owner_id AND t.task_id=NEW.task_id AND t.save_learning AND t.include_typed_results) THEN
   RAISE EXCEPTION 'learning save requires frozen task option';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'immutable learning save identity'; END IF;
 IF (NEW.organization_id,NEW.owner_id,NEW.task_id,NEW.assembly_version,NEW.document_sha256,NEW.create_id,NEW.write_id,NEW.commit_id,NEW.file_id) IS DISTINCT FROM (OLD.organization_id,OLD.owner_id,OLD.task_id,OLD.assembly_version,OLD.document_sha256,OLD.create_id,OLD.write_id,OLD.commit_id,OLD.file_id)
  OR OLD.state<>'pending' OR NEW.state='pending' THEN
  RAISE EXCEPTION 'invalid learning save transition';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_analysis_learning_identity BEFORE INSERT OR UPDATE OR DELETE ON studio.world_analysis_learning FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_learning_guard();
CREATE TRIGGER world_analysis_learning_no_truncate BEFORE TRUNCATE ON studio.world_analysis_learning FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_learning FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_learning ENABLE ALWAYS TRIGGER world_handoff_gate;
