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

//go:embed 0006_analysis_steps.sql
var analysisSQL string

//go:embed 0007_analysis_schedule.sql
var scheduleSQL string

//go:embed 0008_analysis_associations.sql
var associationSQL string

//go:embed 0009_analysis_prompts.sql
var promptsSQL string

//go:embed 0010_analysis_results.sql
var resultsSQL string

//go:embed 0011_analysis_learning.sql
var learningSQL string

//go:embed 0012_analysis_admission.sql
var admissionSQL string

//go:embed 0013_analysis_budget_status.sql
var budgetStatusSQL string

//go:embed 0014_set_review.sql
var setReviewSQL string

//go:embed 0015_collection_lifecycle.sql
var lifecycleSQL string

//go:embed 0016_analysis_quality.sql
var qualitySQL string

//go:embed 0017_analysis_observation_retry.sql
var retrySQL string

//go:embed 0018_analysis_association_retry.sql
var associationRetrySQL string

//go:embed 0019_analysis_reference_encoding.sql
var referenceEncodingSQL string

//go:embed 0020_analysis_final_coverage.sql
var finalCoverageSQL string

//go:embed 0026_analysis_prompt_diagnostic.sql
var promptDiagnosticSQL string

func promptDiagnosticDigest() string {
	s := sha256.Sum256([]byte(promptDiagnosticSQL))
	return hex.EncodeToString(s[:])
}

//go:embed 0025_analysis_method_diagnostic.sql
var methodDiagnosticSQL string

func methodDiagnosticDigest() string {
	s := sha256.Sum256([]byte(methodDiagnosticSQL))
	return hex.EncodeToString(s[:])
}

//go:embed 0024_analysis_result_revalidation.sql
var resultRevalidationSQL string

func resultRevalidationDigest() string {
	s := sha256.Sum256([]byte(resultRevalidationSQL))
	return hex.EncodeToString(s[:])
}

//go:embed 0023_analysis_style_correction.sql
var styleCorrectionSQL string

func styleCorrectionDigest() string {
	s := sha256.Sum256([]byte(styleCorrectionSQL))
	return hex.EncodeToString(s[:])
}

//go:embed 0022_analysis_batch_retry.sql
var batchRetrySQL string

func batchRetryDigest() string {
	s := sha256.Sum256([]byte(batchRetrySQL))
	return hex.EncodeToString(s[:])
}

//go:embed 0021_analysis_revalidation.sql
var revalidationSQL string

func revalidationDigest() string {
	s := sha256.Sum256([]byte(revalidationSQL))
	return hex.EncodeToString(s[:])
}

func finalCoverageDigest() string {
	s := sha256.Sum256([]byte(finalCoverageSQL))
	return hex.EncodeToString(s[:])
}

func referenceEncodingDigest() string {
	s := sha256.Sum256([]byte(referenceEncodingSQL))
	return hex.EncodeToString(s[:])
}

func associationRetryDigest() string {
	s := sha256.Sum256([]byte(associationRetrySQL))
	return hex.EncodeToString(s[:])
}

func retryDigest() string { s := sha256.Sum256([]byte(retrySQL)); return hex.EncodeToString(s[:]) }

func qualityDigest() string { s := sha256.Sum256([]byte(qualitySQL)); return hex.EncodeToString(s[:]) }

func lifecycleDigest() string {
	s := sha256.Sum256([]byte(lifecycleSQL))
	return hex.EncodeToString(s[:])
}

func setReviewDigest() string {
	s := sha256.Sum256([]byte(setReviewSQL))
	return hex.EncodeToString(s[:])
}

func budgetStatusDigest() string {
	s := sha256.Sum256([]byte(budgetStatusSQL))
	return hex.EncodeToString(s[:])
}

func admissionDigest() string {
	s := sha256.Sum256([]byte(admissionSQL))
	return hex.EncodeToString(s[:])
}

func learningDigest() string {
	s := sha256.Sum256([]byte(learningSQL))
	return hex.EncodeToString(s[:])
}

func resultsDigest() string { s := sha256.Sum256([]byte(resultsSQL)); return hex.EncodeToString(s[:]) }

func promptsDigest() string { s := sha256.Sum256([]byte(promptsSQL)); return hex.EncodeToString(s[:]) }

func associationDigest() string {
	s := sha256.Sum256([]byte(associationSQL))
	return hex.EncodeToString(s[:])
}

func scheduleDigest() string {
	s := sha256.Sum256([]byte(scheduleSQL))
	return hex.EncodeToString(s[:])
}

func analysisDigest() string {
	s := sha256.Sum256([]byte(analysisSQL))
	return hex.EncodeToString(s[:])
}

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
	}{{2, gateSQL, gateDigest()}, {3, fenceSQL, fenceDigest()}, {4, draftSQL, draftDigest()}, {5, draftAssetSQL, draftAssetDigest()}, {6, analysisSQL, analysisDigest()}, {7, scheduleSQL, scheduleDigest()}, {8, associationSQL, associationDigest()}, {9, promptsSQL, promptsDigest()}, {10, resultsSQL, resultsDigest()}, {11, learningSQL, learningDigest()}, {12, admissionSQL, admissionDigest()}, {13, budgetStatusSQL, budgetStatusDigest()}, {14, setReviewSQL, setReviewDigest()}, {15, lifecycleSQL, lifecycleDigest()}, {16, qualitySQL, qualityDigest()}, {17, retrySQL, retryDigest()}, {18, associationRetrySQL, associationRetryDigest()}, {19, referenceEncodingSQL, referenceEncodingDigest()}, {20, finalCoverageSQL, finalCoverageDigest()}, {21, revalidationSQL, revalidationDigest()}, {22, batchRetrySQL, batchRetryDigest()}, {23, styleCorrectionSQL, styleCorrectionDigest()}, {24, resultRevalidationSQL, resultRevalidationDigest()}, {25, methodDiagnosticSQL, methodDiagnosticDigest()}, {26, promptDiagnosticSQL, promptDiagnosticDigest()}} {
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
	if count != 2 && count != 3 && count != 4 && count != 5 && count != 6 && count != 7 && count != 8 && count != 9 && count != 10 && count != 11 && count != 12 && count != 13 && count != 14 && count != 15 && count != 16 && count != 17 && count != 18 && count != 19 && count != 20 && count != 21 && count != 22 && count != 23 && count != 24 && count != 25 && count != 26 {
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
	if hash != gateDigest() || count != 26 {
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
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=6").Scan(&hash); e != nil {
		return e
	}
	if hash != analysisDigest() {
		return errors.New("analysis migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=7").Scan(&hash); e != nil {
		return e
	}
	if hash != scheduleDigest() {
		return errors.New("analysis schedule migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=8").Scan(&hash); e != nil {
		return e
	}
	if hash != associationDigest() {
		return errors.New("analysis association migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=9").Scan(&hash); e != nil {
		return e
	}
	if hash != promptsDigest() {
		return errors.New("analysis prompts migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=10").Scan(&hash); e != nil {
		return e
	}
	if hash != resultsDigest() {
		return errors.New("analysis results migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=11").Scan(&hash); e != nil {
		return e
	}
	if hash != learningDigest() {
		return errors.New("analysis learning migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=12").Scan(&hash); e != nil {
		return e
	}
	if hash != admissionDigest() {
		return errors.New("analysis admission migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=13").Scan(&hash); e != nil {
		return e
	}
	if hash != budgetStatusDigest() {
		return errors.New("analysis budget status migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=14").Scan(&hash); e != nil {
		return e
	}
	if hash != setReviewDigest() {
		return errors.New("set review migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=15").Scan(&hash); e != nil {
		return e
	}
	if hash != lifecycleDigest() {
		return errors.New("collection lifecycle migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=16").Scan(&hash); e != nil {
		return e
	}
	if hash != qualityDigest() {
		return errors.New("analysis quality migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=17").Scan(&hash); e != nil {
		return e
	}
	if hash != retryDigest() {
		return errors.New("analysis retry migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=18").Scan(&hash); e != nil {
		return e
	}
	if hash != associationRetryDigest() {
		return errors.New("association retry migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=19").Scan(&hash); e != nil {
		return e
	}
	if hash != referenceEncodingDigest() {
		return errors.New("association reference encoding migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=20").Scan(&hash); e != nil {
		return e
	}
	if hash != finalCoverageDigest() {
		return errors.New("association coverage migration mismatch")
	}
	if e := q.QueryRow(ctx, "SELECT "+gateHashColumn+" FROM "+gateHistory+" WHERE version=21").Scan(&hash); e != nil {
		return e
	}
	if hash != revalidationDigest() {
		return errors.New("association revalidation migration mismatch")
	}
	if err := q.QueryRow(ctx, "SELECT digest FROM studio.world_schema_history WHERE version=22").Scan(&hash); err != nil {
		return err
	}
	if hash != batchRetryDigest() {
		return errors.New("batch retry migration mismatch")
	}
	if err := q.QueryRow(ctx, "SELECT digest FROM studio.world_schema_history WHERE version=23").Scan(&hash); err != nil {
		return err
	}
	if hash != styleCorrectionDigest() {
		return errors.New("style correction migration mismatch")
	}
	if err := q.QueryRow(ctx, "SELECT digest FROM studio.world_schema_history WHERE version=24").Scan(&hash); err != nil {
		return err
	}
	if hash != resultRevalidationDigest() {
		return errors.New("result revalidation migration mismatch")
	}
	if err := q.QueryRow(ctx, "SELECT digest FROM studio.world_schema_history WHERE version=25").Scan(&hash); err != nil {
		return err
	}
	if hash != methodDiagnosticDigest() {
		return errors.New("method diagnostic migration mismatch")
	}
	if err := q.QueryRow(ctx, "SELECT digest FROM studio.world_schema_history WHERE version=26").Scan(&hash); err != nil {
		return err
	}
	if hash != promptDiagnosticDigest() {
		return errors.New("prompt diagnostic migration mismatch")
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
	for _, match := range regexp.MustCompile(`ON studio\.([a-z_]+) FOR EACH STATEMENT`).FindAllStringSubmatch(gateSQL+"\n"+draftSQL+"\n"+analysisSQL+"\n"+scheduleSQL+"\n"+associationSQL+"\n"+promptsSQL+"\n"+resultsSQL+"\n"+learningSQL+"\n"+admissionSQL+"\n"+setReviewSQL+"\n"+lifecycleSQL+"\n"+qualitySQL+"\n"+retrySQL+"\n"+associationRetrySQL+"\n"+revalidationSQL+"\n"+batchRetrySQL, -1) {
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
	for _, match := range regexp.MustCompile(`ON studio\.([a-z_]+) FOR EACH STATEMENT`).FindAllStringSubmatch(gateSQL+"\n"+draftSQL+"\n"+analysisSQL+"\n"+scheduleSQL+"\n"+associationSQL+"\n"+promptsSQL+"\n"+resultsSQL+"\n"+learningSQL+"\n"+admissionSQL+"\n"+setReviewSQL+"\n"+lifecycleSQL+"\n"+qualitySQL+"\n"+retrySQL+"\n"+associationRetrySQL+"\n"+revalidationSQL+"\n"+batchRetrySQL, -1) {
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
