//go:build integration

package migrations

import (
	"testing"

	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
)

func TestAssociationUpgradePreservesObservationAndScheduleState(t *testing.T) {
	db := testdb.New(t)
	ctx := t.Context()
	base, digest, err := schema()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "CREATE SCHEMA studio; CREATE TABLE studio.world_schema_history(version integer PRIMARY KEY,digest text NOT NULL);"+base+gateSQL+fenceSQL+draftSQL+draftAssetSQL+analysisSQL); err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct {
		version int
		digest  string
	}{{1, digest}, {2, gateDigest()}, {3, fenceDigest()}, {4, draftDigest()}, {5, draftAssetDigest()}, {6, analysisDigest()}} {
		if _, err = db.Exec(ctx, "INSERT INTO studio.world_schema_history VALUES($1,$2)", s.version, s.digest); err != nil {
			t.Fatal(err)
		}
	}
	const id = "01900000-0000-7000-8000-000000000001"
	// Minimal relational fixtures, deliberately not a media or model result.
	for _, sql := range []string{
		`UPDATE studio.world_handoff SET state='active'`,
		`INSERT INTO studio.world_drafts(draft_id,organization_id,owner_id,purpose,name,subjects) VALUES($1,$1,$1,'collection','Synthetic upgrade fixture','[]')`,
		`INSERT INTO studio.world_draft_commits(organization_id,created_by,commit_id,draft_id,request_sha256,revision_id,plan) VALUES($1,$1,$1,$1,repeat('a',64),$1,'{}')`,
		`INSERT INTO studio.world_draft_revisions(revision_id,organization_id,draft_id,manifest_ref,manifest_json,created_by,commit_id) VALUES($1,$1,$1,'{}','{}',$1,$1)`,
		`INSERT INTO studio.world_analysis_tasks(organization_id,owner_id,task_id,draft_id,revision_id,file_id,request_sha256,plan) VALUES($1,$1,$1,$1,$1,$1,repeat('b',64),'{"synthetic_fixture":true}')`,
		`INSERT INTO studio.world_analysis_steps(organization_id,owner_id,task_id,step_key,request_sha256,request) VALUES($1,$1,$1,'visual-0-1',repeat('c',64),'{"synthetic_fixture":true}')`,
		`UPDATE studio.world_handoff SET state='closed'`,
	} {
		// Parameter count follows the individual fixture statement.
		if sql[0:6] == "UPDATE" {
			_, err = db.Exec(ctx, sql)
		} else {
			_, err = db.Exec(ctx, sql, id)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(ctx, scheduleSQL); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "INSERT INTO studio.world_schema_history VALUES(7,$1)", scheduleDigest()); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_object('tasks',(SELECT jsonb_agg(to_jsonb(t)-'save_learning'-'prompt_targets'-'include_typed_results') FROM studio.world_analysis_tasks t),'steps',(SELECT jsonb_agg(to_jsonb(s)) FROM studio.world_analysis_steps s),'schedule',(SELECT jsonb_agg(to_jsonb(q)) FROM studio.world_analysis_schedule q),'gate',(SELECT to_jsonb(g) FROM studio.world_handoff g),'history',(SELECT jsonb_agg(to_jsonb(h) ORDER BY version) FROM studio.world_schema_history h WHERE version<=7))::text`
	var before, after string
	if err = db.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = UpgradeGates(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("upgrade changed original state", err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM studio.world_analysis_associations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("migration created an unsolicited association", err)
	}
	if _, err = db.Exec(ctx, `TRUNCATE studio.world_analysis_associations`); err == nil {
		t.Fatal("new table bypassed closed gate")
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_handoff SET state='active'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO studio.world_analysis_associations(organization_id,owner_id,task_id,request_sha256,request) VALUES($1,$1,$1,repeat('d',64),'{}')`, id); err == nil {
		t.Fatal("unready observations admitted association")
	}
	if err = Check(ctx, db); err != nil {
		t.Fatal(err)
	}
}
