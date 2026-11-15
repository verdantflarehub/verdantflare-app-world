package analysis

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLearningAssemblyLegacyFingerprintOmitsNewField(t *testing.T) {
	legacy := Task{}
	baseline := canonicalHash(legacy)
	for _, value := range []string{"", "1"} {
		normalized, err := NormalizeAssemblyVersion(value)
		if err != nil {
			t.Fatal(err)
		}
		legacy.AssemblyVersion = normalized
		raw, _ := json.Marshal(legacy)
		if strings.Contains(string(raw), "assembly_version") || canonicalHash(legacy) != baseline || legacy.LearningAssemblyVersion() != "1" {
			t.Fatal("legacy fingerprint changed")
		}
	}
	legacy.AssemblyVersion = "2"
	if canonicalHash(legacy) == baseline || legacy.LearningAssemblyVersion() != "2" {
		t.Fatal("new version not frozen in fingerprint")
	}
	if _, err := NormalizeAssemblyVersion("3"); err == nil {
		t.Fatal("unknown version accepted")
	}
}
