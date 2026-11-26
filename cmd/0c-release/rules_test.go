package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// -rules（#295）：規則的資產由本機套件算出雜湊、大小與版本；平台或版本對不上就不簽。續簽保留規則，
// 遇到這一版不認得的欄位寧可失敗，不悄悄丟掉。
func TestFeedRulesAndLosslessRenew(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "k")
	if err := keygen([]string{"-private", key}); err != nil {
		t.Fatal(err)
	}
	priv, _ := loadKey(key)
	pub := priv.Public().(ed25519.PublicKey)
	shell := filepath.Join(dir, "shell.zip")
	core := filepath.Join(dir, "core.zip")
	linux := filepath.Join(dir, "l.tar.gz")
	writeZip(t, shell, map[string]any{"product": "0c", "version": "0.2.16", "channel": "public-beta", "os": "windows", "arch": "amd64"})
	writeZip(t, core, map[string]any{"product": "0c", "version": "0.2.16", "channel": "public-beta", "os": "windows", "arch": "amd64", "files": map[string]string{"x": "y"}})
	writeTar(t, linux, map[string]any{"product": "0c", "version": "0.2.16", "channel": "public-beta", "os": "linux", "arch": "amd64"})
	rules := filepath.Join(dir, "rules.json")
	write := func(v any) {
		b, _ := json.Marshal(v)
		os.WriteFile(rules, b, 0o600)
	}
	write([]map[string]any{{"os": "windows", "arch": "amd64", "installed_min": "0.2.15", "assets": []map[string]string{
		{"kind": "core", "path": core, "url": "https://s3.example/core.zip"}, {"kind": "updater", "path": shell, "url": "https://s3.example/shell.zip"}}}})
	out := filepath.Join(dir, "feed.json")
	args := []string{"-key", key, "-serial", "7", "-kind", "shell", "-out", out, "-rules", rules, "-asset", shell + "=https://s3.example/shell.zip"}
	if err := feed(args); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(out)
	sig, _ := os.ReadFile(out + ".sig")
	f, err := update.VerifySignedFeed(body, sig, []ed25519.PublicKey{pub}, time.Now(), 0)
	if err != nil || len(f.Rules) != 1 || len(f.Rules[0].Assets) != 2 || f.Rules[0].Assets[1].Version != "0.2.16" || f.Rules[0].Assets[0].Size <= 0 {
		t.Fatalf("%v %+v", err, f)
	}
	if o := f.Resolve("windows", "amd64", "0.2.15", "0.2.15"); o.Asset == nil || o.Asset.Kind != "core" || o.Updater == nil {
		t.Fatalf("%+v", o)
	}
	g, _, err := renewFeed(body, sig, []ed25519.PublicKey{pub}, f.Expires.Add(time.Hour), 7*24*time.Hour)
	if err != nil || len(g.Rules) != 1 || len(g.Rules[0].Assets) != 2 {
		t.Fatalf("renew dropped rules: %v %+v", err, g)
	}
	// 規則的資產平台不符、core 版本不是規則的版本、未知種類：不簽。
	for _, bad := range []any{
		[]map[string]any{{"os": "windows", "arch": "amd64", "assets": []map[string]string{{"kind": "core", "path": linux, "url": "https://s3.example/l"}}}},
		[]map[string]any{{"os": "windows", "arch": "amd64", "version": "0.2.17", "assets": []map[string]string{{"kind": "core", "path": core, "url": "https://s3.example/c"}}}},
		[]map[string]any{{"os": "windows", "arch": "amd64", "assets": []map[string]string{{"kind": "delta", "path": core, "url": "https://s3.example/c"}}}},
	} {
		write(bad)
		if err := feed(args); err == nil {
			t.Errorf("signed %v", bad)
		}
	}
	// 未來的欄位：這一版的續簽工具不認得，拒絕續簽。
	future := strings.Replace(string(body), `"rules"`, `"mirrors": ["https://m.example/"], "rules"`, 1)
	fsig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(future))))
	if _, _, err := renewFeed([]byte(future), fsig, []ed25519.PublicKey{pub}, f.Expires.Add(time.Hour), 7*24*time.Hour); err == nil {
		t.Fatal("renew would silently drop an unknown field")
	}
}
