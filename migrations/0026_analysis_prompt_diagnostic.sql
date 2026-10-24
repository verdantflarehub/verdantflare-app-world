ALTER TABLE studio.world_analysis_prompt_retries DROP CONSTRAINT world_analysis_prompt_retries_attempt_check;
ALTER TABLE studio.world_analysis_prompt_retries ADD CONSTRAINT world_analysis_prompt_retries_attempt_check CHECK(attempt BETWEEN 1 AND 3);
CREATE FUNCTION studio.world_analysis_prompt_diagnostic_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior record; receipt jsonb;
BEGIN
 IF NEW.attempt<>3 THEN
  IF NEW.request->'retry'->>'diagnostic' IS NOT NULL THEN RAISE EXCEPTION 'unexpected prompt diagnostic'; END IF;
  RETURN NEW;
 END IF;
 SELECT * INTO prior FROM studio.world_analysis_current_prompts WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND batch_key=NEW.batch_key;
 receipt := NEW.request->'retry'->'failure_receipt';
 IF prior.state IS DISTINCT FROM 'failed' OR prior.error_code IS DISTINCT FROM 'native_failed'
 OR prior.request->'retry'->>'number' IS DISTINCT FROM '2'
 OR NEW.request->'retry'->>'diagnostic' IS DISTINCT FROM 'prompt-empty-negative-v1'
 OR receipt->>'call_id' IS DISTINCT FROM prior.call_id::text
 OR receipt->>'task_id' IS DISTINCT FROM NEW.task_id::text
 OR receipt->>'model_id' IS DISTINCT FROM NEW.request->>'model_id'
 OR receipt->>'state' IS DISTINCT FROM 'failed'
 OR receipt->>'reason_code' IS DISTINCT FROM 'output_schema_mismatch'
 OR receipt->'analysis'->>'finish_reason' IS DISTINCT FROM 'STOP'
 OR receipt->'analysis'->'response_schema_verified' IS DISTINCT FROM 'false'::jsonb
 OR receipt->'analysis'->'input'->>'source_text_sha256' IS DISTINCT FROM NEW.request->>'source_text_sha256'
 OR (prior.native_result IS NOT NULL AND prior.native_result IS DISTINCT FROM receipt) THEN
  RAISE EXCEPTION 'prompt diagnostic requires bound complete native schema failure';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER world_analysis_prompt_diagnostic BEFORE INSERT ON studio.world_analysis_prompt_retries FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_prompt_diagnostic_guard();
