package storageprofile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type proofRoundTrip func(*http.Request) (*http.Response, error)

func (f proofRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMultipartProofBadRetryCannotListHistoricalSHA(t *testing.T) {
	data := []byte("actual proven source bytes")
	sum := proofSHA(data)
	var stored []byte
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if stored == nil {
				io.WriteString(w, `<ListPartsResult/>`)
			} else {
				fmt.Fprintf(w, `<ListPartsResult><Part><PartNumber>1</PartNumber><Size>%d</Size><ETag>opaque</ETag></Part></ListPartsResult>`, len(stored))
			}
			return
		}
		stored, _ = io.ReadAll(r.Body)
		// This backend deliberately echoes the declared checksum and reuses an
		// opaque ETag. Only the gateway's actual body proof can authorize SHA.
		w.Header().Set("ETag", "opaque")
		w.Header().Set("X-Amz-Checksum-Sha256", sum)
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	p, err := newMultipartProof(t.TempDir(), "storage-id", "epoch", appCredentials(), target, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method string, body []byte) *httptest.ResponseRecorder {
		query, checksum := "uploadId=mpu", ""
		if method == "PUT" {
			query += "&partNumber=1"
			checksum = sum
		}
		w := httptest.NewRecorder()
		p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, method, query, body, checksum, appCredentials()))
		return w
	}
	if w := call("PUT", data); w.Code != 200 {
		t.Fatalf("initial part: %d %s", w.Code, w.Body.String())
	}
	wrong := bytes.Repeat([]byte("!"), len(data))
	if w := call("PUT", wrong); w.Code < 400 {
		t.Fatalf("bad retry accepted: %d", w.Code)
	}
	if w := call("GET", nil); w.Code < 400 {
		t.Fatalf("historical SHA certified changed bytes: actual=%s declared=%s response=%s", proofSHA(stored), sum, w.Body.String())
	}
}

func TestMultipartProofOnlyCertifiedBadDigestSettlesRejectedWrite(t *testing.T) {
	for _, fixture := range []struct {
		name, code string
		status     int
		settled    bool
	}{
		{"native_bad_digest", "BadDigest", 400, true},
		{"generic_bad_request", "InvalidRequest", 400, false},
		{"server_error", "InternalError", 500, false},
		{"wrong_status_bad_digest", "BadDigest", 500, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			data := []byte("independent source bytes")
			sum := proofSHA(data)
			stored := false
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					if stored {
						fmt.Fprintf(w, `<ListPartsResult><Part><PartNumber>1</PartNumber><Size>%d</Size><ETag>opaque</ETag></Part></ListPartsResult>`, len(data))
					} else {
						io.WriteString(w, `<ListPartsResult/>`)
					}
					return
				}
				body, _ := io.ReadAll(r.Body)
				if !bytes.Equal(body, data) {
					w.WriteHeader(fixture.status)
					fmt.Fprintf(w, `<Error><Code>%s</Code></Error>`, fixture.code)
					return
				}
				stored = true
				w.Header().Set("ETag", "opaque")
				w.Header().Set("X-Amz-Checksum-Sha256", sum)
			}))
			defer backend.Close()
			target, _ := url.Parse(backend.URL)
			p, err := newMultipartProof(t.TempDir(), "storage-id", "epoch", appCredentials(), target, http.DefaultTransport)
			if err != nil {
				t.Fatal(err)
			}
			call := func(method string, body []byte) *httptest.ResponseRecorder {
				query, checksum := "uploadId=mpu", ""
				if method == "PUT" {
					query += "&partNumber=1"
					checksum = sum
				}
				w := httptest.NewRecorder()
				p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, method, query, body, checksum, appCredentials()))
				return w
			}
			if w := call("PUT", data); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := call("PUT", bytes.Repeat([]byte("!"), len(data))); w.Code != fixture.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := call("GET", nil); (w.Code == 200) != fixture.settled {
				t.Fatalf("rejected write settlement=%v, list=%d %s", fixture.settled, w.Code, w.Body.String())
			}
			if w := call("PUT", data); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := call("GET", nil); (w.Code == 200) != fixture.settled {
				t.Fatalf("later ACK erased prior uncertainty=%v, list=%d %s", !fixture.settled, w.Code, w.Body.String())
			}
		})
	}
}

func TestMultipartProofLaterACKCannotResolveEarlierBackendWriter(t *testing.T) {
	data := []byte("actual proven source bytes")
	sum := proofSHA(data)
	wrong := bytes.Repeat([]byte("!"), len(data))
	started, release, ended := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var stored []byte
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			mu.Lock()
			defer mu.Unlock()
			if stored == nil {
				io.WriteString(w, `<ListPartsResult/>`)
			} else {
				fmt.Fprintf(w, `<ListPartsResult><Part><PartNumber>1</PartNumber><Size>%d</Size><ETag>opaque</ETag></Part></ListPartsResult>`, len(stored))
			}
			return
		}
		body, _ := io.ReadAll(r.Body)
		if bytes.Equal(body, wrong) {
			close(started)
			<-release
		}
		mu.Lock()
		stored = body
		mu.Unlock()
		w.Header().Set("ETag", "opaque")
		w.Header().Set("X-Amz-Checksum-Sha256", sum)
	}))
	defer backend.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	puts := 0
	transport := proofRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == "PUT" {
			puts++
			if puts == 2 {
				// A canceled RoundTrip does not prove the backend handler stopped.
				// Keep the real HTTP write alive after returning the lost ACK.
				pending := r.Clone(context.WithoutCancel(r.Context()))
				go func() {
					defer close(ended)
					res, e := http.DefaultTransport.RoundTrip(pending)
					if e == nil {
						io.Copy(io.Discard, res.Body)
						res.Body.Close()
					}
				}()
				<-started
				return nil, errors.New("lost response while backend writer remains active")
			}
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	target, _ := url.Parse(backend.URL)
	root := t.TempDir()
	p, err := newMultipartProof(root, "storage-id", "first", appCredentials(), target, transport)
	if err != nil {
		t.Fatal(err)
	}
	call := func(p *multipartProof, method string, body []byte) *httptest.ResponseRecorder {
		query, checksum := "uploadId=mpu", ""
		if method == "PUT" {
			query += "&partNumber=1"
			checksum = sum
		}
		w := httptest.NewRecorder()
		p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, method, query, body, checksum, appCredentials()))
		return w
	}
	if w := call(p, "PUT", data); w.Code != 200 {
		t.Fatalf("initial: %d %s", w.Code, w.Body.String())
	}
	if w := call(p, "PUT", wrong); w.Code < 400 {
		t.Fatal("lost write acknowledged")
	}
	if w := call(p, "PUT", data); w.Code != 200 {
		t.Fatalf("later valid ACK: %d %s", w.Code, w.Body.String())
	}
	if w := call(p, "GET", nil); w.Code < 400 {
		t.Errorf("later ACK falsely resolved older live writer: %s", w.Body.String())
	}
	close(release)
	<-ended
	mu.Lock()
	if !bytes.Equal(stored, wrong) {
		t.Error("late backend writer did not replace current bytes")
	}
	mu.Unlock()
	if w := call(p, "GET", nil); w.Code < 400 {
		t.Errorf("late wrong body certified: %s", w.Body.String())
	}
	// A new backend epoch proves the old writer stopped, not which bytes won.
	// An actual resend must establish current bytes before SHA can reappear.
	p, err = newMultipartProof(root, "storage-id", "second", appCredentials(), target, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(p, "GET", nil); w.Code < 400 {
		t.Errorf("epoch barrier resurrected stale proof: %s", w.Body.String())
	}
	if w := call(p, "PUT", data); w.Code != 200 {
		t.Fatalf("reprove: %d %s", w.Code, w.Body.String())
	}
	if w := call(p, "GET", nil); w.Code != 200 || !strings.Contains(w.Body.String(), sum) {
		t.Fatalf("actual reproof unavailable: %d %s", w.Code, w.Body.String())
	}
}

func TestMultipartProofRetryResyncsPreviouslyFailedDirectoryCreation(t *testing.T) {
	data := []byte("independent source bytes")
	sum := proofSHA(data)
	var trace []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `<ListPartsResult/>`)
			return
		}
		trace = append(trace, "backend PUT")
		io.Copy(io.Discard, r.Body)
		w.Header().Set("ETag", "opaque")
		w.Header().Set("X-Amz-Checksum-Sha256", sum)
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	p, err := newMultipartProof(t.TempDir(), "storage-id", "epoch", appCredentials(), target, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	p.store.directorySync = func(path string) error {
		trace = append(trace, path)
		if path == p.store.root && !failed {
			failed = true
			return errors.New("directory creation parent fsync failed")
		}
		return syncProofDir(path)
	}
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, "PUT", "uploadId=mpu&partNumber=1", data, sum, appCredentials()))
		return w
	}
	if w := call(); w.Code < 400 || !failed {
		t.Fatalf("creation fault not injected: %d %v", w.Code, failed)
	}
	trace = nil
	if w := call(); w.Code != 200 {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	syncedParent := false
	for _, event := range trace {
		if event == p.store.root {
			syncedParent = true
		}
		if event == "backend PUT" && !syncedParent {
			t.Fatalf("backend dispatched before retry re-established parent directory durability: %v", trace)
		}
	}
	if !syncedParent {
		t.Fatalf("retry ACK omitted failed creation parent fsync: %v", trace)
	}
}

func TestMultipartProofRootCreationRetryResyncsItsParent(t *testing.T) {
	root := t.TempDir()
	s := &proofStore{root: filepath.Join(root, proofDirectory), storageID: "storage-id"}
	failed := false
	s.directorySync = func(path string) error {
		if path == root && !failed {
			failed = true
			return errors.New("proof-root parent fsync failed")
		}
		return syncProofDir(path)
	}
	if err := s.proofDir(s.root); err == nil || !failed {
		t.Fatalf("root creation fault not injected: %v", err)
	}
	var trace []string
	s.directorySync = func(path string) error { trace = append(trace, path); return syncProofDir(path) }
	if err := s.proofDir(s.root); err != nil {
		t.Fatal(err)
	}
	if len(trace) != 1 || trace[0] != root {
		t.Fatalf("existing proof root omitted retry parent fsync: %v", trace)
	}
}

func TestMultipartProofListingRequiresDurableDirectoryChain(t *testing.T) {
	data := []byte("independent source bytes")
	sum := proofSHA(data)
	stored := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if !stored {
				io.WriteString(w, `<ListPartsResult/>`)
			} else {
				fmt.Fprintf(w, `<ListPartsResult><Part><PartNumber>1</PartNumber><Size>%d</Size><ETag>opaque</ETag></Part></ListPartsResult>`, len(data))
			}
			return
		}
		io.Copy(io.Discard, r.Body)
		stored = true
		w.Header().Set("ETag", "opaque")
		w.Header().Set("X-Amz-Checksum-Sha256", sum)
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	root := t.TempDir()
	p, err := newMultipartProof(root, "storage-id", "epoch", appCredentials(), target, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, "PUT", "uploadId=mpu&partNumber=1", data, sum, appCredentials()))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, failedPath := range []string{root, p.store.root} {
		p.store.directorySync = func(path string) error {
			if path == failedPath {
				return errors.New("parent chain fsync failed")
			}
			return syncProofDir(path)
		}
		w = httptest.NewRecorder()
		p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, "GET", "uploadId=mpu", nil, "", appCredentials()))
		if w.Code < 400 {
			t.Errorf("ListParts certified proof with failed parent chain fsync %s: %s", failedPath, w.Body.String())
		}
	}
}

func TestMultipartProofLostTerminalACKRetainsFenceUntilNewBackendEpoch(t *testing.T) {
	data := []byte("source")
	sum := proofSHA(data)
	var mu sync.Mutex
	active := true
	lost := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "GET":
			if !active {
				w.WriteHeader(404)
				io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
				return
			}
			io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated></ListPartsResult>`)
		case "PUT":
			io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", "opaque")
			w.Header().Set("X-Amz-Checksum-Sha256", sum)
		case "POST", "DELETE":
			active = false
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(200)
		}
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	root := t.TempDir()
	transport := proofRoundTrip(func(r *http.Request) (*http.Response, error) {
		res, err := http.DefaultTransport.RoundTrip(r)
		if err == nil && r.Method == "POST" && !lost {
			lost = true
			res.Body.Close()
			return nil, errors.New("lost backend terminal ACK")
		}
		return res, err
	})
	p, err := newMultipartProof(root, "storage-id", "first", appCredentials(), target, transport)
	if err != nil {
		t.Fatal(err)
	}
	call := func(p *multipartProof, method string, body []byte, sum string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		query := "uploadId=mpu"
		if method == "PUT" {
			query += "&partNumber=1"
		}
		p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, method, query, body, sum, appCredentials()))
		return w
	}
	if w := call(p, "PUT", data, sum); w.Code != 200 {
		t.Fatalf("part %d %s", w.Code, w.Body.String())
	}
	if w := call(p, "POST", []byte("<CompleteMultipartUpload/>"), ""); w.Code < 400 {
		t.Fatal("lost terminal ACK was accepted")
	}
	if w := call(p, "DELETE", nil, ""); w.Code != 200 {
		t.Fatalf("abort %d %s", w.Code, w.Body.String())
	}
	tuple := proofTuple{"tag-files", "incoming/test 猫", "mpu"}
	if _, err := p.store.upload(tuple); err != nil {
		t.Fatalf("same-epoch unknown writer fence was erased: %v", err)
	}
	if w := call(p, "PUT", data, sum); w.Code != 409 {
		t.Fatalf("closed MPU reopened: %d", w.Code)
	}
	if _, err = newMultipartProof(root, "storage-id", "second", appCredentials(), target, http.DefaultTransport); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(p.store.uploadPath(tuple)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("proven absent MPU leaked after epoch barrier: %v", err)
	}
}

func TestMultipartProofTerminalFenceIsDurableBeforeWaitingForActivePart(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	data := []byte("source")
	sum := proofSHA(data)
	var mu sync.Mutex
	active := true
	terminal := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			io.Copy(io.Discard, r.Body)
			close(started)
			<-release
			w.Header().Set("ETag", "opaque")
			w.Header().Set("X-Amz-Checksum-Sha256", sum)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" {
			terminal = true
			active = false
			io.Copy(io.Discard, r.Body)
			return
		}
		if !active {
			w.WriteHeader(404)
			io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			return
		}
		io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated></ListPartsResult>`)
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	p, err := newMultipartProof(t.TempDir(), "storage-id", "epoch", appCredentials(), target, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	partDone, completeDone := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, "PUT", "uploadId=mpu&partNumber=1", data, sum, appCredentials()))
		partDone <- w
	}()
	<-started
	go func() {
		w := httptest.NewRecorder()
		p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, "POST", "uploadId=mpu", []byte("<CompleteMultipartUpload/>"), "", appCredentials()))
		completeDone <- w
	}()
	tuple := proofTuple{"tag-files", "incoming/test 猫", "mpu"}
	deadline := time.Now().Add(3 * time.Second)
	for {
		u, e := p.store.upload(tuple)
		if e == nil && u.Closed {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("terminal admission fence was not persisted")
		}
		time.Sleep(time.Millisecond)
	}
	w := httptest.NewRecorder()
	p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, "PUT", "uploadId=mpu&partNumber=2", data, sum, appCredentials()))
	mu.Lock()
	forwarded := terminal
	mu.Unlock()
	if w.Code != 409 || forwarded {
		close(release)
		t.Fatalf("new part entered drain: status=%d terminal=%v", w.Code, forwarded)
	}
	close(release)
	if w := <-partDone; w.Code != 200 {
		t.Fatalf("active part failed: %d %s", w.Code, w.Body.String())
	}
	if w := <-completeDone; w.Code != 200 {
		t.Fatalf("terminal failed: %d %s", w.Code, w.Body.String())
	}
}

func TestMultipartProofRejectsCorruptSymlinkAndForeignPartRecords(t *testing.T) {
	for _, mode := range []string{"corrupt", "symlink", "foreign", "missing_upload_identity"} {
		t.Run(mode, func(t *testing.T) {
			s, err := openProofStore(t.TempDir(), "storage-id")
			if err != nil {
				t.Fatal(err)
			}
			tuple := proofTuple{"tag-files", "key", "mpu"}
			if err = s.saveUpload(proofUpload{Version: 1, StorageID: "storage-id", Tuple: tuple, Epoch: "epoch"}); err != nil {
				t.Fatal(err)
			}
			part := proofPart{Version: 1, Number: 1, Size: 1, SHA256: proofSHA([]byte("a")), Validated: true, ETag: "opaque"}
			if err = s.savePart(tuple, part); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.uploadPath(tuple), "1.json")
			switch mode {
			case "missing_upload_identity":
				if err = os.Remove(filepath.Join(s.uploadPath(tuple), "upload.json")); err != nil {
					t.Fatal(err)
				}
				if _, err = s.upload(tuple); errors.Is(err, os.ErrNotExist) || err == nil {
					t.Fatal("nonempty unowned journal could be silently adopted")
				}
				return
			case "corrupt":
				os.WriteFile(path, []byte(`{"record":{}}`), 0600)
			case "symlink":
				os.Remove(path)
				os.Symlink(filepath.Join(t.TempDir(), "unrelated"), path)
			case "foreign":
				other := proofTuple{"tag-files", "other-key", "other-mpu"}
				s.saveUpload(proofUpload{Version: 1, StorageID: "storage-id", Tuple: other, Epoch: "epoch"})
				s.savePart(other, part)
				b, e := os.ReadFile(filepath.Join(s.uploadPath(other), "1.json"))
				if e != nil {
					t.Fatal(e)
				}
				os.WriteFile(path, b, 0600)
			}
			if _, err = s.part(tuple, 1); err == nil {
				t.Fatal("invalid proof was adopted")
			}
		})
	}
}

func TestMultipartProofListingPreservesPagingAndRejectsMissingProof(t *testing.T) {
	sha := proofSHA([]byte("independent"))
	body := []byte(`<ListPartsResult><PartNumberMarker>1000</PartNumberMarker><NextPartNumberMarker>1001</NextPartNumberMarker><IsTruncated>true</IsTruncated><Part><PartNumber>1001</PartNumber><Size>11</Size><ETag>opaque</ETag><LastModified>2026-11-15T01:02:03Z</LastModified></Part></ListPartsResult>`)
	b, err := enrichProofPage(body, []string{sha})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"<PartNumberMarker>1000</PartNumberMarker>", "<NextPartNumberMarker>1001</NextPartNumberMarker>", "<IsTruncated>true</IsTruncated>", "<LastModified>2026-11-15T01:02:03Z</LastModified>", "<ChecksumSHA256>" + sha + "</ChecksumSHA256>"} {
		if !strings.Contains(string(b), field) {
			t.Errorf("paging metadata lost: %s", field)
		}
	}
	if _, err = enrichProofPage(body, nil); err == nil {
		t.Fatal("part fabricated without proof")
	}
}

func TestMultipartProofDoesNotAcknowledgeUnsyncedRenameOrEchoedHeader(t *testing.T) {
	for _, mode := range []string{"directory_sync_failure", "header_echo_without_body_validation"} {
		t.Run(mode, func(t *testing.T) {
			data := []byte("independent actual bytes")
			sum := proofSHA(data)
			var stored bool
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					if !stored {
						io.WriteString(w, `<ListPartsResult/>`)
					} else {
						fmt.Fprintf(w, `<ListPartsResult><Part><PartNumber>1</PartNumber><Size>%d</Size><ETag>opaque</ETag></Part></ListPartsResult>`, len(data))
					}
					return
				}
				io.Copy(io.Discard, r.Body)
				stored = true
				w.Header().Set("ETag", "opaque")
				w.Header().Set("X-Amz-Checksum-Sha256", sum)
			}))
			defer backend.Close()
			target, _ := url.Parse(backend.URL)
			p, err := newMultipartProof(t.TempDir(), "storage-id", "epoch", appCredentials(), target, http.DefaultTransport)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "directory_sync_failure" {
				p.store.fail = func(stage string) error {
					if stage == "before-directory-sync" {
						part, e := p.store.part(proofTuple{"tag-files", "incoming/test 猫", "mpu"}, 1)
						if e == nil && part.Validated {
							return errors.New("injected directory fsync failure")
						}
					}
					return nil
				}
			} else {
				data = bytes.Repeat([]byte("!"), len(data))
			}
			w := httptest.NewRecorder()
			p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, "PUT", "uploadId=mpu&partNumber=1", data, sum, appCredentials()))
			if w.Code < 400 {
				t.Fatalf("unproven part acknowledged: %d", w.Code)
			}
			w = httptest.NewRecorder()
			p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, "GET", "uploadId=mpu", nil, "", appCredentials()))
			if w.Code < 400 {
				t.Fatalf("unproven/unsynced bytes became ListParts SHA: %d %s", w.Code, w.Body.String())
			}
			if mode == "directory_sync_failure" {
				p.store.fail = func(stage string) error {
					if stage == "before-proof-file-sync" {
						return errors.New("injected proof file resync failure")
					}
					return nil
				}
				w = httptest.NewRecorder()
				p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, "GET", "uploadId=mpu", nil, "", appCredentials()))
				if w.Code < 400 {
					t.Fatalf("file resync failure acknowledged proof: %d", w.Code)
				}
				p.store.fail = nil
				w = httptest.NewRecorder()
				p.wrap(http.NotFoundHandler()).ServeHTTP(w, proofRequest(t, "GET", "uploadId=mpu", nil, "", appCredentials()))
				if w.Code != 200 || !strings.Contains(w.Body.String(), sum) {
					t.Fatalf("durably resynced rename did not recover: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}
