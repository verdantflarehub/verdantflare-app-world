//go:build integration

package migrations

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
	"github.com/verdantflarehub/verdantflare-app-world/internal/testdb"
)

func TestFreshWriterGateAndUnprivilegedRuntime(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	if err := ApplyFresh(ctx, db, 11, 12); err != nil {
		t.Fatal("fresh migration failed")
	}
	if CheckRuntime(ctx, db) == nil {
		t.Fatal("closed privileged runtime accepted")
	}
	role := "pw_gate_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err := db.Exec(ctx, "CREATE ROLE "+quoted+" NOLOGIN NOINHERIT"); err != nil {
		t.Fatal("cannot create isolated test role")
	}
	var runtimeDB *pgxpool.Pool
	t.Cleanup(func() {
		if runtimeDB != nil {
			runtimeDB.Close()
		}
		if _, err := db.Exec(context.Background(), "DROP OWNED BY "+quoted+"; DROP ROLE "+quoted); err != nil {
			t.Error("cannot clean isolated test role")
		}
	})
	grant := "GRANT USAGE ON SCHEMA studio TO " + quoted + "; GRANT SELECT ON ALL TABLES IN SCHEMA studio TO " + quoted + "; GRANT EXECUTE ON FUNCTION studio." + gateDomain + "_locked_fence(text) TO " + quoted + "; GRANT INSERT,UPDATE,DELETE ON studio." + gateDomain + "_coord_operations TO " + quoted
	if _, err := db.Exec(ctx, grant); err != nil {
		t.Fatal("cannot grant runtime reads")
	}
	cfg := db.Config()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET SESSION AUTHORIZATION "+quoted)
		return err
	}
	var err error
	runtimeDB, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("cannot create isolated runtime connection")
	}
	if CheckRuntime(ctx, runtimeDB) == nil {
		t.Fatal("closed writer accepted runtime")
	}
	if err = ActivateFresh(ctx, db); err != nil {
		t.Fatal("fresh activation failed")
	}
	if CheckRuntime(ctx, db) == nil {
		t.Fatal("migration owner accepted as runtime")
	}
	if err = CheckRuntime(ctx, runtimeDB); err != nil {
		t.Fatal("least privilege runtime rejected")
	}
	for _, privilege := range []string{
		"CREATE ON SCHEMA studio",
		"UPDATE ON studio." + gateDomain + "_handoff",
		"UPDATE ON studio." + gateDomain + "_coord_fences",
		"UPDATE ON " + gateHistory,
	} {
		if _, err = db.Exec(ctx, "GRANT "+privilege+" TO "+quoted); err != nil {
			t.Fatal("cannot set isolated excess privilege")
		}
		if CheckRuntime(ctx, runtimeDB) == nil {
			t.Fatal("excess runtime privilege accepted")
		}
		if _, err = db.Exec(ctx, "REVOKE "+privilege+" FROM "+quoted); err != nil {
			t.Fatal("cannot restore isolated role")
		}
	}
	if err = CheckRuntime(ctx, runtimeDB); err != nil {
		t.Fatal("restored least privilege role rejected")
	}
	// A real coordination operation uses the restricted runtime role. The row
	// lock must not require granting that role UPDATE on the external fence.
	if _, err = runtimeDB.Exec(ctx, "SELECT fence FROM studio."+gateDomain+"_coord_fences FOR SHARE"); err == nil {
		t.Fatal("runtime unexpectedly has direct fence row-lock permission")
	}
	if _, err = runtimeDB.Exec(ctx, "UPDATE studio."+gateDomain+"_coord_fences SET fence=fence+1"); err == nil {
		t.Fatal("runtime advanced the external fence")
	}
	if _, err = db.Exec(ctx, "REVOKE EXECUTE ON FUNCTION studio."+gateDomain+"_locked_fence(text) FROM "+quoted); err != nil {
		t.Fatal("cannot remove runtime reader grant")
	}
	if CheckRuntime(ctx, runtimeDB) == nil {
		t.Fatal("runtime without fence reader grant accepted")
	}
	if _, err = db.Exec(ctx, "GRANT EXECUTE ON FUNCTION studio."+gateDomain+"_locked_fence(text) TO "+quoted); err != nil {
		t.Fatal("cannot restore runtime reader grant")
	}
	fence := int64(11)
	if gateDomain == "world" {
		fence = 12
	}
	store, err := coordination.NewStore(runtimeDB, gateDomain, fence, func(context.Context, pgx.Tx, coordination.Operation, coordination.Grant) error { return nil })
	if err != nil {
		t.Fatal("cannot initialize runtime coordinator")
	}
	id := func() string {
		value, err := uuid.NewV7()
		if err != nil {
			t.Fatal("cannot create fixture ID")
		}
		return value.String()
	}
	op := coordination.Operation{Coordinator: gateDomain, OrganizationID: id(), SubjectID: id(), ResourceID: id(), CommitID: id(), RequestHash: strings.Repeat("a", 64), Fence: fence}
	plan := coordination.Plan{gateDomain: {{ResourceID: op.ResourceID, Write: true}}}
	for attempt := 0; attempt < 2; attempt++ {
		if err = store.Register(ctx, op, plan); err != nil {
			t.Fatal("least privilege coordinator registration/retry failed")
		}
	}
	readTx, err := runtimeDB.Begin(ctx)
	if err != nil {
		t.Fatal("cannot start runtime fence read")
	}
	defer readTx.Rollback(context.Background())
	var observed int64
	if err = readTx.QueryRow(ctx, "SELECT * FROM studio."+gateDomain+"_locked_fence($1)", gateDomain).Scan(&observed); err != nil || observed != fence {
		t.Fatal("restricted fence reader failed")
	}
	adminTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal("cannot start test fence maintenance")
	}
	defer adminTx.Rollback(context.Background())
	if _, err = adminTx.Exec(ctx, "SET LOCAL lock_timeout='100ms'"); err != nil {
		t.Fatal("cannot bound fixture lock wait")
	}
	_, err = adminTx.Exec(ctx, "UPDATE studio."+gateDomain+"_coord_fences SET fence=fence WHERE coordinator=$1", gateDomain)
	var locked *pgconn.PgError
	if !errors.As(err, &locked) || locked.Code != "55P03" {
		t.Fatal("restricted reader failed to hold the fence row lock")
	}
	_ = adminTx.Rollback(ctx)
	if err = readTx.Rollback(ctx); err != nil {
		t.Fatal("cannot release fixture fence read")
	}
	// Coordination writes participate in the same gate as business writes.
	if _, err = db.Exec(ctx, "INSERT INTO studio."+gateDomain+"_coord_outbox(organization_id,resource_id,commit_id,participant) VALUES($1,$2,$3,'world')", uuid.NewString(), uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal("active gate rejected fixture write")
	}
	if ActivateFresh(ctx, db) == nil {
		t.Fatal("occupied fresh target reactivated")
	}
	if _, err = db.Exec(ctx, "UPDATE studio."+gateDomain+"_handoff SET state='closed'"); err != nil {
		t.Fatal("cannot close test gate")
	}
	if _, err = db.Exec(ctx, "UPDATE studio."+gateDomain+"_coord_outbox SET delivered=true"); err == nil {
		t.Fatal("closed gate allowed late coordinator write")
	}
	if _, err = db.Exec(ctx, "ALTER TABLE studio."+gateDomain+"_coord_outbox DISABLE TRIGGER "+gateDomain+"_handoff_gate"); err != nil {
		t.Fatal("cannot disable test trigger")
	}
	if CheckGate(ctx, db) == nil {
		t.Fatal("disabled gate accepted")
	}
}

func TestHandoffGateUpgradePreservesVersionTwo(t *testing.T) {
	for _, mode := range []string{"closed", "active"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			db := testdb.New(t)
			initial, digest, err := schema()
			if err != nil {
				t.Fatal("cannot load original initial schema")
			}
			if _, err = db.Exec(ctx, "CREATE SCHEMA studio; CREATE TABLE "+gateHistory+"(version integer PRIMARY KEY,"+gateHashColumn+" text NOT NULL);"+initial+gateSQL); err != nil {
				t.Fatal("cannot initialize actual version-two fixture")
			}
			if _, err = db.Exec(ctx, "INSERT INTO "+gateHistory+" VALUES(1,$1),(2,$2)", digest, gateDigest()); err != nil {
				t.Fatal("cannot record original version-two history")
			}
			if _, err = db.Exec(ctx, "INSERT INTO studio."+gateDomain+"_coord_fences VALUES('project',11),('world',12)"); err != nil {
				t.Fatal("cannot initialize fixture external fences")
			}
			if _, err = db.Exec(ctx, "UPDATE studio."+gateDomain+"_handoff SET state=$1", mode); err != nil {
				t.Fatal("cannot set fixture gate state")
			}
			if Check(ctx, db) == nil {
				t.Fatal("version two falsely accepted as current schema")
			}
			for attempt := 0; attempt < 2; attempt++ {
				if err = UpgradeGates(ctx, db); err != nil {
					t.Fatal("explicit gate upgrade/retry failed")
				}
			}
			if err = Check(ctx, db); err != nil {
				t.Fatal("upgraded schema rejected")
			}
			var actual, oldHash string
			if err = db.QueryRow(ctx, "SELECT state FROM studio."+gateDomain+"_handoff WHERE singleton").Scan(&actual); err != nil || actual != mode {
				t.Fatal("upgrade changed writer state")
			}
			if err = db.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=2").Scan(&oldHash); err != nil || oldHash != gateDigest() {
				t.Fatal("upgrade rewrote version-two digest")
			}
			var fences int64
			if err = db.QueryRow(ctx, "SELECT sum(fence) FROM studio."+gateDomain+"_coord_fences").Scan(&fences); err != nil || fences != 23 {
				t.Fatal("upgrade changed recovery fences")
			}
		})
	}
}
