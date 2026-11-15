package storageprofile

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

func canonicalPartSHA(value string) bool {
	b, err := base64.StdEncoding.Strict().DecodeString(value)
	return err == nil && len(b) == 32 && base64.StdEncoding.EncodeToString(b) == value
}

// Authenticate the owned pinned SDK profile using the SDK's canonical signer.
// The server adds unsigned transport headers, so sign only the original signed
// subset. Method, escaped path, canonical query, Host and length remain intact.
func authenticateMultipart(r *http.Request, credentials aws.Credentials, upload bool) bool {
	if len(r.Header.Values("Authorization")) != 1 || len(r.Header.Get("Authorization")) > 8192 || r.URL.Query().Has("X-Amz-Signature") {
		return false
	}
	authorization := r.Header.Get("Authorization")
	const prefix = "AWS4-HMAC-SHA256 "
	if !strings.HasPrefix(authorization, prefix) {
		return false
	}
	fields := strings.Split(strings.TrimPrefix(authorization, prefix), ", ")
	if len(fields) != 3 || !strings.HasPrefix(fields[0], "Credential=") || !strings.HasPrefix(fields[1], "SignedHeaders=") || !strings.HasPrefix(fields[2], "Signature=") {
		return false
	}
	scope := strings.Split(strings.TrimPrefix(fields[0], "Credential="), "/")
	if len(scope) != 5 || scope[0] != credentials.AccessKeyID || scope[3] != "s3" || scope[4] != "aws4_request" || len(scope[2]) == 0 || len(scope[2]) > 64 {
		return false
	}
	headerNames := strings.Split(strings.TrimPrefix(fields[1], "SignedHeaders="), ";")
	if !sort.StringsAreSorted(headerNames) {
		return false
	}
	clone := r.Clone(r.Context())
	clone.Header = make(http.Header)
	seen := map[string]bool{}
	for _, name := range headerNames {
		if name == "" || name != strings.ToLower(name) || seen[name] {
			return false
		}
		seen[name] = true
		if name == "host" || name == "content-length" {
			continue
		}
		values := r.Header.Values(name)
		if len(values) != 1 {
			return false
		}
		clone.Header.Set(name, values[0])
	}
	if !seen["host"] || !seen["x-amz-date"] || !seen["x-amz-content-sha256"] || (r.ContentLength > 0 && !seen["content-length"]) {
		return false
	}
	if upload && (!seen["x-amz-checksum-sha256"] || !canonicalPartSHA(r.Header.Get("X-Amz-Checksum-Sha256"))) {
		return false
	}
	if r.Header.Get("Content-Encoding") != "" || len(r.TransferEncoding) > 0 || r.ContentLength < 0 {
		return false
	}
	date, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil || scope[1] != date.Format("20060102") || time.Since(date) > 15*time.Minute || time.Until(date) > 15*time.Minute {
		return false
	}
	payload := r.Header.Get("X-Amz-Content-Sha256")
	if payload != "UNSIGNED-PAYLOAD" && (len(payload) != 64 || !validDigest(payload) || strings.ToLower(payload) != payload) {
		return false
	}
	if clone.URL.Host == "" {
		clone.URL.Host = r.Host
		clone.URL.Scheme = "http"
	}
	if err := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(r.Context(), credentials, clone, payload, "s3", scope[2], date); err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(clone.Header.Get("Authorization")), []byte(authorization)) == 1
}
