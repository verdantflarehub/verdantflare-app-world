CREATE TABLE studio.projects (
    project_id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    head_revision_id uuid,
    name text NOT NULL,
    category text NOT NULL,
    status text NOT NULL DEFAULT 'draft',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, project_id)
);
CREATE TABLE studio.project_members (
    organization_id uuid NOT NULL,
    project_id uuid NOT NULL,
    subject_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('owner','editor','reader')),
    PRIMARY KEY (organization_id, project_id, subject_id),
    FOREIGN KEY (organization_id, project_id) REFERENCES studio.projects(organization_id, project_id)
);
CREATE INDEX project_members_subject ON studio.project_members(organization_id,subject_id,project_id);
CREATE TABLE studio.project_create_requests (
    organization_id uuid NOT NULL,
    subject_id uuid NOT NULL,
    commit_id uuid NOT NULL,
    project_id uuid NOT NULL,
    request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (organization_id,subject_id,commit_id),
    FOREIGN KEY (organization_id,project_id) REFERENCES studio.projects(organization_id,project_id)
);
CREATE TABLE studio.project_commits (
    organization_id uuid NOT NULL,
    project_id uuid NOT NULL,
    commit_id uuid NOT NULL,
    created_by uuid NOT NULL,
    request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    expected_revision_id uuid,
    revision_id uuid NOT NULL UNIQUE,
    state text NOT NULL DEFAULT 'preparing' CHECK (state IN ('preparing','committed','conflict','failed')),
    plan jsonb NOT NULL,
    result jsonb,
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id,project_id,commit_id),
    FOREIGN KEY (organization_id,project_id) REFERENCES studio.projects(organization_id,project_id),
    CHECK ((state='committed') = (result IS NOT NULL)),
    CHECK ((state IN ('conflict','failed')) = (error_code IS NOT NULL))
);
CREATE TABLE studio.project_revisions (
    revision_id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    project_id uuid NOT NULL,
    parent_revision_id uuid,
    manifest_ref jsonb NOT NULL,
    manifest_json jsonb NOT NULL,
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    commit_id uuid NOT NULL,
    UNIQUE (organization_id,project_id,revision_id),
    UNIQUE (organization_id,project_id,commit_id),
    FOREIGN KEY (organization_id,project_id,commit_id) REFERENCES studio.project_commits(organization_id,project_id,commit_id),
    FOREIGN KEY (organization_id,project_id,parent_revision_id) REFERENCES studio.project_revisions(organization_id,project_id,revision_id)
);
ALTER TABLE studio.projects ADD CONSTRAINT project_head_revision FOREIGN KEY (organization_id,project_id,head_revision_id) REFERENCES studio.project_revisions(organization_id,project_id,revision_id);
CREATE TABLE studio.studio_events (
    event_id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    subject_id uuid NOT NULL,
    request_id uuid NOT NULL,
    action text NOT NULL,
    resource_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE FUNCTION studio.reject_immutable_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'immutable Studio record'; END;
$$;
CREATE TRIGGER project_revisions_immutable BEFORE UPDATE OR DELETE ON studio.project_revisions FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
CREATE TRIGGER project_revisions_no_truncate BEFORE TRUNCATE ON studio.project_revisions FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
CREATE TRIGGER studio_events_immutable BEFORE UPDATE OR DELETE ON studio.studio_events FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
CREATE TRIGGER studio_events_no_truncate BEFORE TRUNCATE ON studio.studio_events FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
CREATE TRIGGER project_create_requests_immutable BEFORE UPDATE OR DELETE ON studio.project_create_requests FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
CREATE TRIGGER project_create_requests_no_truncate BEFORE TRUNCATE ON studio.project_create_requests FOR EACH STATEMENT EXECUTE FUNCTION studio.reject_immutable_change();
