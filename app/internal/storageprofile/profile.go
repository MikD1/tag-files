// Package storageprofile verifies the pinned deployment profile inside the
// storage container. The application container never mounts these paths.
package storageprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
)

const Backend = "[leveldb2]\nenabled = true\ndir = \"/data/filerldb2\"\n"

type identityCredential struct {
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}
type identity struct {
	Name        string               `json:"name"`
	Credentials []identityCredential `json:"credentials"`
	Actions     []string             `json:"actions"`
}
type identityConfig struct {
	Identities []identity `json:"identities"`
}

func ValidateBackend(data []byte) error {
	// This is an owned, pinned file, not a general TOML configuration surface.
	if strings.TrimSpace(string(data)) != strings.TrimSpace(Backend) {
		return fmt.Errorf("expected the pinned LevelDB2 backend at /data/filerldb2")
	}
	return nil
}

func ValidateRules(data []byte) error {
	var config struct {
		Version   int `json:"version"`
		Locations []struct {
			Prefix   string `json:"locationPrefix"`
			Fsync    *bool  `json:"fsync"`
			TTL      string `json:"ttl"`
			ReadOnly bool   `json:"readOnly"`
			Worm     bool   `json:"worm"`
		} `json:"locations"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("cannot decode persisted filer rules: %w", err)
	}
	if config.Version != 2 {
		return fmt.Errorf("unsupported filer rules format")
	}
	root := false
	seen := map[string]bool{}
	for _, loc := range config.Locations {
		if !strings.HasPrefix(loc.Prefix, "/") || seen[loc.Prefix] {
			return fmt.Errorf("invalid or duplicate filer rule prefix")
		}
		seen[loc.Prefix] = true
		if loc.TTL != "" {
			return fmt.Errorf("storage TTL conflicts with durable uploads")
		}
		if loc.ReadOnly || loc.Worm {
			return fmt.Errorf("storage rule prevents required writes or cleanup")
		}
		if loc.Fsync != nil && !*loc.Fsync {
			return fmt.Errorf("storage rule disables inherited fsync")
		}
		if loc.Prefix == "/" && loc.Fsync != nil && *loc.Fsync {
			root = true
		}
	}
	if !root {
		return fmt.Errorf("persisted global fsync rule is absent")
	}
	return nil
}

func PrivateCredentials(data []byte) (aws.Credentials, error) {
	var config identityConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if !json.Valid(data) {
		return aws.Credentials{}, fmt.Errorf("cannot decode private S3 profile")
	}
	if err := decoder.Decode(&config); err != nil {
		return aws.Credentials{}, fmt.Errorf("cannot decode private S3 profile")
	}
	if len(config.Identities) == 0 {
		return aws.Credentials{}, fmt.Errorf("private S3 identity is required")
	}
	var chosen aws.Credentials
	for _, id := range config.Identities {
		if strings.TrimSpace(id.Name) == "" || strings.EqualFold(id.Name, "anonymous") || len(id.Credentials) == 0 || len(id.Actions) == 0 {
			return aws.Credentials{}, fmt.Errorf("anonymous or incomplete S3 identity is forbidden")
		}
		for _, required := range []string{"Read", "Write", "List"} {
			found := false
			for _, action := range id.Actions {
				if action == required || action == required+":tag-files" {
					found = true
				}
			}
			if !found {
				return aws.Credentials{}, fmt.Errorf("S3 identity must explicitly allow read, write/cleanup and list on tag-files")
			}
		}
		for _, cred := range id.Credentials {
			if len(bytes.TrimSpace([]byte(cred.AccessKey))) == 0 || len(bytes.TrimSpace([]byte(cred.SecretKey))) == 0 {
				return aws.Credentials{}, fmt.Errorf("empty S3 credential is forbidden")
			}
			if chosen.AccessKeyID == "" {
				chosen = aws.Credentials{AccessKeyID: cred.AccessKey, SecretAccessKey: cred.SecretKey, Source: "private-storage-profile"}
			}
		}
	}
	return chosen, nil
}
