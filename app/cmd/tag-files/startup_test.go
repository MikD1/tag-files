package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/application"
)

func TestStartupWaitsForTransientStorageButRefusesPermanentConfiguration(t *testing.T) {
	calls := 0
	expected := &application.App{}
	open := func(context.Context, application.Config) (*application.App, error) {
		calls++
		if calls < 3 {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		}
		return expected, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := openApplication(ctx, application.Config{}, open)
	if err != nil || got != expected || calls != 3 {
		t.Fatalf("transient dependency: calls=%d err=%v", calls, err)
	}
	permanent := errors.New("incompatible library")
	calls = 0
	_, err = openApplication(ctx, application.Config{}, func(context.Context, application.Config) (*application.App, error) { calls++; return nil, permanent })
	if !errors.Is(err, permanent) || calls != 1 {
		t.Fatal("permanent refusal retried", calls, err)
	}
}

func TestStartupWaitIsCancelledBeforeAnotherAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := openApplication(ctx, application.Config{}, func(context.Context, application.Config) (*application.App, error) {
		calls++
		cancel()
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancel did not stop dependency retry", calls, err)
	}
}
