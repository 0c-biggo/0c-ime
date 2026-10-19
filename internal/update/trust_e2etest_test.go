//go:build e2etest

package update

import "testing"

// E2E 測試建置另外信任測試金鑰；正式建置只有正式金鑰（TestTrustedKeysAreReleaseKeys）。
func TestE2EBuildAlsoTrustsTestKey(t *testing.T) {
	if len(TrustedKeys()) != len(releaseKeys())+1 {
		t.Fatal("e2etest build must add exactly the test key")
	}
}
