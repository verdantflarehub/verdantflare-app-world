package world

import (
	"slices"
	"testing"
)

func TestCharacterDomainKeepsIdentityTypes(t *testing.T) {
	types, _, err := worldCatalogFilters(WorldListRequest{Domain: "character"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"character", "character-image"} {
		if !slices.Contains(types, want) {
			t.Fatalf("character domain types %v miss %s", types, want)
		}
	}
	if _, _, err := worldCatalogFilters(WorldListRequest{Domain: "wardrobe"}); err == nil {
		t.Fatal("unknown domain must stay invalid")
	}
}

func TestListPreviewPrefersThumbnailThenImage(t *testing.T) {
	files := []File{
		{ID: "a", Path: "k/pose.json", Role: "domain-json"},
		{ID: "b", Path: "k/views/front.png", Role: "pose-view"},
		{ID: "c", Path: "k/thumbs/front.jpg", Role: "thumbnail"},
	}
	if got := listPreview(files, ""); got == nil || got.ID != "c" {
		t.Fatalf("want thumbnail, got %+v", got)
	}
	if got := listPreview(files[:2], ""); got == nil || got.ID != "b" {
		t.Fatalf("want first image, got %+v", got)
	}
	if got := listPreview(files[:1], ""); got != nil {
		t.Fatalf("want no preview, got %+v", got)
	}
}

func TestPoseAndCostumeAreOwnDomains(t *testing.T) {
	for domain, want := range map[string]string{"pose": "pose-reference", "costume": "costume-reference"} {
		types, _, err := worldCatalogFilters(WorldListRequest{Domain: domain})
		if err != nil || len(types) != 1 || types[0] != want {
			t.Fatalf("%s domain = %v, %v", domain, types, err)
		}
	}
	character, _, _ := worldCatalogFilters(WorldListRequest{Domain: "character"})
	if slices.Contains(character, "pose-reference") || slices.Contains(character, "costume-reference") {
		t.Fatalf("character domain must not list pose or costume references: %v", character)
	}
}

func TestListPreviewHonoursViewHint(t *testing.T) {
	files := []File{
		{ID: "a", Path: "k/thumbs/front.jpg", Role: "thumbnail"},
		{ID: "b", Path: "k/thumbs/diag.jpg", Role: "thumbnail"},
	}
	if got := listPreview(files, "diag"); got == nil || got.ID != "b" {
		t.Fatalf("want diag thumbnail, got %+v", got)
	}
	if got := listPreview(files, "low"); got == nil || got.ID != "a" {
		t.Fatalf("missing view falls back to first thumbnail, got %+v", got)
	}
}

func TestListExtrasValidation(t *testing.T) {
	ok := WorldListRequest{Subjects: []string{"体系:上半身单人", "动作:站姿"}, Facets: []string{"动作", "人数"}, PreviewHint: "diag"}
	if !validListExtras(ok) {
		t.Fatal("valid extras rejected")
	}
	for _, bad := range []WorldListRequest{
		{Facets: []string{"动作:站姿"}},
		{Subjects: []string{"a", "a"}},
		{PreviewHint: "Diag"},
		{Facets: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}},
	} {
		if validListExtras(bad) {
			t.Fatalf("invalid extras accepted: %+v", bad)
		}
	}
}
