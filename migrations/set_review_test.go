package migrations

import (
	"strings"
	"testing"
)

func TestSetReviewMigrationOwnsImmutableVersionedChains(t *testing.T) {
	for _, part := range []string{"PRIMARY KEY(organization_id,asset_id,version_id,record_id)", "UNIQUE(organization_id,asset_id,version_id,sequence)", "record_bytes bytea NOT NULL", "FOREIGN KEY(organization_id,asset_id,version_id,previous_record_id)", "world_set_review_records_immutable BEFORE UPDATE OR DELETE OR TRUNCATE", "world_set_review_requests_immutable BEFORE UPDATE OR DELETE OR TRUNCATE", "world_set_review_target_immutable", "world_set_review_heads ENABLE ALWAYS TRIGGER world_handoff_gate", "world_set_review_records ENABLE ALWAYS TRIGGER world_handoff_gate", "world_set_review_requests ENABLE ALWAYS TRIGGER world_handoff_gate", "world_set_review_cursors ENABLE ALWAYS TRIGGER world_handoff_gate"} {
		if !strings.Contains(setReviewSQL, part) {
			t.Fatal("missing durable invariant", part)
		}
	}
	if strings.Contains(setReviewSQL, "studio.project_") {
		t.Fatal("cross-service SQL introduced")
	}
	if len(setReviewDigest()) != 64 {
		t.Fatal("migration digest absent")
	}
}
