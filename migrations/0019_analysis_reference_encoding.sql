-- Reversible transport references; original observations and terminal rows stay immutable.
CREATE FUNCTION studio.world_analysis_short_references(source jsonb) RETURNS jsonb LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE aliases jsonb; nodes jsonb:='[]'; bounds jsonb:='[]'; n jsonb; item record; links jsonb; refs jsonb; key text; idx integer:=0;
BEGIN
 SELECT jsonb_object_agg(value->>'key','e'||lpad(ordinality::text,6,'0')) INTO aliases FROM jsonb_array_elements(source->'nodes') WITH ORDINALITY;
 FOR n IN SELECT value FROM jsonb_array_elements(source->'nodes') LOOP
  idx:=idx+1; links:='{}';
  FOR item IN SELECT * FROM jsonb_each(n->'links') LOOP
   refs:='[]';
   FOR key IN SELECT jsonb_array_elements_text(item.value) LOOP
    IF NOT aliases ? key THEN RAISE EXCEPTION 'unknown source reference'; END IF;
    refs:=refs||jsonb_build_array(aliases->>key);
   END LOOP;
   links:=links||jsonb_build_object(item.key,refs);
  END LOOP;
  nodes:=nodes||jsonb_build_array(n||jsonb_build_object('key','e'||lpad(idx::text,6,'0'),'links',links));
 END LOOP;
 FOR n IN SELECT value FROM jsonb_array_elements(source->'boundaries') LOOP
  bounds:=bounds||jsonb_build_array(n||jsonb_build_object('left_shot',aliases->>(n->>'left_shot'),'right_shot',aliases->>(n->>'right_shot')));
 END LOOP;
 RETURN source||jsonb_build_object('nodes',nodes,'boundaries',bounds);
END;
$$;
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
