CREATE OR REPLACE FUNCTION studio.world_analysis_style_correction_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior record;
BEGIN
 IF NEW.attempt<>3 AND NEW.request->'retry'->>'diagnostic' IS DISTINCT FROM 'method-links-v1' THEN
  IF NEW.request->'retry'->>'diagnostic' IS NOT NULL THEN RAISE EXCEPTION 'unexpected result diagnostic'; END IF;
  RETURN NEW;
 END IF;
 SELECT * INTO prior FROM studio.world_analysis_current_results WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND batch_key=NEW.batch_key;
 IF prior.state IS DISTINCT FROM 'failed' OR prior.error_code IS DISTINCT FROM 'result_invalid'
 OR COALESCE((prior.request->'retry'->>'number')::integer,0)+1<>NEW.attempt
 OR NOT COALESCE(((NEW.request->'batch'->>'type_key'='style' AND NEW.request->'retry'->>'diagnostic'='style-representation-v1' AND NEW.attempt=3) OR (NEW.request->'batch'->>'type_key'='method' AND NEW.request->'retry'->>'diagnostic'='method-links-v1')),false)
 OR NEW.request->'retry'->'failure_receipt' IS DISTINCT FROM prior.native_result
 OR prior.native_result->>'state' IS DISTINCT FROM 'completed' THEN
  RAISE EXCEPTION 'diagnostic correction requires exact failed native record';
 END IF;
 RETURN NEW;
END; $$;
