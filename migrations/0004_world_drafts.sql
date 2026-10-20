CREATE TABLE studio.world_drafts (
 draft_id uuid PRIMARY KEY,
 organization_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 purpose text NOT NULL CHECK(purpose IN ('collection','learning','creation')),
 name text NOT NULL,
 subjects jsonb NOT NULL CHECK(jsonb_typeof(subjects)='array'),
 head_revision_id uuid,
 has_content boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(organization_id,draft_id)
);
CREATE INDEX world_drafts_owner ON studio.world_drafts(organization_id,owner_id,purpose,draft_id);
CREATE TABLE studio.world_draft_commits (
 organization_id uuid NOT NULL,
 created_by uuid NOT NULL,
 commit_id uuid NOT NULL,
 draft_id uuid NOT NULL,
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 expected_revision_id uuid,
 revision_id uuid NOT NULL UNIQUE,
 state text NOT NULL DEFAULT 'preparing' CHECK(state IN ('preparing','committed','conflict','failed')),
 plan jsonb NOT NULL,
 result jsonb,
 error_code text,
 conflict_revision_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(organization_id,created_by,commit_id),
 UNIQUE(organization_id,draft_id,revision_id),
 FOREIGN KEY(organization_id,draft_id) REFERENCES studio.world_drafts(organization_id,draft_id),
 CHECK((state='committed')=(result IS NOT NULL)),
 CHECK((state IN ('conflict','failed'))=(error_code IS NOT NULL)),
 CHECK((state='conflict')=(conflict_revision_id IS NOT NULL))
);
CREATE INDEX world_draft_recovery ON studio.world_draft_commits(updated_at) WHERE state='preparing';
CREATE TABLE studio.world_draft_revisions (
 revision_id uuid PRIMARY KEY,
 organization_id uuid NOT NULL,
 draft_id uuid NOT NULL,
 parent_revision_id uuid,
 manifest_ref jsonb NOT NULL,
 manifest_json jsonb NOT NULL,
 created_by uuid NOT NULL,
 commit_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(organization_id,draft_id,revision_id),
 FOREIGN KEY(organization_id,draft_id,revision_id) REFERENCES studio.world_draft_commits(organization_id,draft_id,revision_id),
 FOREIGN KEY(organization_id,created_by,commit_id) REFERENCES studio.world_draft_commits(organization_id,created_by,commit_id),
 FOREIGN KEY(organization_id,draft_id,parent_revision_id) REFERENCES studio.world_draft_revisions(organization_id,draft_id,revision_id)
);
ALTER TABLE studio.world_drafts ADD CONSTRAINT world_draft_head FOREIGN KEY(organization_id,draft_id,head_revision_id) REFERENCES studio.world_draft_revisions(organization_id,draft_id,revision_id);
CREATE TRIGGER world_draft_revisions_immutable BEFORE UPDATE OR DELETE ON studio.world_draft_revisions FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_draft_revisions_no_truncate BEFORE TRUNCATE ON studio.world_draft_revisions FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE FUNCTION studio.world_draft_identity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.organization_id IS DISTINCT FROM OLD.organization_id OR NEW.draft_id IS DISTINCT FROM OLD.draft_id OR NEW.owner_id IS DISTINCT FROM OLD.owner_id OR NEW.purpose IS DISTINCT FROM OLD.purpose THEN
  RAISE EXCEPTION 'immutable World draft identity';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_draft_identity BEFORE UPDATE ON studio.world_drafts FOR EACH ROW EXECUTE FUNCTION studio.world_draft_identity_guard();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_drafts FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_drafts ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_draft_commits FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_draft_commits ENABLE ALWAYS TRIGGER world_handoff_gate;
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_draft_revisions FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_draft_revisions ENABLE ALWAYS TRIGGER world_handoff_gate;
