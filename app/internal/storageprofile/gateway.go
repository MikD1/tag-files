package storageprofile

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
)

type gateway struct {
	server    *http.Server
	transport *http.Transport
	done      chan error
}

// The pinned backend binds only loopback, including its control interfaces.
// The network-facing S3 port is opened only after the complete live preflight.
// This closes the startup window for an already-running app reconnecting while
// private bucket access/lifecycle configuration is still being checked.
func openGateway() (*gateway, error) {
	listener, err := net.Listen("tcp", "0.0.0.0:8333")
	if err != nil {
		return nil, fmt.Errorf("open private S3 gateway: %w", err)
	}
	target, _ := url.Parse(backendS3Endpoint)
	proxy := httputil.NewSingleHostReverseProxy(target)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	transport.MaxIdleConnsPerHost = 32
	proxy.Transport = transport
	epoch := domain.NewUUID()
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Header.Set(storageprotocol.EpochHeader, epoch)
		return nil
	}
	// The default Director preserves the original Host, which belongs to the
	// client's SigV4 signature. Bodies are streamed, never buffered or replayed.
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "storage backend unavailable", http.StatusBadGateway)
	}
	g := &gateway{server: &http.Server{Handler: epochGate(epoch, proxy), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}, transport: transport, done: make(chan error, 1)}
	go func() { g.done <- g.server.Serve(listener) }()
	return g, nil
}

func epochGate(epoch string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expected := r.Header.Get(storageprotocol.ExpectedEpochHeader); expected != "" && expected != epoch {
			w.Header().Set(storageprotocol.EpochHeader, epoch)
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`<Error><Code>` + storageprotocol.EpochMismatchCode + `</Code><Message>The storage backend restarted.</Message></Error>`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *gateway) close() { _ = g.server.Close(); g.transport.CloseIdleConnections() }
