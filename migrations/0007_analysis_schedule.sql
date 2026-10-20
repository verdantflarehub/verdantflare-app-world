-- Mutable scheduling is separate from immutable plans and native request state.
CREATE TABLE studio.world_analysis_schedule (
 organization_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 task_id uuid NOT NULL,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 lease_id uuid,
 lease_until timestamptz,
 consecutive_errors integer NOT NULL DEFAULT 0 CHECK(consecutive_errors BETWEEN 0 AND 16),
 last_error_code text NOT NULL DEFAULT '' CHECK(last_error_code IN ('','dependency_unavailable','acceptance_unknown','source_unavailable','invalid_observation','step_failed','interrupted')),
 PRIMARY KEY(organization_id,owner_id,task_id),
 FOREIGN KEY(organization_id,owner_id,task_id) REFERENCES studio.world_analysis_tasks(organization_id,owner_id,task_id),
 CHECK((lease_id IS NULL)=(lease_until IS NULL))
);
-- This controlled migration may run with the writer closed. Install its gate
-- after backfill; immutable task/request rows are never updated.
INSERT INTO studio.world_analysis_schedule(organization_id,owner_id,task_id)
 SELECT organization_id,owner_id,task_id FROM studio.world_analysis_tasks;
CREATE INDEX world_analysis_schedule_due ON studio.world_analysis_schedule(next_attempt_at);
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_analysis_schedule FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_analysis_schedule ENABLE ALWAYS TRIGGER world_handoff_gate;
