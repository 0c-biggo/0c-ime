package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testFeed(now time.Time) *SignedFeed {
	return &SignedFeed{Schema: SignedFeedSchema, Product: "0c", Serial: 7, Issued: now, Expires: now.Add(24 * time.Hour),
		Channel: "internal-preview", Version: "0.1.0-dev.18", Rollout: 100,
		Assets: []SignedAsset{{Asset: Asset{OS: "windows", Arch: "amd64", URL: "https://example.org/a.zip", SHA256: strings.Repeat("a", 64), Size: 10}, Kind: "core"}}}
}

func TestSignedFeedAcceptsOnlyValidFreshSignedIndexes(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC().Truncate(time.Second)
	body, sig, err := SignFeed(testFeed(now), priv)
	if err != nil {
		t.Fatal(err)
	}
	keys := []ed25519.PublicKey{otherPub, pub}
	f, err := VerifySignedFeed(body, sig, keys, now, 7)
	if err != nil || f.Version != "0.1.0-dev.18" {
		t.Fatalf("valid feed rejected: %v", err)
	}
	if a, err := f.Select("windows", "amd64"); err != nil || a.Kind != "core" {
		t.Fatalf("select: %v", err)
	}
	cases := map[string]func() error{
		"tampered": func() error {
			_, err := VerifySignedFeed([]byte(strings.Replace(string(body), "dev.18", "dev.19", 1)), sig, keys, now, 0)
			return err
		},
		"untrusted key": func() error { _, err := VerifySignedFeed(body, sig, []ed25519.PublicKey{otherPub}, now, 0); return err },
		"expired":       func() error { _, err := VerifySignedFeed(body, sig, keys, now.Add(25*time.Hour), 0); return err },
		"rollback":      func() error { _, err := VerifySignedFeed(body, sig, keys, now, 8); return err },
		"future":        func() error { _, err := VerifySignedFeed(body, sig, keys, now.Add(-time.Hour), 0); return err },
		"bad signature": func() error { _, err := VerifySignedFeed(body, []byte("xx"), keys, now, 0); return err },
	}
	for name, run := range cases {
		if run() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for name, mutate := range map[string]func(*SignedFeed){
		"rollout":   func(f *SignedFeed) { f.Rollout = 101 },
		"http":      func(f *SignedFeed) { f.Assets[0].URL = "http://example.org/a.zip" },
		"kind":      func(f *SignedFeed) { f.Assets[0].Kind = "other" },
		"window":    func(f *SignedFeed) { f.Expires = f.Issued },
		"duplicate": func(f *SignedFeed) { f.Assets = append(f.Assets, f.Assets[0]) },
		"withdrawn": func(f *SignedFeed) { f.Withdrawn = []string{"../x"} },
	} {
		f := testFeed(now)
		mutate(f)
		if _, _, err := SignFeed(f, priv); err == nil {
			t.Errorf("%s: signed an invalid feed", name)
		}
	}
	f.Withdrawn = []string{"0.1.0-dev.17"}
	if !f.IsWithdrawn("0.1.0-dev.17") || f.IsWithdrawn("0.1.0-dev.18") {
		t.Fatal("withdrawn lookup")
	}
}

func TestReadSignedFeedOverHTTPS(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC().Truncate(time.Second)
	body, sig, _ := SignFeed(testFeed(now), priv)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sig") {
			w.Write(sig)
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	f, raw, err := ReadSignedFeed(context.Background(), server.Client(), server.URL+"/feeds/latest.json", []ed25519.PublicKey{pub}, now, 0)
	if err != nil || f.Serial != 7 || string(raw) != string(body) {
		t.Fatalf("read: %v", err)
	}
}

// 正式建置必須只信任正式發行金鑰。
func TestTrustedKeysAreReleaseKeys(t *testing.T) {
	if len(releaseKeys()) == 0 || (!e2eBuild && len(TrustedKeys()) != len(releaseKeys())) {
		t.Fatal("no trusted release key compiled in")
	}
}
