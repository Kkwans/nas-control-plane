package docker

import (
	"reflect"
	"testing"
)

func TestMapHubTagPublishesTimestampAndNormalizesArchitectures(t *testing.T) {
	tag := mapHubTag(hubTagWire{
		Name:        "latest",
		LastUpdated: "2026-07-29T12:34:56Z",
		FullSize:    42,
		Images: []hubTagImageWire{
			{OS: "linux", Architecture: "arm64", Variant: "v8"},
			{OS: "linux", Architecture: "amd64"},
			{OS: "linux", Architecture: "arm64", Variant: "v8"},
		},
	})

	if tag.PublishedAt != "2026-07-29T12:34:56Z" {
		t.Fatalf("publishedAt = %q", tag.PublishedAt)
	}
	if tag.LastUpdated != tag.PublishedAt {
		t.Fatalf("legacy lastUpdated = %q, publishedAt = %q", tag.LastUpdated, tag.PublishedAt)
	}
	wantArchitectures := []string{"linux/amd64", "linux/arm64/v8"}
	if !reflect.DeepEqual(tag.Architectures, wantArchitectures) {
		t.Fatalf("architectures = %#v, want %#v", tag.Architectures, wantArchitectures)
	}
}
