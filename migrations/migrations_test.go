package migrations

import (
	"strings"
	"testing"
)

func TestWorldFreshSchemaHasLocalOwnershipAndSourceProof(t *testing.T) {
	sql, digest, err := schema()
	if err != nil || len(digest) != 64 {
		t.Fatal("invalid schema identity", err)
	}
	for _, foreign := range []string{"REFERENCES studio.projects", "REFERENCES studio.project_revisions", "CREATE TABLE studio.project_", "CREATE TABLE studio.studio_events"} {
		if strings.Contains(sql, foreign) {
			t.Fatal("foreign business ownership retained")
		}
	}
	for _, required := range []string{"source_manifest_ref jsonb NOT NULL", "world_grant_changes", "world_coord_reservations", "world_coord_outbox", "world_versions_immutable", "world_events_immutable"} {
		if !strings.Contains(sql, required) {
			t.Fatal("missing durable invariant", required)
		}
	}
}
