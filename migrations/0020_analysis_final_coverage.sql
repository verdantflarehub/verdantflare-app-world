ALTER TABLE studio.world_analysis_association_retries DROP CONSTRAINT world_analysis_association_retries_attempt_check;
ALTER TABLE studio.world_analysis_association_retries ADD CONSTRAINT world_analysis_association_retries_attempt_check CHECK(attempt BETWEEN 1 AND 3);
CREATE OR REPLACE FUNCTION studio.world_analysis_association_retry_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior record;
BEGIN
 IF TG_OP='INSERT' THEN
  PERFORM 1 FROM studio.world_analysis_tasks WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND pipeline_version=2 FOR NO KEY UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'retry requires versioned owner task'; END IF;
  IF EXISTS(SELECT 1 FROM studio.world_analysis_current_steps WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'observation_ready')
   OR EXISTS(SELECT 1 FROM studio.world_analysis_result_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id)
   OR EXISTS(SELECT 1 FROM studio.world_analysis_prompt_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id)
   THEN RAISE EXCEPTION 'association retry must precede downstream work'; END IF;
  SELECT * INTO prior FROM studio.world_analysis_current_associations WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id;
  IF prior.state IS DISTINCT FROM 'failed' OR prior.error_code NOT IN ('association_invalid','native_failed') OR prior.call_id IS NULL
   OR NEW.state<>'planned' OR NEW.revision<>1 OR NEW.attempt<>COALESCE((prior.request->'retry'->>'number')::integer,0)+1
   OR NEW.request->'retry'->>'previous_request_sha256' IS DISTINCT FROM prior.request_sha256
   OR NEW.request->'retry'->>'previous_call_id' IS DISTINCT FROM prior.call_id::text
   OR (NEW.request-'retry'-'idempotency_key'-'payload'-'source_text_sha256'-'reference_encoding') IS DISTINCT FROM (prior.request-'retry'-'idempotency_key'-'payload'-'source_text_sha256'-'reference_encoding') THEN
   RAISE EXCEPTION 'retry requires the original failed association';
  END IF;
  IF NEW.attempt=3 AND (prior.error_code IS DISTINCT FROM 'association_invalid' OR prior.request->>'reference_encoding' IS DISTINCT FROM 'short-v1' OR NEW.request->'retry'->>'issue' NOT LIKE 'missing entity members: %') THEN RAISE EXCEPTION 'final correction requires complete native result with entity omissions'; END IF;
  IF prior.error_code='association_invalid' THEN
   IF prior.native_result IS NULL OR NEW.request->'retry' ? 'failure_receipt' THEN RAISE EXCEPTION 'semantic retry requires original native result'; END IF;
  ELSE
   IF NOT COALESCE(
     NEW.request->'retry'->'failure_receipt' @> jsonb_build_object('schema_version',1,'call_id',prior.call_id::text,'task_id',NEW.task_id::text,'model_id',prior.request->>'model_id','media','analysis','operation','analyze','state','failed','reason_code','output_incomplete')
     AND NEW.request->'retry'->'failure_receipt'->'analysis' @> jsonb_build_object('model_id',prior.request->>'model_id','finish_reason','MAX_TOKENS','response_schema_verified',false,'document',NULL)
     AND NEW.request->'retry'->'failure_receipt'->'analysis'->'input'->>'source_text_sha256'=prior.request->>'source_text_sha256'
     AND length(NEW.request->'retry'->'failure_receipt'->'analysis'->>'text')>0,false) THEN RAISE EXCEPTION 'retry requires bound native truncation receipt'; END IF;
  END IF;
  IF NEW.request->'reference_encoding' IS NOT DISTINCT FROM prior.request->'reference_encoding' THEN
   IF NEW.request->'source_text_sha256' IS DISTINCT FROM prior.request->'source_text_sha256'
    OR ((NEW.request->'payload')-'prompt') IS DISTINCT FROM ((prior.request->'payload')-'prompt') THEN RAISE EXCEPTION 'retry changed source'; END IF;
  ELSE
   IF prior.error_code<>'native_failed' OR prior.request ? 'reference_encoding' OR NEW.request->>'reference_encoding' IS DISTINCT FROM 'short-v1'
    OR ((NEW.request->'payload')-'prompt'-'source_text') IS DISTINCT FROM ((prior.request->'payload')-'prompt'-'source_text')
    OR (NEW.request->'payload'->>'source_text')::jsonb IS DISTINCT FROM studio.world_analysis_short_references((prior.request->'payload'->>'source_text')::jsonb)
    THEN RAISE EXCEPTION 'retry changed observation content'; END IF;
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP<>'UPDATE' OR NEW.attempt<>OLD.attempt OR NEW.retry_id<>OLD.retry_id THEN RAISE EXCEPTION 'immutable retry identity'; END IF;
 RETURN NEW;
END;
$$;
