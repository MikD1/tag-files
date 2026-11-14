package httpapi

import (
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestCompleteContractValidatesAndRetainsStreamingBoundaries(t *testing.T) {
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/items", "/api/v1/items/{id}", "/api/v1/items/bulk-tags", "/api/v1/items/bulk-collection", "/api/v1/items/bulk-metadata", "/api/v1/items/{id}/views", "/api/v1/items/{id}/favorite", "/api/v1/items/{id}/similar", "/api/v1/tags", "/api/v1/tags/{id}", "/api/v1/tag-statistics", "/api/v1/saved-searches", "/api/v1/saved-searches/{id}", "/api/v1/collections", "/api/v1/collections/{id}", "/api/v1/navigation-contexts", "/api/v1/navigation-contexts/{id}/neighbors", "/api/v1/upload-sessions", "/api/v1/upload-sessions/{id}", "/api/v1/upload-sessions/{id}/parts/{number}", "/api/v1/upload-sessions/{id}/complete", "/api/v1/upload-batches", "/api/v1/jobs/{id}/retry", "/api/v1/jobs/{id}/cancel", "/api/v1/operations/{id}", "/media/{assetId}", "/health/live", "/health/ready", "/api/v1/system/status"} {
		if spec.Paths.Value(path) == nil {
			t.Fatalf("missing specified operation path: %s", path)
		}
	}
	part := spec.Paths.Value("/api/v1/upload-sessions/{id}/parts/{number}").Put
	if part.RequestBody.Value.Content.Get("application/octet-stream") == nil || part.RequestBody.Value.Content.Get("application/json") != nil {
		t.Fatal("binary part crossed JSON boundary")
	}
	media := spec.Paths.Value("/media/{assetId}")
	if media.Head == nil || media.Get.Responses.Value("206") == nil || media.Get.Responses.Value("416") == nil {
		t.Fatal("media Range/HEAD contract incomplete")
	}
	complete := spec.Paths.Value("/api/v1/upload-sessions/{id}/complete").Post
	hasKey := false
	for _, p := range complete.Parameters {
		if p.Value.Name == "Idempotency-Key" && p.Value.In == "header" && p.Value.Required {
			hasKey = true
		}
	}
	if !hasKey || spec.Components.Schemas["UploadOutcome"].Value.Properties["operationId"] == nil {
		t.Fatal("Complete must support operation-key receipts alongside upload-ID replay")
	}
	for _, field := range []string{"title", "originalFilename", "displayName", "durationTicks", "objectKey", "bucket"} {
		if spec.Components.Schemas["Item"].Value.Properties[field] != nil {
			t.Fatalf("internal item field %s became public", field)
		}
	}
}
