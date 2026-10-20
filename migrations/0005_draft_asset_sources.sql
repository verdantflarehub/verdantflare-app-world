-- Keep all existing project-origin rows and immutable manifests unchanged.
ALTER TABLE studio.world_asset_versions ADD COLUMN source_draft_id uuid;
ALTER TABLE studio.world_asset_versions ALTER COLUMN source_project_id DROP NOT NULL;
ALTER TABLE studio.world_asset_versions ADD CONSTRAINT world_asset_source_context CHECK (
 (source_project_id IS NOT NULL) <> (source_draft_id IS NOT NULL)
);
ALTER TABLE studio.world_asset_versions ADD CONSTRAINT world_asset_source_draft_revision
 FOREIGN KEY(organization_id,source_draft_id,source_revision_id)
 REFERENCES studio.world_draft_revisions(organization_id,draft_id,revision_id);
