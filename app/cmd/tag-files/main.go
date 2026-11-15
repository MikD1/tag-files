// tag-files runs the application. Data migration and backup/restore remain
// separate offline tools; this command only serves or checks runtime health.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mikd1/tag-files/app/internal/application"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/scratch"
)

var version = "development"

type settings struct {
	listen string
	app    application.Config
}

func readSecret(path string) (string, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("required application secret file is unavailable: %s", path)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("application secret must be a regular file: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, 258))
	value := strings.TrimSuffix(string(data), "\n")
	if err != nil || len(value) < 20 || len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("invalid application secret file: %s", path)
	}
	return value, nil
}

func env(getenv func(string) string, key, fallback string) string {
	if value := getenv(key); value != "" {
		return value
	}
	return fallback
}

func byteLimit(getenv func(string) string, key string, fallback int64) (int64, error) {
	value := getenv(key)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 1 || n > domain.MaxSafeInteger {
		return 0, fmt.Errorf("invalid byte limit: %s", key)
	}
	return n, nil
}

func settingsFrom(getenv func(string) string) (settings, error) {
	access, err := readSecret(env(getenv, "TAG_FILES_ACCESS_KEY_FILE", "/run/secrets/s3_access_key"))
	if err != nil {
		return settings{}, err
	}
	secret, err := readSecret(env(getenv, "TAG_FILES_SECRET_KEY_FILE", "/run/secrets/s3_secret_key"))
	if err != nil {
		return settings{}, err
	}
	scratchMax, err := byteLimit(getenv, "TAG_FILES_SCRATCH_MAX_BYTES", 64*1024*1024*1024)
	if err != nil {
		return settings{}, err
	}
	minimumFree, err := byteLimit(getenv, "TAG_FILES_MIN_FREE_BYTES", 1024*1024*1024)
	if err != nil {
		return settings{}, err
	}
	p := media.DefaultConfig()
	p.MaxOutputBytes, err = byteLimit(getenv, "TAG_FILES_MAX_OUTPUT_BYTES", p.MaxOutputBytes)
	if err != nil {
		return settings{}, err
	}
	return settings{listen: env(getenv, "TAG_FILES_LISTEN", ":8080"), app: application.Config{
		DataDir: env(getenv, "TAG_FILES_DATA_DIR", "/data"),
		WebDir:  getenv("TAG_FILES_WEB_DIR"),
		Scratch: scratch.Config{Root: env(getenv, "TAG_FILES_SCRATCH_DIR", "/scratch"), MaxBytes: scratchMax, MinFreeBytes: minimumFree},
		Storage: objectstore.Config{Endpoint: env(getenv, "TAG_FILES_S3_ENDPOINT", "http://storage:8333"), Region: "us-east-1", Bucket: "tag-files", AccessKey: access, SecretKey: secret},
		Media:   p, Version: version, MinimumFreeBytes: minimumFree,
	}}, nil
}

func health(ctx context.Context, address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid application listen address")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	request, err := http.NewRequestWithContext(ctx, "GET", "http://"+net.JoinHostPort(host, port)+"/health/ready", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("application readiness check failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("application is not ready (HTTP %d)", response.StatusCode)
	}
	return nil
}

func openApplication(ctx context.Context, config application.Config, open func(context.Context, application.Config) (*application.App, error)) (*application.App, error) {
	delay := 100 * time.Millisecond
	for {
		app, err := open(ctx, config)
		if err == nil {
			return app, nil
		}
		var network net.Error
		if !errors.As(err, &network) {
			return nil, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

func run(ctx context.Context, getenv func(string) string, args []string) error {
	if len(args) == 1 && args[0] == "check" {
		return health(ctx, env(getenv, "TAG_FILES_LISTEN", ":8080"))
	}
	if len(args) != 0 {
		return fmt.Errorf("usage: tag-files [check]")
	}
	config, err := settingsFrom(getenv)
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 90*time.Second)
	app, err := openApplication(startup, config.app, application.Open)
	cancel()
	if err != nil {
		return err
	}
	defer app.Close()
	listener, err := net.Listen("tcp", config.listen)
	if err != nil {
		return err
	}
	log.Printf("Tag Files %s listening on %s", version, listener.Addr())
	return app.Serve(ctx, listener)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Getenv, os.Args[1:]); err != nil && !(ctx.Err() != nil && errors.Is(err, context.Canceled)) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
