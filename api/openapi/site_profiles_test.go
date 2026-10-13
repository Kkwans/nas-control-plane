package openapi_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestSiteProfileResponseReferences(t *testing.T) {
	document := loadSiteProfileSpecification(t)
	for _, test := range []struct {
		path, method, status string
		array                bool
	}{
		{"/api/v1/sites", "POST", "201", false},
		{"/api/v1/sites/{siteID}", "PUT", "200", false},
		{"/api/v1/sites/ignored", "GET", "200", true},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			operation := document.Paths.Value(test.path).GetOperation(test.method)
			response := operation.Responses.Value(test.status).Value.Content.Get("application/json").Schema
			if test.array {
				if !response.Value.Type.Is("array") {
					t.Fatal("ignored response must be an array")
				}
				response = response.Value.Items
			} else if operation.RequestBody.Value.Content.Get("application/json").Schema.Ref != "#/components/schemas/SiteProfileInput" {
				t.Fatal("write requests must retain SiteProfileInput")
			}
			if response.Ref != "#/components/schemas/SiteProfileResponse" {
				t.Fatalf("response must use SiteProfileResponse, got %q", response.Ref)
			}
		})
	}
	if document.Components.Schemas["SiteProfileInput"].Value.Properties["name"].Value.MinLength != 1 {
		t.Fatal("input name must retain its nonempty constraint")
	}
}

func TestSiteProfileResponseBackendJSON(t *testing.T) {
	document := loadSiteProfileSpecification(t)
	schema := document.Components.Schemas["SiteProfileResponse"].Value
	ignored := document.Paths.Value("/api/v1/sites/ignored").Get.Responses.Value("200").Value.Content.Get("application/json").Schema.Value
	visited := time.Date(2026, 10, 13, 12, 34, 56, 0, time.UTC)
	for _, test := range []struct {
		name    string
		profile controlstore.SiteProfile
	}{
		{"created manual", controlstore.SiteProfile{ProjectID: "manual:1", Name: "NAS", LaunchURL: "https://nas.local/", Source: "manual"}},
		{"updated metadata", controlstore.SiteProfile{
			ProjectID: "compose:media/web", Name: "媒体", Description: "媒体服务", IconURL: "/api/v1/sites/icon/hash.png",
			Category: "媒体", PrimaryPort: 8096, LaunchURL: "http://nas.local:8096/", Favorite: true,
			SortOrder: -2, Hidden: true, Source: "edited", Ignored: true, LastVisitedAt: &visited,
			DetectedTitle: "Media server", AutoIconURL: "https://nas.local/favicon.ico", LocalIconName: "hash.png",
		}},
		{"ignored placeholder", controlstore.SiteProfile{ProjectID: "compose:removed", Source: "auto", Ignored: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := siteProfileJSON(t, test.profile)
			if err := schema.VisitJSON(value); err != nil {
				t.Fatalf("backend SiteProfile JSON rejected: %v", err)
			}
			for field := range value {
				if schema.Properties[field] == nil {
					t.Errorf("backend field %q is undocumented", field)
				}
			}
			if test.profile.LastVisitedAt == nil && value["lastVisitedAt"] != nil {
				t.Fatal("unvisited backend profile must serialize null")
			}
			if test.profile.Ignored {
				if err := ignored.VisitJSON([]any{value}); err != nil {
					t.Fatalf("ignored backend profile rejected: %v", err)
				}
			}
		})
	}
	if err := ignored.VisitJSON([]any{}); err != nil {
		t.Fatalf("empty ignored list rejected: %v", err)
	}
	if err := ignored.VisitJSON(nil); err == nil {
		t.Fatal("null must not be accepted as an ignored list")
	}
}

func TestSiteProfileResponseCompatibility(t *testing.T) {
	document := loadSiteProfileSpecification(t)
	schema := document.Components.Schemas["SiteProfileResponse"].Value
	value := siteProfileJSON(t, controlstore.SiteProfile{ProjectID: "compose:old"})
	for _, field := range []string{"source", "ignored", "lastVisitedAt", "detectedTitle", "autoIconUrl", "localIconName"} {
		if slices.Contains(schema.Required, field) {
			t.Fatalf("legacy response metadata %q must remain optional", field)
		}
		delete(value, field)
	}
	for _, source := range []string{"auto", "labels", "built-in", "edited", "manual"} {
		value["source"] = source
		if err := schema.VisitJSON(value); err != nil {
			t.Fatalf("known source %q rejected: %v", source, err)
		}
	}
	delete(value, "source")
	value["futureMetadata"] = map[string]any{"revision": float64(1)}
	if err := schema.VisitJSON(value); err != nil {
		t.Fatalf("legacy metadata omissions and unknown extras must remain compatible: %v", err)
	}
	if err := document.Components.Schemas["SiteProfileInput"].Value.VisitJSON(value); err == nil {
		t.Fatal("an empty ignored placeholder name must remain invalid for input")
	}
}

func TestSiteProfileResponseRejectsMalformedFields(t *testing.T) {
	schema := loadSiteProfileSpecification(t).Components.Schemas["SiteProfileResponse"].Value
	for _, test := range []struct {
		name, field string
		value       any
		omit        bool
	}{
		{"missing identity", "projectId", nil, true},
		{"empty identity", "projectId", "", false},
		{"null identity", "projectId", nil, false},
		{"numeric name", "name", float64(1), false},
		{"numeric favorite", "favorite", float64(1), false},
		{"fractional sort", "sortOrder", 0.5, false},
		{"negative port", "primaryPort", float64(-1), false},
		{"out of range port", "primaryPort", float64(65536), false},
		{"numeric source", "source", float64(1), false},
		{"unknown source", "source", "unknown", false},
		{"string ignored", "ignored", "true", false},
		{"null title", "detectedTitle", nil, false},
		{"object auto icon", "autoIconUrl", map[string]any{}, false},
		{"array local icon", "localIconName", []any{}, false},
		{"numeric last visit", "lastVisitedAt", float64(1), false},
		{"invalid last visit", "lastVisitedAt", "yesterday", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := siteProfileJSON(t, controlstore.SiteProfile{ProjectID: "manual:1", Name: "NAS", Source: "manual"})
			if test.omit {
				delete(value, test.field)
			} else {
				value[test.field] = test.value
			}
			if err := schema.VisitJSON(value); err == nil {
				t.Fatalf("malformed %s unexpectedly accepted", test.field)
			}
		})
	}
}

func loadSiteProfileSpecification(t *testing.T) *openapi3.T {
	t.Helper()
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return document
}

// 仅编码真实响应类型，不打开数据库、监听器或连接外部服务。
func siteProfileJSON(t *testing.T, profile controlstore.SiteProfile) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
