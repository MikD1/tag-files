package storageprofile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const backendS3Endpoint = "http://127.0.0.1:8334"

// Ready includes the durable installation marker; Check alone is also useful
// for diagnosing a storage profile before it has been published as ready.
func Ready(ctx context.Context, options Options) error {
	state, err := LoadState(options.DataDir)
	if err != nil {
		return err
	}
	if state.Phase != "ready" {
		return fmt.Errorf("storage initialization is not yet complete")
	}
	data, err := readConfig(options.S3Config)
	if err != nil {
		return err
	}
	if digest(data) != state.ConfigSHA256 {
		return ErrIncompatibleStorage
	}
	return Check(ctx, options)
}

type backend struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
}

func startBackend(options Options, s3 bool, uid, gid int) (*backend, error) {
	args := []string{"mini", "-dir=" + options.DataDir, "-master.dir=" + filepath.Join(options.DataDir, "master"),
		"-ip=127.0.0.1", "-ip.bind=127.0.0.1", "-s3.port=8334", fmt.Sprintf("-s3=%t", s3), "-s3.config=" + options.S3Config,
		"-bucket=", "-tableBucket=", "-s3.autoCreateBucket=false", "-s3.iam.readOnly=true", "-webdav=false",
		"-s3.port.iceberg=0", "-s3.port.lance=0",
		"-admin.ui=false", "-master.telemetry=false", "-master.defaultReplication=000", "-filer.defaultReplicaPlacement=000", "-volume.max=0"}
	cmd := exec.Command("weed", args...)
	cmd.Dir = options.DataDir
	// In particular, S3_BUCKET/S3_TABLE_BUCKET must not create buckets before
	// fsync is persisted. mini receives only the owned, explicit arguments.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	b := &backend{command: cmd, done: make(chan struct{})}
	go func() { b.err = cmd.Wait(); close(b.done) }()
	return b, nil
}

func (b *backend) stop() {
	// Keep the complete-volume ownership lock until the child is reaped.
	select {
	case <-b.done:
		return
	default:
	}
	_ = syscall.Kill(-b.command.Process.Pid, syscall.SIGTERM)
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-b.done:
		return
	case <-timer.C:
	}
	_ = syscall.Kill(-b.command.Process.Pid, syscall.SIGKILL)
	<-b.done
}

func (b *backend) pause(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return fmt.Errorf("storage backend stopped during startup: %v", b.err)
	case <-timer.C:
		return nil
	}
}

func applyGlobalFsync(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "weed", "shell", "-master=127.0.0.1:9333", "-filer=127.0.0.1:8888", "-connectTimeout=3s")
	cmd.Stdin = strings.NewReader("fs.configure -locationPrefix=/ -fsync=true -apply\n")
	cmd.Stdout = &boundedOutput{max: MaxConfigBytes}
	cmd.Stderr = &boundedOutput{max: 64 * 1024}
	cmd.WaitDelay = time.Second
	return cmd.Run()
}

func waitRules(ctx context.Context, b *backend, initialize bool) error {
	for {
		rules, err := persistedRules(ctx)
		if err == nil {
			// An empty rule set belongs only to an interrupted/new owned init.
			// Refuse existing unsafe rules rather than repairing their intent.
			var empty struct {
				Version   int               `json:"version"`
				Locations []json.RawMessage `json:"locations"`
			}
			if initialize && json.Unmarshal(rules, &empty) == nil && (empty.Version == 0 || empty.Version == 2) && len(empty.Locations) == 0 {
				if err := applyGlobalFsync(ctx); err != nil {
					return fmt.Errorf("persist global fsync: %w", err)
				}
				rules, err = persistedRules(ctx)
			}
			if err != nil {
				return err
			}
			if err := ValidateRules(rules); err != nil {
				return err
			}
			return nil
		}
		if err := b.pause(ctx); err != nil {
			return err
		}
	}
}

func signedRequest(ctx context.Context, client *http.Client, credentials aws.Credentials, method, query string, body []byte) (int, []byte, error) {
	url := backendS3Endpoint + "/tag-files"
	if query != "" {
		url += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	sum := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if err := v4.NewSigner().SignHTTP(ctx, credentials, req, payloadHash, "s3", "us-east-1", time.Now()); err != nil {
		return 0, nil, fmt.Errorf("cannot sign bootstrap request")
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxConfigBytes+1))
	if len(data) > MaxConfigBytes {
		return 0, nil, fmt.Errorf("bootstrap response exceeds bound")
	}
	return res.StatusCode, data, err
}

func waitBucket(ctx context.Context, b *backend, credentials aws.Credentials, initialize bool) error {
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		status, _, err := signedRequest(ctx, client, credentials, "HEAD", "", nil)
		if err == nil {
			if status == http.StatusNotFound && initialize {
				var data []byte
				status, data, err = signedRequest(ctx, client, credentials, "PUT", "", nil)
				if err != nil || status != http.StatusOK {
					return bootstrapError("create owned private bucket", status, data, err)
				}
				fmt.Fprintln(os.Stdout, "tag-files: private bucket created after verified fsync")
			} else if status != http.StatusOK {
				return fmt.Errorf("owned bucket unavailable (HTTP %d)", status)
			}
			status, data, err := accessDocument(ctx, client, credentials, backendS3Endpoint, "publicAccessBlock")
			if err != nil {
				return err
			}
			if status == http.StatusNotFound && initialize {
				var missing struct {
					Code string `xml:"Code"`
				}
				if decodeXML(data, &missing) != nil || missing.Code != "NoSuchPublicAccessBlockConfiguration" {
					return fmt.Errorf("unexpected absent public-access-block response")
				}
				body := []byte(`<PublicAccessBlockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><BlockPublicAcls>true</BlockPublicAcls><IgnorePublicAcls>true</IgnorePublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets></PublicAccessBlockConfiguration>`)
				status, data, err = signedRequest(ctx, client, credentials, "PUT", "publicAccessBlock", body)
				if err != nil || status != http.StatusOK {
					return bootstrapError("initialize public access block", status, data, err)
				}
			} else if status != http.StatusOK {
				return fmt.Errorf("public access block unavailable (HTTP %d)", status)
			} else if err := ValidatePublicAccessBlock(data); err != nil {
				return err
			}
			return nil
		}
		if err := b.pause(ctx); err != nil {
			return err
		}
	}
}

func bootstrapError(action string, status int, body []byte, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	var failure struct {
		Code string `xml:"Code"`
	}
	if decodeXML(body, &failure) != nil || len(failure.Code) > 128 {
		failure.Code = "invalid error response"
	}
	return fmt.Errorf("%s: HTTP %d (%s)", action, status, failure.Code)
}

// Run is the PID 1 supervisor in the owned storage image. It starts without S3,
// persists/verifies fsync, reopens the backend with S3, then initializes the
// private bucket and publishes readiness. Existing profiles are never healed.
func Run(ctx context.Context, options Options, credentials aws.Credentials, uid, gid int) error {
	options.s3Endpoint = backendS3Endpoint
	backendConfig, err := readConfig(options.FilerConfig)
	if err != nil {
		return err
	}
	if err := ValidateBackend(backendConfig); err != nil {
		return err
	}
	p, err := Prepare(options.DataDir, credentials, uid, gid)
	if err != nil {
		return err
	}
	defer p.Close()
	controlConfig, err := readConfig(options.S3Config)
	if err != nil {
		return err
	}
	control, err := PrivateCredentials(controlConfig)
	if err != nil {
		return err
	}
	initialize := p.State.Phase == "initializing"
	startup, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	b, err := startBackend(options, false, uid, gid)
	if err != nil {
		return err
	}
	defer func() { b.stop() }()
	if err := waitRules(startup, b, initialize); err != nil {
		return fmt.Errorf("storage fsync preflight: %w", err)
	}
	fmt.Fprintln(os.Stdout, "tag-files: persisted global fsync verified; S3 remains disabled")
	b.stop()
	next, err := startBackend(options, true, uid, gid)
	if err != nil {
		return err
	}
	b = next
	if err := waitRules(startup, b, false); err != nil {
		return err
	}
	if err := waitBucket(startup, b, control, initialize); err != nil {
		return err
	}
	if err := Check(startup, options); err != nil {
		return err
	}
	if initialize {
		if err := p.RecordReady(); err != nil {
			return err
		}
	}
	gateway, err := openGateway()
	if err != nil {
		return err
	}
	defer gateway.close()
	fmt.Fprintln(os.Stdout, "tag-files: owned private storage ready")
	cancel()
	select {
	case <-ctx.Done():
		return nil
	case <-b.done:
		return fmt.Errorf("storage backend stopped: %v", b.err)
	case err := <-gateway.done:
		return fmt.Errorf("storage S3 gateway stopped: %w", err)
	}
}
