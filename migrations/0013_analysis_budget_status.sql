-- Keep the published admission migration immutable. Existing diagnostics and
-- leases are preserved while allowing the scheduler to persist a budget stop.
ALTER TABLE studio.world_analysis_schedule DROP CONSTRAINT world_analysis_schedule_last_error_code_check;
ALTER TABLE studio.world_analysis_schedule ADD CONSTRAINT world_analysis_schedule_last_error_code_check
 CHECK(last_error_code IN ('','dependency_unavailable','acceptance_unknown','source_unavailable','invalid_observation','step_failed','interrupted','budget_exhausted'));
