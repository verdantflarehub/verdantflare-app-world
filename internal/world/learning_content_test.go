package world

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestLearningSharedContractCases(t *testing.T) {
	data, err := os.ReadFile("testdata/reference_learning_cases.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Case     string          `json:"case"`
		Valid    bool            `json:"valid"`
		Document json.RawMessage `json:"document"`
		FileIDs  []string        `json:"file_ids"`
	}
	if json.Unmarshal(data, &cases) != nil || len(cases) == 0 {
		t.Fatal("missing central cases")
	}
	for i, c := range cases {
		t.Run(fmt.Sprintf("%02d_%s", i, c.Case), func(t *testing.T) {
			files := []File{}
			for _, id := range c.FileIDs {
				files = append(files, File{ID: id})
			}
			_, err := ValidateLearningContent(c.Document, files)
			if (err == nil) != c.Valid {
				t.Fatalf("expected valid=%v, got %v", c.Valid, err)
			}
		})
	}
}
func TestLearningExactTimebaseAndIntegerPrecision(t *testing.T) {
	doc := `{"schema_version":1,"document_kind":"reference-learning","origin":"manual","sources":[{"key":"s","title":"source","media_kind":"video","role":"original","acquisition_state":"ready","content":{"kind":"inline","text":"fixture"}}],"results":[],"relations":[],"uses":[],"coverage":[{"source_key":"s","dimension":"picture","state":"complete","requested_scope":{"kind":"time","start_tick":9007199254740992,"end_tick":9007199254740993,"timebase_num":1,"timebase_den":30},"covered_scopes":[{"kind":"time","start_tick":9007199254740992,"end_tick":9007199254740993,"timebase_num":1,"timebase_den":30}],"note":"one exact tick"}]}`
	if _, err := ValidateLearningContent([]byte(doc), nil); err != nil {
		t.Fatal("exact one-tick interval rejected", err)
	}
	broken := strings.Replace(doc, `"covered_scopes":[{"kind":"time","start_tick":9007199254740992,"end_tick":9007199254740993`, `"covered_scopes":[{"kind":"time","start_tick":9007199254740992,"end_tick":9007199254740992`, 1)
	if _, err := ValidateLearningContent([]byte(broken), nil); err == nil {
		t.Fatal("rounded empty interval accepted")
	}
}
func TestLearningPayloadTextCannotChangeValidationOrExecute(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":1,"document_kind":"reference-learning","origin":"manual","sources":[],"results":[],"relations":[],"uses":[],"coverage":[],"install_skill":true}`,
		`{"schema_version":1,"schema_version":1,"document_kind":"reference-learning","origin":"manual","sources":[],"results":[],"relations":[],"uses":[],"coverage":[]}`,
	} {
		if _, err := ValidateLearningContent([]byte(raw), nil); err == nil {
			t.Fatal("foreign instruction or duplicate accepted")
		}
	}
}
