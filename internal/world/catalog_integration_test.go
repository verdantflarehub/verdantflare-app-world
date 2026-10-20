//go:build integration

package world

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"github.com/verdantflarehub/verdantflare-app-world/migrations"
)

func TestWorldCatalogFiltersAndIsolation(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	if err := migrations.ApplyFresh(ctx, db, 1, 1); err != nil {
		t.Fatal(err)
	}
	svc := &Service{db: db}
	p := Principal{testID(30), testID(31), testID(32)}
	// Only query fixtures; no task completion or publication behavior is inferred.
	seed := func(org, subject, kind, name, relation string, tags []string) string {
		t.Helper()
		id, version, commit := newID(), newID(), newID()
		subjects, _ := json.Marshal(tags)
		if _, err := db.Exec(ctx, `INSERT INTO studio.world_assets(asset_id,organization_id,owner_id,asset_type,name,subjects) VALUES($1,$2,$3,$4,$5,$6)`, id, org, subject, kind, name, subjects); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO studio.world_asset_grants VALUES($1,$2,$3,'owner')`, org, id, subject); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO studio.world_commits(organization_id,asset_id,commit_id,created_by,request_sha256,version_id,plan) VALUES($1,$2,$3,$4,repeat('0',64),$5,'{}')`, org, id, commit, subject, version); err != nil {
			t.Fatal(err)
		}
		manifest, _ := json.Marshal(map[string]any{"source": map[string]string{"relation": relation}})
		if _, err := db.Exec(ctx, `INSERT INTO studio.world_asset_versions(version_id,organization_id,asset_id,source_project_id,source_revision_id,source_manifest_ref,manifest_ref,manifest_json,created_by,commit_id) VALUES($1,$2,$3,$4,$5,'{}','{}',$6,$7,$8)`, version, org, id, newID(), newID(), manifest, subject, commit); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE studio.world_assets SET head_version_id=$1 WHERE asset_id=$2`, version, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := seed(p.OrganizationID, p.SubjectID, "prompt", "布鼠 Cinematic 100%_A", "curated_in", []string{"材质", "Gpt"})
	second := seed(p.OrganizationID, p.SubjectID, "knowledge", "叙事方法", "produced_in", []string{"布鼠", "cinematic"})
	seed(p.OrganizationID, p.SubjectID, "character-image", "布鼠 Cinematic", "produced_in", []string{})
	seed(p.OrganizationID, testID(33), "prompt", "布鼠 Cinematic 100%_A", "curated_in", []string{})
	seed(testID(34), p.SubjectID, "prompt", "布鼠 Cinematic 100%_A", "curated_in", []string{})
	check := func(req WorldListRequest, want ...string) WorldListResult {
		t.Helper()
		r, err := svc.WorldList(ctx, p, req)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, a := range r.Items {
			got[a.AssetID] = true
		}
		if len(got) != len(want) {
			t.Fatalf("unexpected count %d want %d", len(got), len(want))
		}
		for _, id := range want {
			if !got[id] {
				t.Fatal("expected asset missing")
			}
		}
		return r
	}
	check(WorldListRequest{Domain: "knowledge", Query: "布鼠 cinematic"}, first, second)
	check(WorldListRequest{Domain: "knowledge", Relation: "curated_in", Query: "GPT"}, first)
	check(WorldListRequest{Query: "%_"}, first)
	check(WorldListRequest{Domain: "knowledge", AssetType: "prompt"}, first)
	check(WorldListRequest{Domain: "scene"})
	check(WorldListRequest{Query: "布鼠 nonexistent"})
	ids := []string{first, second}
	sort.Strings(ids)
	page := check(WorldListRequest{Domain: "knowledge", Limit: 1}, ids[0])
	if page.NextCursor != ids[0] {
		t.Fatal("missing query cursor")
	}
	check(WorldListRequest{Domain: "knowledge", Limit: 1, Cursor: page.NextCursor}, ids[1])
	if _, err := svc.WorldList(ctx, p, WorldListRequest{Domain: "unknown"}); err != ErrInvalid {
		t.Fatal("unknown domain accepted")
	}
	if _, err := svc.WorldList(ctx, p, WorldListRequest{Relation: "manual"}); err != ErrInvalid {
		t.Fatal("unknown relation accepted")
	}
}
