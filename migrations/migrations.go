// Package migrations creates the independent World schema on explicit request.
// Legacy migrations are preserved verbatim for controlled migration comparison.
package migrations

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/verdantflarehub/verdantflare-app-world/internal/coordination"
)

//go:embed 0001_world.sql
var initial string

func schema() (string, string, error) {
	coord, err := coordination.Schema("world")
	if err != nil {
		return "", "", err
	}
	sql := initial + "\n" + coord
	hash := sha256.Sum256([]byte(sql))
	return sql, hex.EncodeToString(hash[:]), nil
}
func ApplyFresh(ctx context.Context, db *pgxpool.Pool, projectFence, worldFence int64) error {
	if projectFence < 1 || worldFence < 1 {
		return errors.New("externally fenced epochs required")
	}
	sql, digest, err := schema()
	if err != nil {
		return err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(2026101105)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS studio; CREATE TABLE IF NOT EXISTS studio.world_schema_history(version integer PRIMARY KEY,digest text NOT NULL)"); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM studio.world_schema_history").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		// Existing legacy tables deliberately fail CREATE: adoption needs an audited
		// freeze, ownership handoff and immutable reference comparison.
		if _, err = tx.Exec(ctx, sql); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO studio.world_coord_fences(coordinator,fence) VALUES('project',$1),('world',$2)", projectFence, worldFence); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO studio.world_schema_history VALUES(1,$1)", digest); err != nil {
			return err
		}
	} else {
		var saved string
		if count != 23 {
			return errors.New("existing World schema requires explicit gate upgrade")
		}
		if err = tx.QueryRow(ctx, "SELECT digest FROM studio.world_schema_history WHERE version=1").Scan(&saved); err != nil {
			return err
		}
		if saved != digest {
			return errors.New("World migration digest mismatch")
		}
		for domain, wanted := range map[string]int64{"project": projectFence, "world": worldFence} {
			var existing int64
			if err = tx.QueryRow(ctx, "SELECT fence FROM studio.world_coord_fences WHERE coordinator=$1", domain).Scan(&existing); err != nil {
				return err
			}
			if existing != wanted {
				return errors.New("migration cannot advance or rewind recovery fence")
			}
		}
	}
	if err = applyGate(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func Check(ctx context.Context, db *pgxpool.Pool) error {
	_, digest, err := schema()
	if err != nil {
		return err
	}
	var saved string
	var count int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM studio.world_schema_history").Scan(&count); err != nil {
		return err
	}
	if count != 23 {
		return errors.New("unsupported World schema")
	}
	if err = db.QueryRow(ctx, "SELECT digest FROM studio.world_schema_history WHERE version=1").Scan(&saved); err != nil {
		return err
	}
	if saved != digest {
		return errors.New("World migration digest mismatch")
	}
	return CheckGate(ctx, db)
}
