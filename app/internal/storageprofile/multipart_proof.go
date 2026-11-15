package storageprofile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const proofPageLimit = 8 << 20

type multipartProof struct {
	store       *proofStore
	epoch       string
	credentials aws.Credentials
	target      *url.URL
	client      *http.Client
	mu          sync.Mutex
	actors      map[string]*proofActor
}
type proofActor struct {
	mu            sync.Mutex
	terminal      sync.Mutex
	writers, refs int
	changed       chan struct{}
	parts         map[int]*sync.Mutex
}
type proofListedPart struct {
	Number int    `xml:"PartNumber"`
	Size   int64  `xml:"Size"`
	ETag   string `xml:"ETag"`
	SHA256 string `xml:"ChecksumSHA256"`
}
type proofList struct {
	XMLName xml.Name
	Parts   []proofListedPart `xml:"Part"`
}

func newMultipartProof(root, storageID, epoch string, credentials aws.Credentials, target *url.URL, transport http.RoundTripper) (*multipartProof, error) {
	s, err := openProofStore(root, storageID)
	if err != nil {
		return nil, err
	}
	p := &multipartProof{store: s, epoch: epoch, credentials: credentials, target: target, client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, actors: map[string]*proofActor{}}
	startup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = p.recover(startup); err != nil {
		return nil, err
	}
	return p, nil
}
func (p *multipartProof) actor(t proofTuple) (*proofActor, func()) {
	key := p.store.uploadPath(t)
	p.mu.Lock()
	a := p.actors[key]
	if a == nil {
		a = &proofActor{changed: make(chan struct{}), parts: map[int]*sync.Mutex{}}
		p.actors[key] = a
	}
	a.refs++
	p.mu.Unlock()
	return a, func() {
		p.mu.Lock()
		a.refs--
		if a.refs == 0 {
			delete(p.actors, key)
		}
		p.mu.Unlock()
	}
}
func proofError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<Error><Code>%s</Code><Message>Owned multipart checksum proof is unavailable or conflicts.</Message></Error>", code)
}
func (p *multipartProof) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			proofError(w, 400, "InvalidRequest")
			return
		}
		if !q.Has("uploadId") {
			next.ServeHTTP(w, r)
			return
		}
		id := q.Get("uploadId")
		key := strings.TrimPrefix(r.URL.Path, "/tag-files/")
		if len(q["uploadId"]) != 1 || id == "" || len(id) > 4096 || strings.ContainsAny(id, "\x00\r\n") || key == r.URL.Path || key == "" || len(key) > 1024 {
			proofError(w, 400, "InvalidRequest")
			return
		}
		upload := r.Method == "PUT" && q.Has("partNumber")
		if !authenticateMultipart(r, p.credentials, upload) {
			proofError(w, 403, "SignatureDoesNotMatch")
			return
		}
		t := proofTuple{"tag-files", key, id}
		a, release := p.actor(t)
		defer release()
		switch {
		case upload:
			n, e := strconv.Atoi(q.Get("partNumber"))
			if e != nil || n < 1 || n > 10000 || strconv.Itoa(n) != q.Get("partNumber") || len(q["partNumber"]) != 1 || r.ContentLength < 1 || r.ContentLength > 5<<30 {
				proofError(w, 400, "InvalidRequest")
				return
			}
			p.upload(w, r, t, a, n)
		case r.Method == "GET":
			p.list(w, r, t, a)
		case r.Method == "POST" || r.Method == "DELETE":
			p.terminal(w, r, t, a)
		default:
			next.ServeHTTP(w, r)
		}
	})
}
func (p *multipartProof) forward(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.URL.Scheme = p.target.Scheme
	clone.URL.Host = p.target.Host
	clone.RequestURI = ""
	clone.GetBody = nil
	return p.client.Do(clone)
}
func copyProofResponse(w http.ResponseWriter, res *http.Response, body []byte) {
	for k, values := range res.Header {
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") || strings.EqualFold(k, "Connection") {
			continue
		}
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(body)
}
func proofResponseBody(res *http.Response, limit int64) ([]byte, error) {
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, fmt.Errorf("multipart response exceeds metadata bound: %v", e)
	}
	return b, nil
}

type proofBody struct {
	body io.ReadCloser
	mu   sync.Mutex
	n    int64
	h    hash.Hash
}

func (b *proofBody) Read(buf []byte) (int, error) {
	n, e := b.body.Read(buf)
	b.mu.Lock()
	b.n += int64(n)
	_, _ = b.h.Write(buf[:n])
	b.mu.Unlock()
	return n, e
}
func (b *proofBody) Close() error { return b.body.Close() }
func (b *proofBody) result() (int64, string, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sum := b.h.Sum(nil)
	return b.n, base64.StdEncoding.EncodeToString(sum), hex.EncodeToString(sum)
}

func (p *multipartProof) upload(w http.ResponseWriter, r *http.Request, t proofTuple, a *proofActor, n int) {
	a.mu.Lock()
	lock := a.parts[n]
	if lock == nil {
		lock = &sync.Mutex{}
		a.parts[n] = lock
	}
	a.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	a.mu.Lock()
	u, err := p.store.upload(t)
	if errors.Is(err, os.ErrNotExist) {
		u = proofUpload{Version: 1, StorageID: p.store.storageID, Tuple: t, Epoch: p.epoch}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		a.mu.Unlock()
		proofError(w, 503, "MultipartProofCorrupt")
		return
	}
	if u.Closed {
		a.mu.Unlock()
		proofError(w, 409, "MultipartAdmissionClosed")
		return
	}
	part, err := p.store.part(t, n)
	if errors.Is(err, os.ErrNotExist) {
		exists, listed, e := p.nativePart(r.Context(), t, n)
		if e != nil || !exists {
			a.mu.Unlock()
			proofError(w, 503, "MultipartProofUnavailable")
			return
		}
		if listed {
			a.mu.Unlock()
			proofError(w, 409, "MultipartChecksumUnavailable")
			return
		}
		if err = p.store.saveUpload(u); err != nil {
			a.mu.Unlock()
			proofError(w, 503, "MultipartProofUnavailable")
			return
		}
		part = proofPart{Version: 1, Number: n, Size: r.ContentLength, SHA256: r.Header.Get("X-Amz-Checksum-Sha256")}
	} else if err != nil {
		a.mu.Unlock()
		proofError(w, 503, "MultipartProofCorrupt")
		return
	}
	if part.Size != r.ContentLength || part.SHA256 != r.Header.Get("X-Amz-Checksum-Sha256") {
		a.mu.Unlock()
		proofError(w, 409, "MultipartBindingConflict")
		return
	}
	part.Uncertain = part.Uncertain || part.AttemptEpoch != ""
	part.AttemptEpoch = p.epoch
	if err = p.store.savePart(t, part); err != nil {
		a.mu.Unlock()
		proofError(w, 503, "MultipartProofUnavailable")
		return
	}
	a.writers++
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.writers--; close(a.changed); a.changed = make(chan struct{}); a.mu.Unlock() }()
	body := &proofBody{body: r.Body, h: sha256.New()}
	clone := r.Clone(r.Context())
	clone.Body = body
	res, err := p.forward(clone)
	if err != nil {
		proofError(w, 502, "MultipartWriteUncertain")
		return
	}
	response, err := proofResponseBody(res, 64<<10)
	if err != nil {
		proofError(w, 502, "MultipartWriteUncertain")
		return
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// The pinned native backend rejects BadDigest before replacing a part.
		// Only that exact negative acknowledgement settles this attempt; an
		// arbitrary error or a failed transport may leave a writer alive.
		var rejection struct {
			XMLName xml.Name
			Code    string `xml:"Code"`
		}
		if res.StatusCode == http.StatusBadRequest && xml.Unmarshal(response, &rejection) == nil && rejection.XMLName.Local == "Error" && rejection.Code == "BadDigest" {
			part.AttemptEpoch = ""
			if err = p.store.savePart(t, part); err != nil {
				proofError(w, 503, "MultipartProofUnavailable")
				return
			}
		}
		copyProofResponse(w, res, response)
		return
	}
	count, sum, hexSum := body.result()
	signedPayload := r.Header.Get("X-Amz-Content-Sha256")
	if count != part.Size || sum != part.SHA256 || (signedPayload != "UNSIGNED-PAYLOAD" && signedPayload != hexSum) || res.Header.Get("X-Amz-Checksum-Sha256") != sum || res.Header.Get("ETag") == "" || strings.ContainsAny(res.Header.Get("ETag"), "\x00\r\n") {
		proofError(w, 502, "MultipartBodyIntegrity")
		return
	}
	part.Validated = true
	part.ETag = res.Header.Get("ETag")
	part.AttemptEpoch = ""
	if err = p.store.savePart(t, part); err != nil {
		proofError(w, 503, "MultipartProofUnavailable")
		return
	}
	copyProofResponse(w, res, response)
}

func (p *multipartProof) list(w http.ResponseWriter, r *http.Request, t proofTuple, a *proofActor) {
	res, err := p.forward(r)
	if err != nil {
		proofError(w, 502, "MultipartBackendUnavailable")
		return
	}
	body, err := proofResponseBody(res, proofPageLimit)
	if err != nil {
		proofError(w, 502, "MultipartProofBoundExceeded")
		return
	}
	if res.StatusCode != 200 {
		copyProofResponse(w, res, body)
		return
	}
	var doc proofList
	if xml.Unmarshal(body, &doc) != nil || doc.XMLName.Local != "ListPartsResult" || len(doc.Parts) > 10000 {
		proofError(w, 502, "MultipartProofBoundExceeded")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	checks := make([]string, 0, len(doc.Parts))
	seen := map[int]proofListedPart{}
	for _, listed := range doc.Parts {
		part, e := p.store.part(t, listed.Number)
		old, duplicate := seen[listed.Number]
		if e != nil || (duplicate && old != listed) || !part.Validated || part.AttemptEpoch != "" || part.Uncertain || part.Size != listed.Size || part.ETag != listed.ETag || (listed.SHA256 != "" && listed.SHA256 != part.SHA256) {
			proofError(w, 503, "MultipartChecksumUnavailable")
			return
		}
		seen[listed.Number] = listed
		if err = p.store.resyncPart(t, listed.Number); err != nil {
			proofError(w, 503, "MultipartProofUnavailable")
			return
		}
		checks = append(checks, part.SHA256)
	}
	// A previous rename may have succeeded while its directory fsync failed.
	// Re-establish durability before publishing any checksum from that record.
	if len(checks) > 0 {
		if err = p.store.durableUploadDirectory(t); err != nil {
			proofError(w, 503, "MultipartProofUnavailable")
			return
		}
	}
	result, err := enrichProofPage(body, checks)
	if err != nil {
		proofError(w, 502, "MultipartProofBoundExceeded")
		return
	}
	copyProofResponse(w, res, result)
}
func enrichProofPage(body []byte, checks []string) ([]byte, error) {
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)
	dec := xml.NewDecoder(bytes.NewReader(body))
	index := -1
	inPart := false
	for {
		token, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			if t.Name.Local == "Part" {
				index++
				if index >= len(checks) {
					return nil, fmt.Errorf("part count changed")
				}
				inPart = true
			}
			if inPart && t.Name.Local == "ChecksumSHA256" {
				if err := dec.Skip(); err != nil {
					return nil, err
				}
				continue
			}
		case xml.EndElement:
			if t.Name.Local == "Part" && inPart {
				if err := enc.EncodeElement(checks[index], xml.StartElement{Name: xml.Name{Local: "ChecksumSHA256"}}); err != nil {
					return nil, err
				}
				inPart = false
			}
		}
		if err := enc.EncodeToken(token); err != nil {
			return nil, err
		}
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	if out.Len() > proofPageLimit {
		return nil, fmt.Errorf("enriched response exceeds bound")
	}
	return out.Bytes(), nil
}

// The admission fence is durable before waiting. A late UploadPart therefore
// cannot enter between observing zero known writers and terminal dispatch.
func (p *multipartProof) terminal(w http.ResponseWriter, r *http.Request, t proofTuple, a *proofActor) {
	a.terminal.Lock()
	defer a.terminal.Unlock()
	if r.ContentLength > 4<<20 {
		proofError(w, 400, "InvalidRequest")
		return
	}
	a.mu.Lock()
	u, err := p.store.upload(t)
	if errors.Is(err, os.ErrNotExist) {
		u = proofUpload{Version: 1, StorageID: p.store.storageID, Tuple: t, Epoch: p.epoch}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		a.mu.Unlock()
		proofError(w, 503, "MultipartProofCorrupt")
		return
	}
	u.Closed = true
	u.TerminalUncertain = u.TerminalUncertain || u.TerminalAttemptEpoch != ""
	u.TerminalAttemptEpoch = p.epoch
	if err = p.store.saveUpload(u); err != nil {
		a.mu.Unlock()
		proofError(w, 503, "MultipartProofUnavailable")
		return
	}
	for a.writers > 0 {
		changed := a.changed
		a.mu.Unlock()
		select {
		case <-r.Context().Done():
			proofError(w, 503, "MultipartWriteUncertain")
			return
		case <-changed:
		}
		a.mu.Lock()
	}
	a.mu.Unlock()
	res, err := p.forward(r)
	if err != nil {
		proofError(w, 502, "MultipartWriteUncertain")
		return
	}
	body, err := proofResponseBody(res, proofPageLimit)
	if err != nil {
		proofError(w, 502, "MultipartWriteUncertain")
		return
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		a.mu.Lock()
		u.TerminalAttemptEpoch = ""
		if err = p.store.saveUpload(u); err != nil {
			a.mu.Unlock()
			proofError(w, 503, "MultipartProofUnavailable")
			return
		}
		err = p.cleanup(r.Context(), u, false)
		a.mu.Unlock()
		if err != nil {
			proofError(w, 503, "MultipartProofUnavailable")
			return
		}
	}
	copyProofResponse(w, res, body)
}

func (p *multipartProof) nativePart(ctx context.Context, t proofTuple, number int) (exists, part bool, err error) {
	u := *p.target
	u.Path = "/" + t.Bucket + "/" + t.Key
	u.RawPath = ""
	q := url.Values{"uploadId": {t.UploadID}, "max-parts": {"1"}}
	if number > 0 {
		q.Set("part-number-marker", strconv.Itoa(number-1))
	}
	u.RawQuery = q.Encode()
	r, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return false, false, err
	}
	sum := digest(nil)
	r.Header.Set("X-Amz-Content-Sha256", sum)
	if err = v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(ctx, p.credentials, r, sum, "s3", "us-east-1", time.Now()); err != nil {
		return false, false, err
	}
	res, err := p.client.Do(r)
	if err != nil {
		return false, false, err
	}
	b, err := proofResponseBody(res, proofPageLimit)
	if err != nil {
		return false, false, err
	}
	if res.StatusCode == 404 {
		var e struct{ Code string }
		if xml.Unmarshal(b, &e) == nil && e.Code == "NoSuchUpload" {
			return false, false, nil
		}
	}
	if res.StatusCode != 200 {
		return false, false, fmt.Errorf("native MPU inventory HTTP %d", res.StatusCode)
	}
	var page proofList
	if xml.Unmarshal(b, &page) != nil || page.XMLName.Local != "ListPartsResult" {
		return false, false, fmt.Errorf("invalid native MPU inventory")
	}
	for _, v := range page.Parts {
		if v.Number == number {
			return true, true, nil
		}
	}
	return true, false, nil
}

func (p *multipartProof) cleanup(ctx context.Context, u proofUpload, barrier bool) error {
	if !barrier && (u.TerminalUncertain || u.TerminalAttemptEpoch != "") {
		return nil
	}
	path := p.store.uploadPath(u.Tuple)
	if err := cleanProofTemps(path); err != nil {
		return err
	}
	entries, err := boundedProofEntries(path, 10001)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "upload.json" {
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("nonregular multipart proof")
		}
		n, err := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil || entry.Name() != strconv.Itoa(n)+".json" || n < 1 || n > 10000 {
			return fmt.Errorf("unknown multipart proof file")
		}
		part, err := p.store.part(u.Tuple, n)
		if err != nil {
			return err
		}
		if !barrier && (part.Uncertain || part.AttemptEpoch != "") {
			return nil
		}
	}
	exists, _, err := p.nativePart(ctx, u.Tuple, 0)
	if err != nil || exists {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "upload.json" {
			if err = os.Remove(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
	}
	if err = syncProofDir(path); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(path, "upload.json")); err != nil {
		return err
	}
	if err = syncProofDir(path); err != nil {
		return err
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return syncProofDir(p.store.root)
}
func (p *multipartProof) recover(ctx context.Context) error {
	if err := cleanProofTemps(p.store.root); err != nil {
		return err
	}
	directory, err := os.Open(p.store.root)
	if err != nil {
		return err
	}
	defer directory.Close()
	for {
		entries, readErr := directory.ReadDir(128)
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		for _, entry := range entries {
			if err = ctx.Err(); err != nil {
				return err
			}
			if entry.Name() == "format.json" {
				continue
			}
			if !entry.IsDir() || !validDigest(entry.Name()) || len(entry.Name()) != 64 {
				return fmt.Errorf("unknown multipart proof journal entry")
			}
			path := filepath.Join(p.store.root, entry.Name())
			if err = cleanProofTemps(path); err != nil {
				return err
			}
			var u proofUpload
			if err = p.store.read(filepath.Join(path, "upload.json"), &u); errors.Is(err, os.ErrNotExist) {
				children, e := boundedProofEntries(path, 0)
				if e != nil || len(children) != 0 {
					return fmt.Errorf("partial multipart proof directory")
				}
				if e = os.Remove(path); e != nil {
					return e
				}
				if e = syncProofDir(p.store.root); e != nil {
					return e
				}
				continue
			} else if err != nil {
				return err
			}
			if p.store.uploadPath(u.Tuple) != path {
				return fmt.Errorf("multipart proof identity mismatch")
			}
			if _, err = p.store.upload(u.Tuple); err != nil {
				return err
			}
			if u.Epoch == p.epoch {
				continue
			}
			if err = p.cleanup(ctx, u, true); err != nil {
				return err
			}
			if _, err = os.Lstat(path); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return err
			}
			children, err := boundedProofEntries(path, 10001)
			if err != nil {
				return err
			}
			for _, file := range children {
				if file.Name() == "upload.json" {
					continue
				}
				n, e := strconv.Atoi(strings.TrimSuffix(file.Name(), ".json"))
				if e != nil {
					return fmt.Errorf("unknown part proof")
				}
				part, e := p.store.part(u.Tuple, n)
				if e != nil {
					return e
				}
				// The old backend is now dead, so no old writer can finish later.
				// That proves quiescence, not which request last stored bytes: an
				// uncertain overwrite invalidates the historical proof until a
				// fresh actual-body verification and native ACK establish it.
				if part.AttemptEpoch != "" || part.Uncertain {
					part.Validated = false
				}
				part.AttemptEpoch = ""
				part.Uncertain = false
				if e = p.store.savePart(u.Tuple, part); e != nil {
					return e
				}
			}
			u.Epoch = p.epoch
			u.TerminalAttemptEpoch = ""
			u.TerminalUncertain = false
			if err = p.store.saveUpload(u); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
	}
}
