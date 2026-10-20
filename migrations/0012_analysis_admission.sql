-- Public task admission is durable before media preparation. Legacy tasks keep
-- their original fingerprint and budget behavior.
ALTER TABLE studio.world_analysis_tasks ADD COLUMN total_budget text NOT NULL DEFAULT '';
ALTER TABLE studio.world_analysis_tasks ADD CONSTRAINT world_analysis_total_budget CHECK(CASE WHEN total_budget='' THEN true WHEN total_budget ~ '^(0|[1-9][0-9]{0,8})(\.[0-9]{1,8})?$' THEN total_budget::numeric>0 ELSE false END);
CREATE TABLE studio.world_analysis_requests (
 organization_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 task_id uuid NOT NULL,
 draft_id uuid NOT NULL,
 revision_id uuid NOT NULL,
 file_id uuid NOT NULL,
 source_sha256 text NOT NULL CHECK(source_sha256 ~ '^[0-9a-f]{64}$'),
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','preparing','queued','failed')),
 error_code text NOT NULL DEFAULT '',
 lease_id uuid,
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 consecutive_errors integer NOT NULL DEFAULT 0 CHECK(consecutive_errors>=0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,owner_id,task_id),
 UNIQUE(organization_id,task_id),
 FOREIGN KEY(organization_id,draft_id,revision_id) REFERENCES studio.world_draft_revisions(organization_id,draft_id,revision_id),
 CHECK((lease_id IS NULL)=(lease_until IS NULL))
);
CREATE FUNCTION studio.world_analysis_request_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.lease_id IS NOT NULL OR NEW.error_code<>'' THEN RAISE EXCEPTION 'invalid admission'; END IF;
  RETURN NEW;
 END IF;
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'immutable analysis request'; END IF;
 IF (NEW.organization_id,NEW.owner_id,NEW.task_id,NEW.draft_id,NEW.revision_id,NEW.file_id,NEW.source_sha256,NEW.request_sha256,NEW.request,NEW.created_at) IS DISTINCT FROM
    (OLD.organization_id,OLD.owner_id,OLD.task_id,OLD.draft_id,OLD.revision_id,OLD.file_id,OLD.source_sha256,OLD.request_sha256,OLD.request,OLD.created_at) THEN RAISE EXCEPTION 'immutable analysis request'; END IF;
 IF NEW.state IS DISTINCT FROM OLD.state AND NOT ((OLD.state='pending' AND NEW.state='preparing') OR (OLD.state='preparing' AND NEW.state IN ('queued','failed'))) THEN RAISE EXCEPTION 'invalid admission transition'; END IF;
 IF NEW.state='queued' AND NOT EXISTS(SELECT 1 FROM studio.world_analysis_tasks t WHERE t.organization_id=NEW.organization_id AND t.owner_id=NEW.owner_id AND t.task_id=NEW.task_id AND t.save_learning AND t.include_typed_results AND t.total_budget=NEW.request->>'budget_limit') THEN RAISE EXCEPTION 'admission requires complete task'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_analysis_request_identity BEFORE INSERT OR UPDATE OR DELETE ON studio.world_analysis_requests FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_request_guard();
CREATE TRIGGER world_analysis_request_no_truncate BEFORE TRUNCATE ON studio.world_analysis_requests FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_requests FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_requests ENABLE ALWAYS TRIGGER world_handoff_gate;

-- Count each frozen operation once on its first dispatch intent. Unknown
-- acceptance and failures remain reserved; no fabricated settlement/release.
CREATE FUNCTION studio.world_analysis_budget_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cap text; reserved numeric; amount numeric;
BEGIN
 IF OLD.state<>'planned' OR NEW.state<>'dispatching' THEN RETURN NEW; END IF;
 SELECT total_budget INTO cap FROM studio.world_analysis_tasks WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id FOR NO KEY UPDATE;
 IF cap='' THEN RETURN NEW; END IF;
 IF cap IS NULL OR NEW.request->>'budget_limit' IS NULL THEN RAISE EXCEPTION 'invalid analysis budget'; END IF;
 amount := (NEW.request->>'budget_limit')::numeric;
 SELECT COALESCE(sum((r.request->>'budget_limit')::numeric),0) INTO reserved FROM (
  SELECT request FROM studio.world_analysis_steps WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
  UNION ALL SELECT request FROM studio.world_analysis_associations WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
  UNION ALL SELECT request FROM studio.world_analysis_prompt_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
  UNION ALL SELECT request FROM studio.world_analysis_result_batches WHERE organization_id=NEW.organization_id AND owner_id=NEW.owner_id AND task_id=NEW.task_id AND state<>'planned'
 ) r;
 IF amount<=0 OR reserved+amount>cap::numeric THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='WORLD_ANALYSIS_BUDGET_EXHAUSTED'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_analysis_budget BEFORE UPDATE ON studio.world_analysis_steps FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_budget_guard();
CREATE TRIGGER world_analysis_budget BEFORE UPDATE ON studio.world_analysis_associations FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_budget_guard();
CREATE TRIGGER world_analysis_budget BEFORE UPDATE ON studio.world_analysis_prompt_batches FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_budget_guard();
CREATE TRIGGER world_analysis_budget BEFORE UPDATE ON studio.world_analysis_result_batches FOR EACH ROW EXECUTE FUNCTION studio.world_analysis_budget_guard();
