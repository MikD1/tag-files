package scratch

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
)

// Moving removal ahead of callback/monitor joining would delete a live input.
func TestRunCancelsOnCapacityAndWaitsBeforeRemovingAttempt(t *testing.T) {
	m, err := Open(Config{Root: t.TempDir(), MaxBytes: 10000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, err := m.Begin(domain.NewUUID(), 1, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = a.Run(ctx, func(work context.Context) error {
		if err := os.WriteFile(a.Output, make([]byte, 21), 0600); err != nil {
			return err
		}
		<-work.Done()
		if _, err := os.Stat(a.Output); err != nil {
			t.Error("monitor removed files while producer still ran")
		}
		return work.Err()
	})
	if !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("lost capacity cancellation cause: %v", err)
	}
	if _, err := os.Stat(a.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stopped attempt remains")
	}
}
