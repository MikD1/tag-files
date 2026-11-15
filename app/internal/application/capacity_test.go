package application_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/application"
	"github.com/mikd1/tag-files/app/internal/domain"
)

func TestApplicationMetadataAdmissionReturns507WhileReadsRemainAvailable(t *testing.T) {
	c, _ := setup(t)
	c.MinimumFreeBytes = domain.MaxSafeInteger
	app, err := application.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	origin, stop, done := serve(t, app)
	defer stop()
	defer func() { stop(); <-done }()
	req, err := http.NewRequest("POST", origin+"/api/v1/tags", strings.NewReader(`{"name":"not-admitted"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", domain.NewUUID())
	response, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 507 {
		t.Fatal("factory bypassed metadata headroom", response.StatusCode)
	}
	status, body := request(t, origin, "/api/v1/tags")
	if status != 200 || strings.Contains(string(body), "not-admitted") {
		t.Fatal("507 mutated tags or blocked reads", status, string(body))
	}
	status, body = request(t, origin, "/api/v1/system/status")
	var report struct {
		Disk map[string]map[string]any `json:"disk"`
	}
	if err := json.Unmarshal(body, &report); err != nil || status != 200 || report.Disk["metadata"]["status"] != "measured" || report.Disk["storage"]["availableBytes"] == nil {
		t.Fatal("factory did not expose measured capacities", status, string(body), err)
	}
}
