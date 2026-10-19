package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"git.biggo.com/0c/ime/internal/update"
)

func writeTar(t *testing.T, path string, release map[string]any) {
	f, _ := os.Create(path)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	b, _ := json.Marshal(release)
	tw.WriteHeader(&tar.Header{Name: "0c-x/release.json", Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg})
	tw.Write(b)
	tw.Close()
	gz.Close()
	f.Close()
}

func writeZip(t *testing.T, path string, release map[string]any) {
	f, _ := os.Create(path)
	z := zip.NewWriter(f)
	w, _ := z.Create("0c-x/release.json")
	b, _ := json.Marshal(release)
	w.Write(b)
	z.Close()
	f.Close()
}

func TestFeedSignsRealBundleMetadata(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "k")
	if err := keygen([]string{"-private", key}); err != nil {
		t.Fatal(err)
	}
	priv, err := loadKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(key); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 { // Windows 沒有 Unix 權限位
		t.Fatal("private key must be 0600")
	}
	linux := filepath.Join(dir, "l.tar.gz")
	windows := filepath.Join(dir, "w.zip")
	writeTar(t, linux, map[string]any{"product": "0c", "version": "0.1.0-dev.18", "channel": "internal-preview", "machine": "x86_64"})
	writeZip(t, windows, map[string]any{"product": "0c", "version": "0.1.0-dev.18", "channel": "internal-preview", "os": "windows", "arch": "amd64"})
	out := filepath.Join(dir, "latest.json")
	if err := feed([]string{"-key", key, "-serial", "3", "-out", out, "-asset", linux + "=https://s3.example/l.tar.gz", "-asset", windows + "=https://s3.example/w.zip"}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(out)
	sig, _ := os.ReadFile(out + ".sig")
	f, err := update.VerifySignedFeed(body, sig, []ed25519.PublicKey{priv.Public().(ed25519.PublicKey)}, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != "0.1.0-dev.18" || len(f.Assets) != 2 || f.Assets[0].OS != "linux" || f.Assets[1].OS != "windows" {
		t.Fatalf("feed %+v", f)
	}
	// 個別平台改種類：Linux 先發 core 換更新器，Windows 照常 shell（#92、#93）。
	split := filepath.Join(dir, "split.json")
	if err := feed([]string{"-key", key, "-serial", "4", "-out", split, "-kind", "shell", "-kind-for", "linux=core",
		"-asset", linux + "=https://s3.example/l.tar.gz", "-asset", windows + "=https://s3.example/w.zip"}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(split)
	sig, _ = os.ReadFile(split + ".sig")
	f, err = update.VerifySignedFeed(body, sig, []ed25519.PublicKey{priv.Public().(ed25519.PublicKey)}, time.Now(), 0)
	if err != nil || f.Assets[0].Kind != "core" || f.Assets[1].Kind != "shell" {
		t.Fatalf("per-OS kind: %+v %v", f, err)
	}
	// 同一 OS 的第二個架構也要套用 -kind-for。
	arm := filepath.Join(dir, "a.tar.gz")
	writeTar(t, arm, map[string]any{"product": "0c", "version": "0.1.0-dev.18", "channel": "internal-preview", "machine": "aarch64"})
	if err := feed([]string{"-key", key, "-serial", "5", "-out", split, "-kind", "shell", "-kind-for", "linux=core",
		"-asset", linux + "=https://s3.example/l.tar.gz", "-asset", arm + "=https://s3.example/a.tar.gz"}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(split)
	sig, _ = os.ReadFile(split + ".sig")
	f, err = update.VerifySignedFeed(body, sig, []ed25519.PublicKey{priv.Public().(ed25519.PublicKey)}, time.Now(), 0)
	if err != nil || len(f.Assets) != 2 || f.Assets[0].Kind != "core" || f.Assets[1].Kind != "core" {
		t.Fatalf("every Linux arch must be core: %+v %v", f, err)
	}
	for _, bad := range []string{"linux", "linux=both", "darwin=core"} {
		err := feed([]string{"-key", key, "-serial", "5", "-out", split, "-kind-for", bad, "-asset", linux + "=https://s3.example/l.tar.gz"})
		if err == nil {
			t.Fatalf("-kind-for %q accepted", bad)
		}
	}
	if err := keygen([]string{"-private", key}); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("keygen must not overwrite an existing key: %v", err)
	}
}

// 續簽：只接受信任金鑰簽的索引（過期也可），序號加一、內容不變；有效期還長時不動。
func TestRenewKeepsContentAndBumpsSerial(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	pub := priv.Public().(ed25519.PublicKey)
	issued := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	f := &update.SignedFeed{Schema: update.SignedFeedSchema, Product: "0c", Version: "0.1.0-dev.20", Channel: "public-beta",
		Serial: 3, Issued: issued, Expires: issued.Add(14 * 24 * time.Hour), Rollout: 100, Withdrawn: []string{"0.1.0-dev.17"},
		Assets: []update.SignedAsset{{Asset: update.Asset{OS: "linux", Arch: "amd64", URL: "https://s3.example/l.tar.gz", SHA256: strings.Repeat("a", 64), Size: 10}, Kind: "core"}}}
	body, sig, err := update.SignFeed(f, priv)
	if err != nil {
		t.Fatal(err)
	}
	keys := []ed25519.PublicKey{pub}
	if g, _, err := renewFeed(body, sig, keys, issued.Add(24*time.Hour), 7*24*time.Hour); err != nil || g != nil {
		t.Fatalf("剩 13 天不應續簽：%v %v", g, err)
	}
	later := issued.Add(20 * 24 * time.Hour) // 已過期
	g, previous, err := renewFeed(body, sig, keys, later, 7*24*time.Hour)
	if err != nil || g == nil {
		t.Fatalf("過期應可續簽：%v", err)
	}
	if g.Serial != 4 || !g.Issued.Equal(later) || !previous.Equal(f.Expires) || g.Version != f.Version || g.Rollout != 100 ||
		len(g.Assets) != 1 || g.Assets[0] != f.Assets[0] || len(g.Withdrawn) != 1 {
		t.Fatalf("續簽改了內容：%+v", g)
	}
	g.Expires = later.Add(14 * 24 * time.Hour)
	nb, ns, err := update.SignFeed(g, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := update.VerifySignedFeed(nb, ns, keys, later, 3); err != nil {
		t.Fatalf("續簽後客戶端應接受：%v", err)
	}
	_, other, _ := ed25519.GenerateKey(nil)
	_, badSig, _ := update.SignFeed(f, other)
	if _, _, err := renewFeed(body, badSig, keys, later, 7*24*time.Hour); err == nil {
		t.Fatal("接受了非信任金鑰簽的索引")
	}
	if _, _, err := renewFeed(append([]byte(" "), body...), sig, keys, later, 7*24*time.Hour); err == nil {
		t.Fatal("接受了被改過的索引")
	}
}
