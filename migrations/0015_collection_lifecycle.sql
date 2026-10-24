ALTER TABLE studio.world_drafts
 ADD COLUMN lifecycle_state text NOT NULL DEFAULT 'active' CHECK(lifecycle_state IN ('active','trashed')),
 ADD COLUMN lifecycle_version bigint NOT NULL DEFAULT 1 CHECK(lifecycle_version BETWEEN 1 AND 9007199254740991),
 ADD COLUMN trashed_at timestamptz,
 ADD COLUMN trashed_by uuid,
 ADD CONSTRAINT collection_lifecycle CHECK (
  (lifecycle_state='active' AND trashed_at IS NULL AND trashed_by IS NULL) OR
  (purpose='collection' AND lifecycle_state='trashed' AND trashed_at IS NOT NULL AND trashed_by IS NOT NULL AND trashed_by=owner_id));
CREATE INDEX world_drafts_lifecycle ON studio.world_drafts(organization_id,owner_id,purpose,lifecycle_state,draft_id);
CREATE TABLE studio.world_collection_operations (
 organization_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 operation_id uuid NOT NULL,
 draft_id uuid NOT NULL,
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,owner_id,operation_id),
 FOREIGN KEY(organization_id,draft_id) REFERENCES studio.world_drafts(organization_id,draft_id)
);
CREATE TRIGGER world_collection_operations_immutable BEFORE UPDATE OR DELETE ON studio.world_collection_operations FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_collection_operations_no_truncate BEFORE TRUNCATE ON studio.world_collection_operations FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_collection_operations FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_collection_operations ENABLE ALWAYS TRIGGER world_handoff_gate;
