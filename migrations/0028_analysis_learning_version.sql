ALTER TABLE studio.world_analysis_requests ADD COLUMN assembly_version text NOT NULL DEFAULT '1'
 CHECK(assembly_version IN ('1','2') AND (assembly_version='1' OR pipeline_version=2));
ALTER TABLE studio.world_analysis_tasks ADD COLUMN assembly_version text NOT NULL DEFAULT '1'
 CHECK(assembly_version IN ('1','2') AND (assembly_version='1' OR pipeline_version=2));
ALTER TABLE studio.world_analysis_learning DROP CONSTRAINT world_analysis_learning_assembly_version_check;
ALTER TABLE studio.world_analysis_learning ADD CONSTRAINT world_analysis_learning_assembly_version_check CHECK(assembly_version IN ('1','2'));

CREATE FUNCTION studio.world_analysis_assembly_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected text;
BEGIN
 IF TG_OP='UPDATE' AND NEW.assembly_version IS DISTINCT FROM OLD.assembly_version THEN
  RAISE EXCEPTION 'immutable analysis assembly version';
 END IF;
 IF TG_OP='INSERT' AND TG_TABLE_NAME='world_analysis_tasks' THEN
  SELECT assembly_version INTO expected FROM studio.world_analysis_requests
   WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id;
  IF FOUND AND NEW.assembly_version IS DISTINCT FROM expected THEN
   RAISE EXCEPTION 'task assembly differs from frozen admission';
  END IF;
 END IF;
 IF TG_OP='INSERT' AND TG_TABLE_NAME='world_analysis_learning' THEN
  SELECT assembly_version INTO expected FROM studio.world_analysis_tasks
   WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id;
  IF NOT FOUND OR NEW.assembly_version IS DISTINCT FROM expected THEN
   RAISE EXCEPTION 'save assembly differs from frozen task';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_analysis_assembly_identity BEFORE INSERT OR UPDATE ON studio.world_analysis_requests
 FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_assembly_guard();
CREATE TRIGGER world_analysis_assembly_identity BEFORE INSERT OR UPDATE ON studio.world_analysis_tasks
 FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_assembly_guard();
CREATE TRIGGER world_analysis_assembly_identity BEFORE INSERT OR UPDATE ON studio.world_analysis_learning
 FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_assembly_guard();
