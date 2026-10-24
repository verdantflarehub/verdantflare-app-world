//go:build integration

package migrations

import (
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
	"testing"
)

func TestLearningUpgradePreservesTasksAndRunningResults(t *testing.T) {
	db := testdb.New(t)
	ctx := t.Context()
	base, digest, err := schema()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "CREATE SCHEMA studio; CREATE TABLE studio.world_schema_history(version integer PRIMARY KEY,digest text NOT NULL);"+base+gateSQL+fenceSQL+draftSQL+draftAssetSQL+analysisSQL+scheduleSQL+associationSQL+promptsSQL+resultsSQL); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		version int
		digest  string
	}{{1, digest}, {2, gateDigest()}, {3, fenceDigest()}, {4, draftDigest()}, {5, draftAssetDigest()}, {6, analysisDigest()}, {7, scheduleDigest()}, {8, associationDigest()}, {9, promptsDigest()}, {10, resultsDigest()}} {
		if _, err = db.Exec(ctx, "INSERT INTO studio.world_schema_history VALUES($1,$2)", step.version, step.digest); err != nil {
			t.Fatal(err)
		}
	}
	const id = "01900000-0000-7000-8000-000000000001"
	// Relational migration fixture only. Native content is intentionally synthetic.
	for _, sql := range []string{
		`UPDATE studio.world_handoff SET state='active'`,
		`INSERT INTO studio.world_drafts(draft_id,organization_id,owner_id,purpose,name,subjects) VALUES($1,$1,$1,'collection','Synthetic result upgrade','[]')`,
		`INSERT INTO studio.world_draft_commits(organization_id,created_by,commit_id,draft_id,request_sha256,revision_id,plan) VALUES($1,$1,$1,$1,repeat('a',64),$1,'{}')`,
		`INSERT INTO studio.world_draft_revisions(revision_id,organization_id,draft_id,manifest_ref,manifest_json,created_by,commit_id) VALUES($1,$1,$1,'{}','{}',$1,$1)`,
		`INSERT INTO studio.world_analysis_tasks(organization_id,owner_id,task_id,draft_id,revision_id,file_id,request_sha256,plan,prompt_targets,include_typed_results) VALUES($1,$1,$1,$1,$1,$1,repeat('b',64),'{"steps":[{"key":"visual-0-1"}],"synthetic_fixture":true}','[{"key":"synthetic"}]',true)`,
		`INSERT INTO studio.world_analysis_steps(organization_id,owner_id,task_id,step_key,request_sha256,request) VALUES($1,$1,$1,'visual-0-1',repeat('c',64),'{"synthetic_fixture":true}')`,
		`INSERT INTO studio.world_analysis_schedule(organization_id,owner_id,task_id) VALUES($1,$1,$1)`,
		`UPDATE studio.world_analysis_steps SET state='dispatching',revision=2`,
		`UPDATE studio.world_analysis_steps SET state='observation_ready',revision=3,call_id='01900000-0000-7000-8000-000000000002',native_result='{}',report='{"passed":true,"issues":[]}'`,
		`INSERT INTO studio.world_analysis_associations(organization_id,owner_id,task_id,request_sha256,request) VALUES($1,$1,$1,repeat('e',64),'{"synthetic_fixture":true}')`,
		`UPDATE studio.world_analysis_associations SET state='dispatching',revision=2`,
		`UPDATE studio.world_analysis_associations SET state='association_ready',revision=3,call_id='01900000-0000-7000-8000-000000000003',native_result='{}'`,
		`INSERT INTO studio.world_analysis_prompt_batches(organization_id,owner_id,task_id,batch_key,batch_index,request_sha256,request) VALUES($1,$1,$1,'prompts-v1-0000',0,repeat('f',64),jsonb_build_object('task_id',$1::uuid::text,'batch',jsonb_build_object('batch_key','prompts-v1-0000','targets','[{"key":"synthetic"}]'::jsonb)))`,
		`UPDATE studio.world_analysis_prompt_batches SET state='dispatching',revision=2`,
		`UPDATE studio.world_analysis_prompt_batches SET state='prompt_ready',revision=3,call_id='01900000-0000-7000-8000-000000000004',native_result='{}'`,
		`INSERT INTO studio.world_analysis_result_batches(organization_id,owner_id,task_id,batch_key,batch_index,request_sha256,request) VALUES($1,$1,$1,'results-v1-shot-0000',0,repeat('a',64),jsonb_build_object('task_id',$1::uuid::text,'batch',jsonb_build_object('batch_key','results-v1-shot-0000')))`,
		`UPDATE studio.world_analysis_result_batches SET state='dispatching',revision=2`,
		`UPDATE studio.world_analysis_result_batches SET state='running',revision=3,call_id='01900000-0000-7000-8000-000000000005'`,
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
	const snapshot = `SELECT jsonb_build_object('tasks',(SELECT jsonb_agg(to_jsonb(t)-'pipeline_version'-'total_budget'-'save_learning') FROM studio.world_analysis_tasks t),'steps',(SELECT jsonb_agg(to_jsonb(s)) FROM studio.world_analysis_steps s),'associations',(SELECT jsonb_agg(to_jsonb(a)) FROM studio.world_analysis_associations a),'prompts',(SELECT jsonb_agg(to_jsonb(p)) FROM studio.world_analysis_prompt_batches p),'results',(SELECT jsonb_agg(to_jsonb(r)) FROM studio.world_analysis_result_batches r),'schedule',(SELECT jsonb_agg(to_jsonb(q)) FROM studio.world_analysis_schedule q),'gate',(SELECT to_jsonb(g) FROM studio.world_handoff g),'history',(SELECT jsonb_agg(to_jsonb(h) ORDER BY version) FROM studio.world_schema_history h WHERE version<=10))::text`
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
	if err = db.QueryRow(ctx, `SELECT count(*) FROM studio.world_analysis_tasks WHERE save_learning=false AND include_typed_results=true`).Scan(&count); err != nil || count != 1 {
		t.Fatal("legacy task gained work", err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM studio.world_analysis_learning`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unsolicited learning saves", err)
	}
	if _, err = db.Exec(ctx, `TRUNCATE studio.world_analysis_learning`); err == nil {
		t.Fatal("writer gate bypassed")
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_handoff SET state='active'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE studio.world_analysis_tasks SET save_learning=true`); err == nil {
		t.Fatal("legacy task mutated")
	}
	if _, err = db.Exec(ctx, `INSERT INTO studio.world_analysis_learning(organization_id,owner_id,task_id,assembly_version,document_sha256,create_id,write_id,commit_id,file_id) VALUES($1,$1,$1,'1',repeat('a',64),$1,$1,$1,$1)`, id); err == nil {
		t.Fatal("legacy task accepted unsolicited save")
	}

	if err = Check(ctx, db); err != nil {
		t.Fatal(err)
	}
}
