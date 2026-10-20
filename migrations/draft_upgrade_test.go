//go:build integration

package migrations

import (
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
)

func TestDraftUpgradePreservesLegacyVersionsAndWriterGate(t *testing.T) {
	db := testdb.New(t)
	ctx := t.Context()
	initialSQL, initialDigest, err := schema()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "CREATE SCHEMA studio; CREATE TABLE studio.world_schema_history(version integer PRIMARY KEY,digest text NOT NULL);"+initialSQL+gateSQL+fenceSQL); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		version int
		digest  string
	}{{1, initialDigest}, {2, gateDigest()}, {3, fenceDigest()}} {
		if _, err = db.Exec(ctx, "INSERT INTO studio.world_schema_history VALUES($1,$2)", step.version, step.digest); err != nil {
			t.Fatal(err)
		}
	}
	const id = "01900000-0000-7000-8000-000000000001"
	if _, err = db.Exec(ctx, `UPDATE studio.world_handoff SET state='active';
INSERT INTO studio.world_coord_fences VALUES('project',7),('world',9);`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "INSERT INTO studio.world_assets(asset_id,organization_id,owner_id,asset_type,name) VALUES($1,$1,$1,'prompt','Existing asset')", id); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "INSERT INTO studio.world_commits(organization_id,asset_id,commit_id,created_by,request_sha256,version_id,plan) VALUES($1,$1,$1,$1,repeat('a',64),$1,'{}')", id); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "INSERT INTO studio.world_asset_versions(version_id,organization_id,asset_id,source_project_id,source_revision_id,source_manifest_ref,manifest_ref,manifest_json,created_by,commit_id) VALUES($1,$1,$1,$1,$1,'{}','{}','{\"schema_version\":1}',$1,$1)", id); err != nil {
		t.Fatal(err)
	}
	var before string
	const snapshot = `SELECT jsonb_build_object('history',(SELECT jsonb_agg(to_jsonb(h) ORDER BY version) FROM studio.world_schema_history h WHERE version<=3),'gate',(SELECT to_jsonb(g) FROM studio.world_handoff g),'fences',(SELECT jsonb_agg(to_jsonb(f) ORDER BY coordinator) FROM studio.world_coord_fences f),'asset',(SELECT to_jsonb(a) FROM studio.world_assets a),'version',(SELECT to_jsonb(v)-'source_draft_id' FROM studio.world_asset_versions v))::text`
	if err = db.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if Check(ctx, db) == nil {
		t.Fatal("unmigrated schema accepted")
	}
	for range 2 {
		if err = UpgradeGates(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	if err = Check(ctx, db); err != nil {
		t.Fatal(err)
	}
	var after string
	if err = db.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("legacy state changed", err)
	}
	var count int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM studio.world_schema_history").Scan(&count); err != nil || count != 7 {
		t.Fatal("upgrade history mismatch", err)
	}
	if _, err = db.Exec(ctx, "UPDATE studio.world_asset_versions SET manifest_json='{}'"); err == nil {
		t.Fatal("immutable version was editable")
	}
	if _, err = db.Exec(ctx, "UPDATE studio.world_handoff SET state='closed'"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "INSERT INTO studio.world_drafts(draft_id,organization_id,owner_id,purpose,name,subjects) VALUES($1,$1,$1,'collection','Blocked','[]')", id); err == nil {
		t.Fatal("new draft bypassed closed writer gate")
	}
}
