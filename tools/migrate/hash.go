package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

func newHash() hash.Hash         { return sha256.New() }
func hexHash(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }
