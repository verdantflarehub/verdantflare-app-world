CREATE TABLE studio.world_assets (
    asset_id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    head_version_id uuid,
    asset_type text NOT NULL,
    name text NOT NULL,
    subjects jsonb NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(organization_id,asset_id)
);
CREATE TABLE studio.world_asset_grants (
    organization_id uuid NOT NULL,
    asset_id uuid NOT NULL,
    subject_id uuid NOT NULL,
    role text NOT NULL CHECK(role IN ('owner','editor','reader')),
    PRIMARY KEY(organization_id,asset_id,subject_id),
    FOREIGN KEY(organization_id,asset_id) REFERENCES studio.world_assets(organization_id,asset_id)
);
CREATE INDEX world_asset_grants_subject ON studio.world_asset_grants(organization_id,subject_id,asset_id);
CREATE TABLE studio.world_create_requests (
    organization_id uuid NOT NULL,
    subject_id uuid NOT NULL,
    commit_id uuid NOT NULL,
    asset_id uuid NOT NULL,
    request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY(organization_id,subject_id,commit_id),
    FOREIGN KEY(organization_id,asset_id) REFERENCES studio.world_assets(organization_id,asset_id)
);
CREATE TABLE studio.world_commits (
    organization_id uuid NOT NULL,
    asset_id uuid NOT NULL,
    commit_id uuid NOT NULL,
    created_by uuid NOT NULL,
    request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
    expected_version_id uuid,
    version_id uuid NOT NULL UNIQUE,
    state text NOT NULL DEFAULT 'preparing' CHECK(state IN ('preparing','committed','conflict','failed')),
    plan jsonb NOT NULL,
    result jsonb,
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(organization_id,asset_id,commit_id),
    FOREIGN KEY(organization_id,asset_id) REFERENCES studio.world_assets(organization_id,asset_id),
    CHECK((state='committed')=(result IS NOT NULL)),
    CHECK((state IN ('conflict','failed'))=(error_code IS NOT NULL))
);
CREATE TABLE studio.world_asset_versions (
    version_id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    asset_id uuid NOT NULL,
    parent_version_id uuid,
    source_project_id uuid NOT NULL,
    source_revision_id uuid NOT NULL,
    manifest_ref jsonb NOT NULL,
    manifest_json jsonb NOT NULL,
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    commit_id uuid NOT NULL,
    UNIQUE(organization_id,asset_id,version_id),
    UNIQUE(organization_id,asset_id,commit_id),
    FOREIGN KEY(organization_id,asset_id,commit_id) REFERENCES studio.world_commits(organization_id,asset_id,commit_id),
    FOREIGN KEY(organization_id,asset_id,parent_version_id) REFERENCES studio.world_asset_versions(organization_id,asset_id,version_id),
    FOREIGN KEY(organization_id,source_project_id,source_revision_id) REFERENCES studio.project_revisions(organization_id,project_id,revision_id)
);
ALTER TABLE studio.world_assets ADD CONSTRAINT world_head_version FOREIGN KEY(organization_id,asset_id,head_version_id) REFERENCES studio.world_asset_versions(organization_id,asset_id,version_id);
CREATE TABLE studio.world_version_dependencies (
    organization_id uuid NOT NULL,
    asset_id uuid NOT NULL,
    version_id uuid NOT NULL,
    dependency_asset_id uuid NOT NULL,
    dependency_version_id uuid NOT NULL,
    purpose text NOT NULL,
    PRIMARY KEY(organization_id,version_id,dependency_asset_id,dependency_version_id,purpose),
    FOREIGN KEY(organization_id,asset_id,version_id) REFERENCES studio.world_asset_versions(organization_id,asset_id,version_id),
    FOREIGN KEY(organization_id,dependency_asset_id,dependency_version_id) REFERENCES studio.world_asset_versions(organization_id,asset_id,version_id)
);
CREATE TRIGGER world_versions_immutable BEFORE UPDATE OR DELETE ON studio.world_asset_versions FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
CREATE TRIGGER world_versions_no_truncate BEFORE TRUNCATE ON studio.world_asset_versions FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
CREATE TRIGGER world_dependencies_immutable BEFORE UPDATE OR DELETE ON studio.world_version_dependencies FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
CREATE TRIGGER world_dependencies_no_truncate BEFORE TRUNCATE ON studio.world_version_dependencies FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
CREATE TRIGGER world_create_immutable BEFORE UPDATE OR DELETE ON studio.world_create_requests FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
CREATE TRIGGER world_create_no_truncate BEFORE TRUNCATE ON studio.world_create_requests FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
