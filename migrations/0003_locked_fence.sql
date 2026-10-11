CREATE FUNCTION studio.world_locked_fence(coordinator_name text) RETURNS SETOF bigint LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog AS $$
SELECT fence FROM studio.world_coord_fences WHERE coordinator=coordinator_name FOR SHARE;
$$;
REVOKE ALL ON FUNCTION studio.world_locked_fence(text) FROM PUBLIC;
CREATE OR REPLACE FUNCTION studio.world_runtime_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
SELECT state='active'
AND NOT pg_has_role(session_user,migration_owner,'MEMBER')
AND NOT pg_has_role(session_user,(SELECT nspowner FROM pg_namespace WHERE nspname='studio'),'MEMBER')
AND NOT has_schema_privilege(session_user,'studio','CREATE')
AND NOT EXISTS(SELECT 1 FROM pg_class WHERE relnamespace='studio'::regnamespace AND relkind IN ('r','p') AND pg_has_role(session_user,relowner,'MEMBER'))
AND NOT (SELECT rolsuper FROM pg_roles WHERE rolname=session_user)
AND NOT has_table_privilege(session_user,'studio.world_handoff','INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(session_user,'studio.world_coord_fences','INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(session_user,'studio.world_schema_history','INSERT,UPDATE,DELETE,TRUNCATE')
AND has_function_privilege(session_user,'studio.world_locked_fence(text)','EXECUTE')
FROM studio.world_handoff WHERE singleton;
$$;
