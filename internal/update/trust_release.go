//go:build !e2etest

package update

import (
	"crypto/ed25519"
	"crypto/x509"
)

// 正式發行公鑰（私鑰只在發布端；金鑰輪替時同時列出新舊金鑰，再由後續版本移除舊金鑰）。
func trustedKeys() []ed25519.PublicKey { return releaseKeys() }

// E2EBuild 為 false：正式建置不接受測試週期、測試金鑰或測試 CA。
const E2EBuild = false

// tlsRoots 為 nil：使用系統信任。
func tlsRoots() *x509.CertPool { return nil }
