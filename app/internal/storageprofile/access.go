package storageprofile

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

func decodeXML(data []byte, value any) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(value); err != nil {
		return err
	}
	for {
		token, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch v := token.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(v)) != "" {
				return fmt.Errorf("trailing XML data")
			}
		case xml.Comment, xml.ProcInst:
		default:
			return fmt.Errorf("extra XML document")
		}
	}
}

func ValidateACL(data []byte) error {
	var acl struct {
		XMLName xml.Name `xml:"AccessControlPolicy"`
		Owner   struct {
			ID string `xml:"ID"`
		} `xml:"Owner"`
		Grants []struct {
			Grantee struct {
				ID    string `xml:"ID"`
				URI   string `xml:"URI"`
				Email string `xml:"EmailAddress"`
				Type  string `xml:"type,attr"`
			} `xml:"Grantee"`
			Permission string `xml:"Permission"`
		} `xml:"AccessControlList>Grant"`
	}
	if err := decodeXML(data, &acl); err != nil {
		return fmt.Errorf("cannot verify bucket ACL: %w", err)
	}
	if acl.Owner.ID == "" || len(acl.Grants) == 0 {
		return fmt.Errorf("bucket owner ACL is absent")
	}
	for _, grant := range acl.Grants {
		g := grant.Grantee
		if g.ID != acl.Owner.ID || g.URI != "" || g.Email != "" || (g.Type != "" && g.Type != "CanonicalUser") || grant.Permission != "FULL_CONTROL" {
			return fmt.Errorf("bucket ACL differs from the private owner-only profile")
		}
	}
	return nil
}

func ValidatePublicAccessBlock(data []byte) error {
	var block struct {
		XMLName         xml.Name `xml:"PublicAccessBlockConfiguration"`
		BlockACL        bool     `xml:"BlockPublicAcls"`
		IgnoreACL       bool     `xml:"IgnorePublicAcls"`
		BlockPolicy     bool     `xml:"BlockPublicPolicy"`
		RestrictBuckets bool     `xml:"RestrictPublicBuckets"`
	}
	if err := decodeXML(data, &block); err != nil {
		return fmt.Errorf("cannot verify public access block: %w", err)
	}
	if !block.BlockACL || !block.IgnoreACL || !block.BlockPolicy || !block.RestrictBuckets {
		return fmt.Errorf("all public-access-block settings must be enabled")
	}
	return nil
}

func accessDocument(ctx context.Context, client *http.Client, credentials aws.Credentials, endpoint, query string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/tag-files?"+query, nil)
	if err != nil {
		return 0, nil, err
	}
	if err := v4.NewSigner().SignHTTP(ctx, credentials, req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "s3", "us-east-1", time.Now()); err != nil {
		return 0, nil, fmt.Errorf("cannot sign access-policy probe")
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("cannot read bucket access profile: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxConfigBytes+1))
	if err != nil {
		return 0, nil, err
	}
	if len(data) > MaxConfigBytes {
		return 0, nil, fmt.Errorf("bucket access document exceeds bound")
	}
	return res.StatusCode, data, nil
}

func checkBucketAccess(ctx context.Context, client *http.Client, credentials aws.Credentials, endpoint string) error {
	status, data, err := accessDocument(ctx, client, credentials, endpoint, "publicAccessBlock")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("public-access-block configuration is required (HTTP %d)", status)
	}
	if err := ValidatePublicAccessBlock(data); err != nil {
		return err
	}
	status, data, err = accessDocument(ctx, client, credentials, endpoint, "policy")
	if err != nil {
		return err
	}
	// This owned single-user profile needs no bucket policies. Refuse any
	// policy rather than attempting an incomplete IAM condition evaluator.
	if status != http.StatusNotFound {
		return fmt.Errorf("bucket policy must be absent (HTTP %d)", status)
	}
	var missing struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
	}
	if err := decodeXML(data, &missing); err != nil || missing.Code != "NoSuchBucketPolicy" {
		return fmt.Errorf("cannot establish absence of a bucket policy")
	}
	status, data, err = accessDocument(ctx, client, credentials, endpoint, "lifecycle")
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		missing.Code = ""
		if err := decodeXML(data, &missing); err != nil || missing.Code != "NoSuchLifecycleConfiguration" {
			return fmt.Errorf("cannot establish absence of bucket lifecycle rules")
		}
	} else if status == http.StatusOK {
		var lifecycle struct {
			XMLName xml.Name   `xml:"LifecycleConfiguration"`
			Rules   []xml.Name `xml:"Rule"`
		}
		if err := decodeXML(data, &lifecycle); err != nil || len(lifecycle.Rules) != 0 {
			return fmt.Errorf("bucket lifecycle rules conflict with durable uploads")
		}
	} else {
		return fmt.Errorf("cannot verify bucket lifecycle rules (HTTP %d)", status)
	}
	status, data, err = accessDocument(ctx, client, credentials, endpoint, "acl")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("cannot read owner-only bucket ACL (HTTP %d)", status)
	}
	return ValidateACL(data)
}
