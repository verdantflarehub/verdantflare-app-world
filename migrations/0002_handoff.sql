CREATE TABLE studio.world_handoff (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 state text NOT NULL CHECK(state IN ('closed','importing','staged','active')),
 migration_owner name NOT NULL DEFAULT CURRENT_USER,
 handoff_id uuid,
 archive_digest text,
 domain_digest text,
 project_fence bigint,
 world_fence bigint,
 CHECK((handoff_id IS NULL)=(archive_digest IS NULL)),
 CHECK((handoff_id IS NULL)=(domain_digest IS NULL)),
 CHECK((handoff_id IS NULL)=(project_fence IS NULL)),
 CHECK((handoff_id IS NULL)=(world_fence IS NULL))
);
INSERT INTO studio.world_handoff(state) VALUES('closed');
CREATE FUNCTION studio.world_check_writer() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE gate studio.world_handoff;
BEGIN
 SELECT * INTO STRICT gate FROM studio.world_handoff WHERE singleton FOR SHARE;
 IF gate.state='active' OR (gate.state='importing' AND session_user=gate.migration_owner) THEN RETURN NULL; END IF;
 RAISE EXCEPTION 'business writer is closed' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_assets FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_assets ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_asset_grants FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_asset_grants ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_create_requests FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_create_requests ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_commits FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_commits ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_asset_versions FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_asset_versions ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_version_dependencies FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_version_dependencies ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_events FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_events ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_coord_operations FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_coord_operations ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_coord_participants FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_coord_participants ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_coord_barriers FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_coord_barriers ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_coord_reservations FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_coord_reservations ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_coord_outbox FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_coord_outbox ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_grant_changes FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_grant_changes ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE FUNCTION studio.world_runtime_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
SELECT state='active'
AND NOT pg_has_role(session_user,migration_owner,'MEMBER')
AND NOT pg_has_role(session_user,(SELECT nspowner FROM pg_namespace WHERE nspname='studio'),'MEMBER')
AND NOT has_schema_privilege(session_user,'studio','CREATE')
AND NOT EXISTS(SELECT 1 FROM pg_class WHERE relnamespace='studio'::regnamespace AND relkind IN ('r','p') AND pg_has_role(session_user,relowner,'MEMBER'))
AND NOT (SELECT rolsuper FROM pg_roles WHERE rolname=session_user)
AND NOT has_table_privilege(session_user,'studio.world_handoff','INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(session_user,'studio.world_coord_fences','INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(session_user,'studio.world_schema_history','INSERT,UPDATE,DELETE,TRUNCATE')
FROM studio.world_handoff WHERE singleton;
$$;
