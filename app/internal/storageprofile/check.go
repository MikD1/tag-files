package storageprofile

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const MaxConfigBytes = 1024 * 1024

type Options struct {
	DataDir, FilerConfig, S3Config string
	// Only the supervisor probes the backend before opening the S3 gateway.
	s3Endpoint string
}

func (o Options) endpoint() string {
	if o.s3Endpoint != "" {
		return o.s3Endpoint
	}
	return "http://127.0.0.1:8333"
}

func readConfig(path string) ([]byte, error) {
	// O_NONBLOCK prevents even opening a misconfigured FIFO from hanging the
	// health check. Only regular files are part of this deployment profile.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("storage configuration must be a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if len(b) > MaxConfigBytes {
		return nil, fmt.Errorf("storage configuration is too large")
	}
	return b, err
}

func checkLayout(root string) error {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	for _, sub := range []string{"master", "filerldb2"} {
		path := filepath.Join(root, sub)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		actual, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(resolved, actual)
		if err != nil || !info.IsDir() || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("storage metadata must remain inside the complete data volume")
		}
	}
	return nil
}

type boundedOutput struct {
	buffer bytes.Buffer
	max    int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.max-b.buffer.Len() {
		return 0, fmt.Errorf("storage control output exceeded bound")
	}
	return b.buffer.Write(p)
}

func (b *boundedOutput) Bytes() []byte { return b.buffer.Bytes() }

func persistedRules(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "weed", "shell", "-master=127.0.0.1:9333", "-filer=127.0.0.1:8888", "-connectTimeout=3s")
	cmd.Stdin = strings.NewReader("fs.configure\n")
	out, diagnostic := &boundedOutput{max: MaxConfigBytes}, &boundedOutput{max: 64 * 1024}
	cmd.Stdout, cmd.Stderr = out, diagnostic
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cannot read live filer configuration: %w", err)
	}
	return out.Bytes(), nil
}

// Check is read-only. Startup/orchestration must gate uploads on its success;
// this check never repairs an unsafe existing library or changes its rules.
func Check(ctx context.Context, options Options) error {
	if err := checkLayout(options.DataDir); err != nil {
		return fmt.Errorf("storage layout: %w", err)
	}
	backend, err := readConfig(options.FilerConfig)
	if err != nil {
		return fmt.Errorf("filer backend: %w", err)
	}
	if err := ValidateBackend(backend); err != nil {
		return err
	}
	data, err := readConfig(options.S3Config)
	if err != nil {
		return fmt.Errorf("private S3 profile: %w", err)
	}
	credentials, err := PrivateCredentials(data)
	if err != nil {
		return err
	}
	rules, err := persistedRules(ctx)
	if err != nil {
		return err
	}
	if err := ValidateRules(rules); err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, signed := range []bool{false, true} {
		req, err := http.NewRequestWithContext(ctx, "HEAD", options.endpoint()+"/tag-files", nil)
		if err != nil {
			return err
		}
		if signed {
			if err := v4.NewSigner().SignHTTP(ctx, credentials, req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "s3", "us-east-1", time.Now()); err != nil {
				return fmt.Errorf("cannot sign storage probe")
			}
		}
		res, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("private S3 probe failed: %w", err)
		}
		res.Body.Close()
		expected := http.StatusForbidden
		if signed {
			expected = http.StatusOK
		}
		if res.StatusCode != expected {
			return fmt.Errorf("private S3 probe returned HTTP %d (signed=%t)", res.StatusCode, signed)
		}
	}
	return checkBucketAccess(ctx, client, credentials, options.endpoint())
}
