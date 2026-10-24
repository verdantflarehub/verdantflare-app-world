CREATE OR REPLACE FUNCTION studio.world_analysis_prompt_diagnostic_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior record; receipt jsonb; diagnostic text;
BEGIN
 diagnostic := NEW.request->'retry'->>'diagnostic';
 IF NEW.attempt<>3 AND diagnostic IS NULL THEN RETURN NEW; END IF;
 SELECT * INTO prior FROM studio.world_analysis_current_prompts WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND batch_key=NEW.batch_key;
 receipt := NEW.request->'retry'->'failure_receipt';
 IF prior.state IS DISTINCT FROM 'failed'
 OR (NEW.attempt=3 AND prior.request->'retry'->>'number' IS DISTINCT FROM '2')
 OR receipt->>'call_id' IS DISTINCT FROM prior.call_id::text
 OR receipt->>'task_id' IS DISTINCT FROM NEW.task_id::text
 OR receipt->>'model_id' IS DISTINCT FROM NEW.request->>'model_id'
 OR receipt->'analysis'->>'finish_reason' IS DISTINCT FROM 'STOP'
 OR receipt->'analysis'->'input'->>'source_text_sha256' IS DISTINCT FROM NEW.request->>'source_text_sha256'
 OR (prior.native_result IS NOT NULL AND prior.native_result IS DISTINCT FROM receipt) THEN
  RAISE EXCEPTION 'prompt diagnostic requires bound complete native receipt';
 END IF;
 IF diagnostic='prompt-empty-negative-v1' AND NEW.attempt=3 THEN
  IF prior.error_code IS DISTINCT FROM 'native_failed' OR receipt->>'state' IS DISTINCT FROM 'failed'
  OR receipt->>'reason_code' IS DISTINCT FROM 'output_schema_mismatch'
  OR receipt->'analysis'->'response_schema_verified' IS DISTINCT FROM 'false'::jsonb THEN RAISE EXCEPTION 'prompt schema diagnostic mismatch'; END IF;
 ELSIF diagnostic='prompt-evidence-v1' THEN
  IF prior.error_code IS DISTINCT FROM 'prompt_invalid' OR prior.native_result IS DISTINCT FROM receipt
  OR receipt->>'state' IS DISTINCT FROM 'completed'
  OR receipt->'analysis'->'response_schema_verified' IS DISTINCT FROM 'true'::jsonb THEN RAISE EXCEPTION 'prompt evidence diagnostic mismatch'; END IF;
 ELSE RAISE EXCEPTION 'unsupported prompt diagnostic';
 END IF;
 RETURN NEW;
END; $$;
