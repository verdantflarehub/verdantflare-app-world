package artifactclient

import (
	"github.com/verdantflarehub/verdantflare-app-world/internal/world"
	"net/url"
	"testing"
)

func TestDraftReadURLPinsScope(t *testing.T) {
	ref := world.ContentRef{StoreID: "store", ArtifactID: "artifact", VersionID: "version"}
	a := world.Access{WorldDraftID: "draft", WorldDraftRevisionID: "revision"}
	for _, content := range []bool{true, false} {
		u, err := url.Parse(path(ref, a, content))
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if q.Get("world_draft_id") != "draft" || q.Get("world_draft_revision_id") != "revision" || q.Get("project_id") != "" || q.Get("asset_id") != "" || len(q) != 4 {
			t.Fatal("scope lost or changed")
		}
	}
}
