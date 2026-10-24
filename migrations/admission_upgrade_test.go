//go:build integration

package migrations

import (
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"testing"
)

func TestAdmissionUpgradePreservesLearningAndLegacyRequests(t *testing.T) {
	ctx := t.Context()
	db := testdb.New(t)
	base, digest, err := schema()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "CREATE SCHEMA studio; CREATE TABLE studio.world_schema_history(version integer PRIMARY KEY,digest text NOT NULL);"+base+gateSQL+fenceSQL+draftSQL+draftAssetSQL+analysisSQL+scheduleSQL+associationSQL+promptsSQL+resultsSQL+learningSQL); err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct {
		v int
		d string
	}{{1, digest}, {2, gateDigest()}, {3, fenceDigest()}, {4, draftDigest()}, {5, draftAssetDigest()}, {6, analysisDigest()}, {7, scheduleDigest()}, {8, associationDigest()}, {9, promptsDigest()}, {10, resultsDigest()}, {11, learningDigest()}} {
		if _, err = db.Exec(ctx, "INSERT INTO studio.world_schema_history VALUES($1,$2)", s.v, s.d); err != nil {
			t.Fatal(err)
		}
	}
	const id = "01900000-0000-7000-8000-000000000001"
	for _, sql := range []string{
		`UPDATE studio.world_handoff SET state='active'`,
		`INSERT INTO studio.world_drafts(draft_id,organization_id,owner_id,purpose,name,subjects) VALUES($1,$1,$1,'collection','Synthetic admission upgrade','[]')`,
		`INSERT INTO studio.world_draft_commits(organization_id,created_by,commit_id,draft_id,request_sha256,revision_id,plan) VALUES($1,$1,$1,$1,repeat('a',64),$1,'{}')`,
		`INSERT INTO studio.world_draft_revisions(revision_id,organization_id,draft_id,manifest_ref,manifest_json,created_by,commit_id) VALUES($1,$1,$1,'{}','{}',$1,$1)`,
		`INSERT INTO studio.world_analysis_tasks(organization_id,owner_id,task_id,draft_id,revision_id,file_id,request_sha256,plan,include_typed_results,save_learning) VALUES($1,$1,$1,$1,$1,$1,repeat('b',64),'{"steps":[{"key":"visual-0-1"}]}',true,true)`,
		`INSERT INTO studio.world_analysis_steps(organization_id,owner_id,task_id,step_key,request_sha256,request) VALUES($1,$1,$1,'visual-0-1',repeat('c',64),'{"budget_limit":"0.1","synthetic_fixture":true}')`,
		`INSERT INTO studio.world_analysis_schedule(organization_id,owner_id,task_id) VALUES($1,$1,$1)`,
		`UPDATE studio.world_analysis_steps SET state='dispatching',revision=2`,
		`UPDATE studio.world_analysis_steps SET state='running',revision=3,call_id='01900000-0000-7000-8000-000000000002'`,
		`INSERT INTO studio.world_analysis_learning(organization_id,owner_id,task_id,assembly_version,document_sha256,create_id,write_id,commit_id,file_id) VALUES($1,$1,$1,'1',repeat('d',64),$1,$1,$1,$1)`,
		`UPDATE studio.world_analysis_learning SET state='saved',result='{"synthetic_fixed_receipt":true}'`,
		`UPDATE studio.world_handoff SET state='closed'`,
	} {
		if sql[:6] == "UPDATE" {
			_, err = db.Exec(ctx, sql)
		} else {
			_, err = db.Exec(ctx, sql, id)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	const snapshot = `SELECT jsonb_build_object('tasks',(SELECT jsonb_agg(to_jsonb(t)-'pipeline_version'-'total_budget') FROM studio.world_analysis_tasks t),'steps',(SELECT jsonb_agg(to_jsonb(s)) FROM studio.world_analysis_steps s),'learning',(SELECT jsonb_agg(to_jsonb(l)) FROM studio.world_analysis_learning l),'schedule',(SELECT jsonb_agg(to_jsonb(q)) FROM studio.world_analysis_schedule q),'gate',(SELECT to_jsonb(g) FROM studio.world_handoff g),'history',(SELECT jsonb_agg(to_jsonb(h) ORDER BY version) FROM studio.world_schema_history h WHERE version<=11))::text`
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
		t.Fatal("migration changed preserved task/save state", err)
	}
	var n int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM studio.world_analysis_requests`).Scan(&n); err != nil || n != 0 {
		t.Fatal("upgrade created unsolicited analysis", err)
	}
	var cap string
	if err = db.QueryRow(ctx, `SELECT total_budget FROM studio.world_analysis_tasks LIMIT 1`).Scan(&cap); err != nil || cap != "" {
		t.Fatal("legacy budget changed", err)
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_handoff SET state='active'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_analysis_steps SET state='failed',revision=4,error_code='SYNTHETIC'`); err != nil {
		t.Fatal("legacy inflight call cannot recover", err)
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_analysis_tasks SET total_budget='1'`); err == nil {
		t.Fatal("original task budget mutable")
	}
	if err = Check(ctx, db); err != nil {
		t.Fatal(err)
	}
}
