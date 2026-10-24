ALTER TABLE studio.world_analysis_result_retries DROP CONSTRAINT world_analysis_result_retries_attempt_check;
ALTER TABLE studio.world_analysis_result_retries ADD CONSTRAINT world_analysis_result_retries_attempt_check CHECK(attempt BETWEEN 1 AND 3);
CREATE FUNCTION studio.world_analysis_style_correction_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior record;
BEGIN
 IF NEW.attempt<>3 THEN
  IF NEW.request->'retry'->>'diagnostic' IS NOT NULL THEN RAISE EXCEPTION 'unexpected result diagnostic'; END IF;
  RETURN NEW;
 END IF;
 SELECT * INTO prior FROM studio.world_analysis_current_results WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND batch_key=NEW.batch_key;
 IF prior.state IS DISTINCT FROM 'failed' OR prior.error_code IS DISTINCT FROM 'result_invalid'
 OR prior.request->'retry'->>'number' IS DISTINCT FROM '2'
 OR NEW.request->'batch'->>'type_key' IS DISTINCT FROM 'style'
 OR NEW.request->'retry'->>'diagnostic' IS DISTINCT FROM 'style-representation-v1'
 OR NEW.request->'retry'->'failure_receipt' IS DISTINCT FROM prior.native_result
 OR prior.native_result->>'state' IS DISTINCT FROM 'completed' THEN
  RAISE EXCEPTION 'final style correction requires exact failed native record';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER world_analysis_style_correction BEFORE INSERT ON studio.world_analysis_result_retries FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_style_correction_guard();
