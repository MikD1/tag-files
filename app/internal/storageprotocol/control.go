package storageprotocol

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
)

const ControlPath = "/_tag-files/v1/library"
const MaxControlBytes = 4096
const accessHeader = "X-Tag-Files-Control-Access"
const nonceHeader = "X-Tag-Files-Control-Nonce"
const signatureHeader = "X-Tag-Files-Control-Signature"

var ErrAuthentication = errors.New("owned storage control authentication failed")
var ErrBindingMismatch = errors.New("storage and library identity mismatch")
var ErrProtocol = errors.New("incompatible owned storage control protocol")

type Identity struct {
	Version   int    `json:"version"`
	StorageID string `json:"storageId"`
	VolumeID  string `json:"volumeId"`
	LibraryID string `json:"libraryId"`
	Epoch     string `json:"epoch"`
}
type Binding struct {
	Version   int    `json:"version"`
	StorageID string `json:"storageId"`
	VolumeID  string `json:"volumeId"`
	LibraryID string `json:"libraryId"`
}
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}
type Client struct {
	origin, access, secret string
	http                   HTTPClient
}

func NewClient(origin, access, secret string, client HTTPClient) (*Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || access == "" || secret == "" || strings.ContainsAny(access+secret, "\x00\r\n") {
		return nil, ErrProtocol
	}
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client = &http.Client{Timeout: 30 * time.Second, Transport: transport}
	}
	return &Client{origin: strings.TrimRight(origin, "/"), access: access, secret: secret, http: client}, nil
}
func sum(data []byte) string { value := sha256.Sum256(data); return hex.EncodeToString(value[:]) }
func signature(secret, message string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}
func requestMessage(r *http.Request, body []byte) string {
	return "tag-files/control/request/v1\n" + r.Method + "\n" + r.Host + "\n" + r.URL.RequestURI() + "\n" + r.Header.Get(nonceHeader) + "\n" + sum(body)
}
func responseMessage(r *http.Request, status int, body []byte) string {
	return "tag-files/control/response/v1\n" + r.Header.Get(signatureHeader) + "\n" + r.Header.Get(nonceHeader) + "\n" + strconv.Itoa(status) + "\n" + sum(body)
}
func validMAC(expected, actual string) bool {
	want, e1 := hex.DecodeString(expected)
	got, e2 := hex.DecodeString(actual)
	return e1 == nil && e2 == nil && len(got) == sha256.Size && hmac.Equal(want, got)
}

func Authenticate(r *http.Request, body []byte, access, secret string) bool {
	return len(body) <= MaxControlBytes && len(r.Header.Values(accessHeader)) == 1 && hmac.Equal([]byte(r.Header.Get(accessHeader)), []byte(access)) && len(r.Header.Values(nonceHeader)) == 1 && domain.ValidateUUID(r.Header.Get(nonceHeader)) == nil && len(r.Header.Values(signatureHeader)) == 1 && validMAC(signature(secret, requestMessage(r, body)), r.Header.Get(signatureHeader))
}

// Reply authenticates the exact status/body and the request's random nonce. A
// previously signed identity response cannot authenticate a fresh request.
func Reply(w http.ResponseWriter, r *http.Request, secret string, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(nonceHeader, r.Header.Get(nonceHeader))
	w.Header().Set(signatureHeader, signature(secret, responseMessage(r, status, body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (c *Client) request(ctx context.Context, method string, binding *Binding) (Identity, error) {
	var body []byte
	var err error
	if binding != nil {
		body, err = json.Marshal(binding)
		if err != nil {
			return Identity{}, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+ControlPath, bytes.NewReader(body))
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set(accessHeader, c.access)
	req.Header.Set(nonceHeader, domain.NewUUID())
	req.Header.Set(signatureHeader, signature(c.secret, requestMessage(req, body)))
	if binding != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return Identity{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxControlBytes+1))
	if err != nil {
		return Identity{}, err
	}
	if len(data) > MaxControlBytes {
		return Identity{}, ErrProtocol
	}
	if response.StatusCode == http.StatusUnauthorized {
		return Identity{}, ErrAuthentication
	}
	if len(response.Header.Values(signatureHeader)) != 1 || len(response.Header.Values(nonceHeader)) != 1 || response.Header.Get(nonceHeader) != req.Header.Get(nonceHeader) || !validMAC(signature(c.secret, responseMessage(req, response.StatusCode, data)), response.Header.Get(signatureHeader)) {
		return Identity{}, ErrAuthentication
	}
	if response.StatusCode == http.StatusConflict {
		return Identity{}, ErrBindingMismatch
	}
	if response.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("owned storage control HTTP %d", response.StatusCode)
	}
	var identity Identity
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if !json.Valid(data) || decoder.Decode(&identity) != nil || identity.Version != 1 || domain.ValidateUUID(identity.StorageID) != nil || !ValidVolumeID(identity.VolumeID) || domain.ValidateUUID(identity.Epoch) != nil || (identity.LibraryID != "" && domain.ValidateUUID(identity.LibraryID) != nil) {
		return Identity{}, ErrProtocol
	}
	identity.StorageID, identity.LibraryID, identity.Epoch = strings.ToLower(identity.StorageID), strings.ToLower(identity.LibraryID), strings.ToLower(identity.Epoch)
	return identity, nil
}
func (c *Client) Identity(ctx context.Context) (Identity, error) {
	return c.request(ctx, http.MethodGet, nil)
}
func (c *Client) Bind(ctx context.Context, storageID, libraryID, volumeID string) (Identity, error) {
	if domain.ValidateUUID(storageID) != nil || domain.ValidateUUID(libraryID) != nil || !ValidVolumeID(volumeID) {
		return Identity{}, ErrProtocol
	}
	binding := Binding{Version: 1, StorageID: strings.ToLower(storageID), LibraryID: strings.ToLower(libraryID), VolumeID: volumeID}
	identity, err := c.request(ctx, http.MethodPost, &binding)
	if err != nil {
		return identity, err
	}
	if identity.StorageID != binding.StorageID || identity.LibraryID != binding.LibraryID || identity.VolumeID != binding.VolumeID {
		return Identity{}, ErrBindingMismatch
	}
	return identity, nil
}

func ValidVolumeID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}
