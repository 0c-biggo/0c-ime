//go:build e2etest

package update

import (
	"crypto/ed25519"
	"crypto/x509"
)

// E2E 測試建置另外信任測試金鑰（testdata/e2e/release-test.key）；正式建置不含此檔。
var testKeyText = []string{"qnPEX/wcxogJ72KQcc2BT6UunnJyBKxtLDcOk9l1HAY="}

func trustedKeys() []ed25519.PublicKey { return append(releaseKeys(), decodeKeys(testKeyText)...) }

// E2EBuild 表示這是 E2E 測試建置：允許測試用的短週期，並信任測試 CA（testdata/e2e/test-ca.pem）。
const E2EBuild = true

const testCA = `-----BEGIN CERTIFICATE-----
MIIB3TCCAYOgAwIBAgIUcrDsZn0Y7Dg8YU3HPogrzK1mfYswCgYIKoZIzj0EAwIw
PDE6MDgGA1UEAwwxMGMgRTJFIHRlc3QgQ0EgKHVudHJ1c3RlZCBvdXRzaWRlIGUy
ZXRlc3QgYnVpbGRzKTAeFw0yNjA5MjkxMDA1MzlaFw0zNjA5MjYxMDA1MzlaMDwx
OjA4BgNVBAMMMTBjIEUyRSB0ZXN0IENBICh1bnRydXN0ZWQgb3V0c2lkZSBlMmV0
ZXN0IGJ1aWxkcykwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAASJ8k6BgiFOiQzR
wm/yX5zj+c3sSPxnf4yuljqva/oevxSytjI6O8i10s1r3+ns0qh1JXZAEf58Sbw7
OdnNZBuvo2MwYTAdBgNVHQ4EFgQU8f4vQLEfbDxEEVa9CAnxibsQJUcwHwYDVR0j
BBgwFoAU8f4vQLEfbDxEEVa9CAnxibsQJUcwDwYDVR0TAQH/BAUwAwEB/zAOBgNV
HQ8BAf8EBAMCAQYwCgYIKoZIzj0EAwIDSAAwRQIhALYd3mTSbbTeV6o4QN80vsdj
P2ls6oJZjsyW27ATs+utAiBZD9pIgb+gmCB7qN3enA2/M+ArCe4+9GfyRIvqsLEZ
Cw==
-----END CERTIFICATE-----
`

// tlsRoots 在系統信任之外加入測試 CA，只用於本機 HTTPS 測試發布服務。
func tlsRoots() *x509.CertPool {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	pool.AppendCertsFromPEM([]byte(testCA))
	return pool
}
