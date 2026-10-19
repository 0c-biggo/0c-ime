package update

import (
	"crypto/ed25519"
	"encoding/base64"
)

// 0c-release-2026-09：2026-09-29 產生的正式發行金鑰。
var releaseKeyText = []string{"p8+04oez+UEucOQTdHmA7sjI1Pixh5Q4VtepvVBqlb0="}

func releaseKeys() []ed25519.PublicKey { return decodeKeys(releaseKeyText) }

func decodeKeys(texts []string) []ed25519.PublicKey {
	var keys []ed25519.PublicKey
	for _, t := range texts {
		if raw, err := base64.StdEncoding.DecodeString(t); err == nil && len(raw) == ed25519.PublicKeySize {
			keys = append(keys, ed25519.PublicKey(raw))
		}
	}
	return keys
}
