package world

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAssetManifestVersionsReadStrictly(t *testing.T) {
	const id = "01900000-0000-7000-8000-000000000001"
	legacy := `{"schema_version":1,"kind":"asset-version","asset_id":"` + id + `","name":"Prompt","asset_type":"prompt","subjects":[],"files":[{"file_id":"` + id + `","path":"prompt.txt","role":"prompt","content_ref":{"store_id":"` + id + `","artifact_id":"` + id + `","version_id":"` + id + `"}}],"source":{"project_id":"` + id + `","project_revision_id":"` + id + `","relation":"curated_in"},"depends_on":[]}`
	current := strings.Replace(legacy, `"schema_version":1`, `"schema_version":2,"domain_documents":[]`, 1)
	current = strings.Replace(current, `"project_id":"`+id+`","project_revision_id":"`+id+`"`, `"kind":"world_draft","draft_id":"`+id+`","draft_revision_id":"`+id+`"`, 1)
	for _, data := range []string{legacy, current} {
		m, err := decodeAsset([]byte(data))
		if err != nil {
			t.Fatal("valid manifest rejected", err)
		}
		encoded, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = decodeAsset(encoded); err != nil {
			t.Fatal("manifest did not round trip", err)
		}
	}
	for _, bad := range []string{
		strings.Replace(current, `"schema_version":2`, `"schema_version":1`, 1),
		strings.Replace(legacy, `"schema_version":1`, `"schema_version":2`, 1),
		strings.Replace(current, `"domain_documents":[],`, "", 1),
		strings.Replace(current, `"domain_documents":[]`, `"domain_documents":null`, 1),
		strings.Replace(current, `"source":{`, `"source":{"project_id":"`+id+`",`, 1),
		strings.Replace(legacy, `"source":{`, `"source":{"kind":"world_draft",`, 1),
		strings.Replace(current, `"domain_documents":[]`, `"domain_documents":[{"type":"reference-learning","file_id":"01900000-0000-7000-8000-000000000002"}]`, 1),
	} {
		if _, err := decodeAsset([]byte(bad)); err == nil {
			t.Fatal("mixed or malformed manifest accepted")
		}
	}
}
