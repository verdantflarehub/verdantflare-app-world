CREATE TABLE studio.world_analysis_association_validations (
 organization_id uuid NOT NULL, owner_id uuid NOT NULL, task_id uuid NOT NULL,
 retry_id uuid NOT NULL, request_sha256 text NOT NULL, call_id uuid NOT NULL,
 native_result jsonb NOT NULL, native_sha256 text NOT NULL CHECK(native_sha256 ~ '^[0-9a-f]{64}$'),
 policy text NOT NULL CHECK(policy='evidence-graph-v2'), created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,owner_id,task_id), UNIQUE(organization_id,owner_id,retry_id),
 FOREIGN KEY(organization_id,owner_id,task_id) REFERENCES studio.world_analysis_tasks(organization_id,owner_id,task_id)
);
CREATE OR REPLACE FUNCTION studio.world_analysis_retry_id_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended(NEW.organization_id::text||NEW.owner_id::text||NEW.retry_id::text,9018));
 IF EXISTS(SELECT 1 FROM studio.world_analysis_step_retries WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND retry_id=NEW.retry_id)
 OR EXISTS(SELECT 1 FROM studio.world_analysis_association_retries WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND retry_id=NEW.retry_id)
 OR EXISTS(SELECT 1 FROM studio.world_analysis_association_validations WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND retry_id=NEW.retry_id) THEN RAISE EXCEPTION 'analysis retry id already used'; END IF;
 RETURN NEW;
END; $$;
CREATE FUNCTION studio.world_analysis_association_validation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior record;
BEGIN
 PERFORM 1 FROM studio.world_analysis_tasks WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND pipeline_version=2 FOR NO KEY UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'validation requires versioned owner task'; END IF;
 SELECT * INTO prior FROM studio.world_analysis_current_associations WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id;
 IF prior.state IS DISTINCT FROM 'failed' OR prior.error_code IS DISTINCT FROM 'association_invalid'
 OR prior.request_sha256 IS DISTINCT FROM NEW.request_sha256 OR prior.call_id IS DISTINCT FROM NEW.call_id OR prior.native_result IS DISTINCT FROM NEW.native_result
 OR NOT COALESCE(NEW.native_result @> jsonb_build_object('state','completed','analysis',jsonb_build_object('finish_reason','STOP','response_schema_verified',true)),false)
 OR EXISTS(SELECT 1 FROM studio.world_analysis_current_steps WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'observation_ready')
 OR EXISTS(SELECT 1 FROM studio.world_analysis_result_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id)
 OR EXISTS(SELECT 1 FROM studio.world_analysis_prompt_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id)
 THEN RAISE EXCEPTION 'validation must preserve the complete original failure'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER world_analysis_retry_id BEFORE INSERT ON studio.world_analysis_association_validations FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_retry_id_guard();
CREATE TRIGGER world_analysis_validation_admission BEFORE INSERT ON studio.world_analysis_association_validations FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_association_validation_guard();
CREATE TRIGGER world_analysis_validation_immutable BEFORE UPDATE OR DELETE ON studio.world_analysis_association_validations FOR EACH ROW EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_analysis_validation_no_truncate BEFORE TRUNCATE ON studio.world_analysis_association_validations FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_association_validations FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_association_validations ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE OR REPLACE VIEW studio.world_analysis_current_associations AS
 SELECT r.organization_id,r.owner_id,r.task_id,r.request_sha256,r.request,r.revision,
 CASE WHEN v.retry_id IS NOT NULL THEN 'association_ready' ELSE r.state END AS state,
 r.call_id,r.native_result, CASE WHEN v.retry_id IS NOT NULL THEN NULL::text ELSE r.error_code END AS error_code,r.updated_at,
 v.retry_id AS validation_id,v.policy AS validation_policy,v.native_sha256 AS validation_native_sha256
 FROM (SELECT DISTINCT ON(organization_id,owner_id,task_id) * FROM (
  SELECT organization_id,owner_id,task_id,request_sha256,request,revision,state,call_id,native_result,error_code,updated_at,0 AS attempt FROM studio.world_analysis_associations
  UNION ALL SELECT organization_id,owner_id,task_id,request_sha256,request,revision,state,call_id,native_result,error_code,updated_at,attempt FROM studio.world_analysis_association_retries
 ) records ORDER BY organization_id,owner_id,task_id,attempt DESC) r
 LEFT JOIN studio.world_analysis_association_validations v ON v.organization_id=r.organization_id AND v.owner_id=r.owner_id AND v.task_id=r.task_id AND v.request_sha256=r.request_sha256 AND v.call_id=r.call_id AND v.native_result=r.native_result;
