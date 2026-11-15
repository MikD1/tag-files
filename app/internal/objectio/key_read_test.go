package objectio_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/objectio"
)

// A global registration would unnecessarily block cleanup of another object;
// retiring before the callback ends would let deletion truncate an active read.
func TestReadKeyDrainsOnlyItsObjectAndLivesThroughCallback(t *testing.T) {
	db, _ := setup(t)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Tag-Files-Storage-Epoch", epochOne) }))
	defer peer.Close()
	m, err := objectio.New(db, client(t, peer.URL), 2)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1), make(chan struct{})
	go func() {
		defer close(finished)
		done <- m.ReadKey(ctx, "primary/owned/media.mp4", func(_ objectio.KeyReader) error { close(entered); <-release; return nil })
	}()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-finished
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("read not admitted")
	}
	other, cancelOther := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelOther()
	if err := m.Drain(other, "primary/other/media.mp4", ""); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := m.Drain(deadline, "primary/owned/media.mp4", ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("live read retired before callback returned: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := m.Drain(ctx, "primary/owned/media.mp4", ""); err != nil {
		t.Fatal(err)
	}
}
