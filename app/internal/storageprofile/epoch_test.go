package storageprofile

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikd1/tag-files/app/internal/storageprotocol"
)

func TestGatewayEpochFenceRejectsStaleRequestsBeforeForwarding(t *testing.T) {
	const epoch = "11111111-1111-4111-8111-111111111111"
	forwarded := 0
	handler := epochGate(epoch, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded++; w.WriteHeader(204) }))
	for _, expected := range []string{"", epoch, "22222222-2222-4222-8222-222222222222"} {
		request := httptest.NewRequest("PUT", "http://storage:8333/tag-files/incoming", strings.NewReader("body"))
		if expected != "" {
			request.Header.Set(storageprotocol.ExpectedEpochHeader, expected)
		}
		response := httptest.NewRecorder()
		before := forwarded
		handler.ServeHTTP(response, request)
		if expected != "" && expected != epoch {
			if response.Header().Get(storageprotocol.EpochHeader) != epoch {
				t.Fatal("refusal did not identify this live gateway")
			}
			if forwarded != before || response.Code != 409 || !strings.Contains(response.Body.String(), "<Code>StorageEpochChanged</Code>") {
				t.Fatalf("stale write reached backend: %d %d %s", forwarded-before, response.Code, response.Body.String())
			}
		} else if response.Code != 204 || forwarded != before+1 {
			t.Fatal("current/standard S3 request was not forwarded")
		}
	}
}
