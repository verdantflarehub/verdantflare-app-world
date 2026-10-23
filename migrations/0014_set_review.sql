CREATE TABLE studio.world_set_review_heads (
 organization_id uuid NOT NULL, asset_id uuid NOT NULL, version_id uuid NOT NULL,
 target_sha256 text NOT NULL CHECK(target_sha256 ~ '^[0-9a-f]{64}$'), target_bytes bytea NOT NULL CHECK(encode(sha256(target_bytes),'hex')=target_sha256),
 head_record_id uuid, record_count bigint NOT NULL DEFAULT 0 CHECK(record_count>=0),
 PRIMARY KEY(organization_id,asset_id,version_id),
 FOREIGN KEY(organization_id,asset_id,version_id) REFERENCES studio.world_asset_versions(organization_id,asset_id,version_id));
CREATE TABLE studio.world_set_review_records (
 organization_id uuid NOT NULL, asset_id uuid NOT NULL, version_id uuid NOT NULL,
 record_id uuid NOT NULL, sequence bigint NOT NULL CHECK(sequence>0),
 target_sha256 text NOT NULL CHECK(target_sha256 ~ '^[0-9a-f]{64}$'), previous_record_id uuid,
 record_sha256 text NOT NULL CHECK(record_sha256 ~ '^[0-9a-f]{64}$'), record_bytes bytea NOT NULL CHECK(encode(sha256(record_bytes),'hex')=record_sha256),
 PRIMARY KEY(organization_id,asset_id,version_id,record_id),
 UNIQUE(organization_id,asset_id,version_id,sequence),
 FOREIGN KEY(organization_id,asset_id,version_id) REFERENCES studio.world_set_review_heads(organization_id,asset_id,version_id),
 FOREIGN KEY(organization_id,asset_id,version_id,previous_record_id) REFERENCES studio.world_set_review_records(organization_id,asset_id,version_id,record_id));
ALTER TABLE studio.world_set_review_heads ADD CONSTRAINT world_set_review_head_record FOREIGN KEY(organization_id,asset_id,version_id,head_record_id) REFERENCES studio.world_set_review_records(organization_id,asset_id,version_id,record_id);
CREATE TABLE studio.world_set_review_requests (
 organization_id uuid NOT NULL, asset_id uuid NOT NULL, commit_id uuid NOT NULL, version_id uuid NOT NULL,
 created_by uuid NOT NULL, origin text NOT NULL CHECK(origin IN ('agent','human_interactive')),
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 state text NOT NULL CHECK(state IN ('committed','conflict')), result jsonb, error_code text,
 PRIMARY KEY(organization_id,asset_id,commit_id),
 FOREIGN KEY(organization_id,asset_id,version_id) REFERENCES studio.world_asset_versions(organization_id,asset_id,version_id),
 CHECK((state='committed')=(result IS NOT NULL)), CHECK((state='conflict')=(error_code IS NOT NULL)));
CREATE TABLE studio.world_set_review_cursors (
 cursor_id uuid PRIMARY KEY, organization_id uuid NOT NULL, asset_id uuid NOT NULL, version_id uuid NOT NULL,
 subject_id uuid NOT NULL, target_sha256 text NOT NULL, snapshot_sequence bigint NOT NULL,
 snapshot_head uuid, page_offset integer NOT NULL CHECK(page_offset>=0), expires_at timestamptz NOT NULL,
 FOREIGN KEY(organization_id,asset_id,version_id,snapshot_head) REFERENCES studio.world_set_review_records(organization_id,asset_id,version_id,record_id));
CREATE FUNCTION studio.world_set_review_commit_namespace() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('world-commit:'||NEW.organization_id::text||':'||NEW.asset_id::text||':'||NEW.commit_id::text,0));
 IF TG_TABLE_NAME='world_commits' THEN
  IF EXISTS(SELECT 1 FROM studio.world_set_review_requests WHERE organization_id=NEW.organization_id AND asset_id=NEW.asset_id AND commit_id=NEW.commit_id) THEN RAISE EXCEPTION 'commit namespace already used by review'; END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM studio.world_commits WHERE organization_id=NEW.organization_id AND asset_id=NEW.asset_id AND commit_id=NEW.commit_id) THEN RAISE EXCEPTION 'commit namespace already used by publication'; END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_set_review_commit_namespace BEFORE INSERT ON studio.world_commits FOR EACH ROW EXECUTE FUNCTION studio.world_set_review_commit_namespace();
CREATE TRIGGER world_set_review_commit_namespace BEFORE INSERT ON studio.world_set_review_requests FOR EACH ROW EXECUTE FUNCTION studio.world_set_review_commit_namespace();
CREATE FUNCTION studio.world_set_review_keep_target() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.organization_id,NEW.asset_id,NEW.version_id,NEW.target_sha256,NEW.target_bytes) IS DISTINCT FROM ROW(OLD.organization_id,OLD.asset_id,OLD.version_id,OLD.target_sha256,OLD.target_bytes) THEN RAISE EXCEPTION 'immutable review target'; END IF;
 IF NEW.record_count<>OLD.record_count+1 OR NEW.head_record_id IS NOT DISTINCT FROM OLD.head_record_id THEN RAISE EXCEPTION 'invalid review head transition'; END IF;
 RETURN NEW;
END;
$$;
CREATE FUNCTION studio.world_set_review_check_append() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_head uuid; current_count bigint; target_hash text;
BEGIN
 SELECT head_record_id,record_count,target_sha256 INTO STRICT current_head,current_count,target_hash FROM studio.world_set_review_heads WHERE organization_id=NEW.organization_id AND asset_id=NEW.asset_id AND version_id=NEW.version_id FOR UPDATE;
 IF NEW.sequence<>current_count+1 OR NEW.previous_record_id IS DISTINCT FROM current_head OR NEW.target_sha256<>target_hash THEN RAISE EXCEPTION 'review chain compare and swap failed'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER world_set_review_record_chain BEFORE INSERT ON studio.world_set_review_records FOR EACH ROW EXECUTE FUNCTION studio.world_set_review_check_append();
CREATE TRIGGER world_set_review_target_immutable BEFORE UPDATE ON studio.world_set_review_heads FOR EACH ROW EXECUTE FUNCTION studio.world_set_review_keep_target();
CREATE TRIGGER world_set_review_heads_no_delete BEFORE DELETE OR TRUNCATE ON studio.world_set_review_heads FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_set_review_records_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON studio.world_set_review_records FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_set_review_requests_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON studio.world_set_review_requests FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_set_review_cursors_immutable BEFORE UPDATE ON studio.world_set_review_cursors FOR EACH STATEMENT EXECUTE FUNCTION studio.world_reject_immutable_change();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_set_review_heads FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_set_review_records FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_set_review_requests FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
CREATE TRIGGER world_handoff_gate BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON studio.world_set_review_cursors FOR EACH STATEMENT EXECUTE FUNCTION studio.world_check_writer();
ALTER TABLE studio.world_set_review_heads ENABLE ALWAYS TRIGGER world_handoff_gate;
ALTER TABLE studio.world_set_review_records ENABLE ALWAYS TRIGGER world_handoff_gate;
ALTER TABLE studio.world_set_review_requests ENABLE ALWAYS TRIGGER world_handoff_gate;
ALTER TABLE studio.world_set_review_cursors ENABLE ALWAYS TRIGGER world_handoff_gate;
