package migrations

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"regexp"
	"strings"
)

//go:embed 0002_handoff.sql
var gateSQL string

//go:embed 0003_locked_fence.sql
var fenceSQL string

//go:embed 0004_world_drafts.sql
var draftSQL string

//go:embed 0005_draft_asset_sources.sql
var draftAssetSQL string

func draftAssetDigest() string {
	s := sha256.Sum256([]byte(draftAssetSQL))
	return hex.EncodeToString(s[:])
}

func draftDigest() string { s := sha256.Sum256([]byte(draftSQL)); return hex.EncodeToString(s[:]) }

const gateDomain = "world"
const gateHistory = "studio.world_schema_history"
const gateHashColumn = "digest"

func gateDigest() string  { s := sha256.Sum256([]byte(gateSQL)); return hex.EncodeToString(s[:]) }
func fenceDigest() string { s := sha256.Sum256([]byte(fenceSQL)); return hex.EncodeToString(s[:]) }

type gateQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func applyGate(ctx context.Context, tx pgx.Tx) error {
	for _, step := range []struct {
		version     int
		sql, digest string
	}{{2, gateSQL, gateDigest()}, {3, fenceSQL, fenceDigest()}, {4, draftSQL, draftDigest()}, {5, draftAssetSQL, draftAssetDigest()}} {
		var hash string
		err := tx.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=$1", step.version).Scan(&hash)
		if err == nil {
			if hash != step.digest {
				return errors.New("handoff migration checksum mismatch")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(ctx, step.sql); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO "+gateHistory+"(version,"+gateHashColumn+") VALUES($1,$2)", step.version, step.digest); err != nil {
			return err
		}
	}
	return CheckGate(ctx, tx)
}

// UpgradeGates is explicit and accepts only this app's verified initial schema
// and existing v2 gate. It neither initializes a legacy database nor opens gates.
func UpgradeGates(ctx context.Context, db *pgxpool.Pool) error {
	_, expected, err := schema()
	if err != nil {
		return err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var actual string
	var count int
	if err = tx.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=1").Scan(&actual); err != nil {
		return err
	}
	if actual != expected {
		return errors.New("initial migration checksum mismatch")
	}
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM "+gateHistory).Scan(&count); err != nil {
		return err
	}
	if count != 2 && count != 3 && count != 4 && count != 5 {
		return errors.New("unsupported gate upgrade source")
	}
	var owner bool
	if err = tx.QueryRow(ctx, "SELECT session_user=migration_owner FROM studio."+gateDomain+"_handoff WHERE singleton FOR UPDATE").Scan(&owner); err != nil {
		return err
	}
	if !owner {
		return errors.New("gate upgrade requires migration owner")
	}
	if err = applyGate(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CheckGate checks actual triggers, not only recorded SQL checksums.
func CheckGate(ctx context.Context, q gateQuery) error {
	var hash string
	var count int
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=2").Scan(&hash); e != nil {
		return e
	}
	if e := q.QueryRow(ctx, "SELECT count(*) FROM "+gateHistory).Scan(&count); e != nil {
		return e
	}
	if hash != gateDigest() || count != 5 {
		return errors.New("handoff migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=3").Scan(&hash); e != nil {
		return e
	}
	if hash != fenceDigest() {
		return errors.New("locked fence migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=4").Scan(&hash); e != nil {
		return e
	}
	if hash != draftDigest() {
		return errors.New("draft migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=5").Scan(&hash); e != nil {
		return e
	}
	if hash != draftAssetDigest() {
		return errors.New("draft asset migration mismatch")
	}
	_, body, ok := strings.Cut(gateSQL, "SET search_path=pg_catalog AS $$")
	if !ok {
		return errors.New("invalid gate source")
	}
	body, _, ok = strings.Cut(body, "$$;")
	if !ok {
		return errors.New("invalid gate source")
	}
	var valid bool
	if e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_proc WHERE oid=$1::regprocedure AND prosrc=$2 AND prosecdef AND proconfig=ARRAY['search_path=pg_catalog'])`, "studio."+gateDomain+"_check_writer()", body).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return errors.New("writer guard changed")
	}
	_, fenceBody, ok := strings.Cut(fenceSQL, "_locked_fence(coordinator_name text) RETURNS SETOF bigint LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog AS $$")
	if !ok {
		return errors.New("invalid fence reader source")
	}
	fenceBody, _, ok = strings.Cut(fenceBody, "$$;")
	if !ok {
		return errors.New("invalid fence reader source")
	}
	if e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=$1::regprocedure AND prosrc=$2 AND prosecdef AND provolatile='v' AND proretset AND prorettype='bigint'::regtype AND l.lanname='sql' AND proconfig=ARRAY['search_path=pg_catalog'] AND NOT EXISTS(SELECT 1 FROM aclexplode(COALESCE(proacl,acldefault('f',proowner))) WHERE grantee=0 AND privilege_type='EXECUTE'))`, "studio."+gateDomain+"_locked_fence(text)", fenceBody).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return errors.New("locked fence reader changed")
	}
	_, readyBody, ok := strings.Cut(fenceSQL, "_runtime_ready() RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$")
	if !ok {
		return errors.New("invalid readiness source")
	}
	readyBody, _, ok = strings.Cut(readyBody, "$$;")
	if !ok {
		return errors.New("invalid readiness source")
	}
	if e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_proc WHERE oid=$1::regprocedure AND prosrc=$2 AND prosecdef AND provolatile='s' AND proconfig=ARRAY['search_path=pg_catalog'])`, "studio."+gateDomain+"_runtime_ready()", readyBody).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return errors.New("runtime readiness guard changed")
	}
	for _, match := range regexp.MustCompile(`ON studio\.([a-z_]+) FOR EACH STATEMENT`).FindAllStringSubmatch(gateSQL+"\n"+draftSQL, -1) {
		if e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=$1::regclass AND tgname=$2 AND tgenabled='A' AND NOT tgisinternal AND tgtype=62 AND tgnargs=0 AND tgqual IS NULL AND tgfoid=$3::regprocedure)`, "studio."+match[1], gateDomain+"_handoff_gate", "studio."+gateDomain+"_check_writer()").Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return errors.New("writer guard disabled")
		}
	}
	return nil
}
func ActivateFresh(ctx context.Context, db *pgxpool.Pool) error {
	if e := Check(ctx, db); e != nil {
		return e
	}
	tx, e := db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	var mode string
	var owner, unused bool
	if e = tx.QueryRow(ctx, "SELECT state,session_user=migration_owner,handoff_id IS NULL FROM studio."+gateDomain+"_handoff WHERE singleton FOR UPDATE").Scan(&mode, &owner, &unused); e != nil {
		return e
	}
	if !owner || !unused || (mode != "closed" && mode != "active") {
		return errors.New("fresh activation denied")
	}
	for _, match := range regexp.MustCompile(`ON studio\.([a-z_]+) FOR EACH STATEMENT`).FindAllStringSubmatch(gateSQL+"\n"+draftSQL, -1) {
		var occupied bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM studio."+match[1]+")").Scan(&occupied); e != nil {
			return e
		}
		if occupied {
			return errors.New("fresh activation requires empty target")
		}
	}
	if _, e = tx.Exec(ctx, "UPDATE studio."+gateDomain+"_handoff SET state='active' WHERE singleton"); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func CheckRuntime(ctx context.Context, db *pgxpool.Pool) error {
	if e := Check(ctx, db); e != nil {
		return e
	}
	var ready bool
	if e := db.QueryRow(ctx, "SELECT COALESCE(studio."+gateDomain+"_runtime_ready(),false)").Scan(&ready); e != nil {
		return e
	}
	if !ready {
		return errors.New("writer closed or runtime database role privileged")
	}
	return nil
}
