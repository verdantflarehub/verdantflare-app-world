package world

import (
	"slices"
	"testing"
)

func TestCharacterDomainIncludesPoseAndCostumeReferences(t *testing.T) {
	types, _, err := worldCatalogFilters(WorldListRequest{Domain: "character"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"character", "character-image", "pose-reference", "costume-reference"} {
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
	if got := listPreview(files); got == nil || got.ID != "c" {
		t.Fatalf("want thumbnail, got %+v", got)
	}
	if got := listPreview(files[:2]); got == nil || got.ID != "b" {
		t.Fatalf("want first image, got %+v", got)
	}
	if got := listPreview(files[:1]); got != nil {
		t.Fatalf("want no preview, got %+v", got)
	}
}
