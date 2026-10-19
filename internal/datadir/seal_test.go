package datadir

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// 混淆後的字典可被引擎載入；manifest 與其雜湊維持明文，不重複混淆，舊的明文資料包照常可讀。
func TestSealedPackLoadsAndKeepsManifest(t *testing.T) {
	plain := []byte("lexicon bytes 字典")
	sealed, err := Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain) || !bytes.HasPrefix(sealed, sealMagic) {
		t.Fatal("sealed output still contains the plaintext")
	}
	again, err := Seal(append([]byte(nil), sealed...))
	if err == nil || again != nil {
		t.Fatal("sealing twice must be rejected")
	}
	back, err := unseal(sealed)
	if err != nil || !bytes.Equal(back, plain) {
		t.Fatalf("unseal: %q %v", back, err)
	}
	if b, _ := unseal(plain); !bytes.Equal(b, plain) {
		t.Fatal("plaintext must pass through")
	}
	if _, err := unseal(sealMagic); err == nil {
		t.Fatal("truncated sealed data must fail")
	}
}

func TestSealDirKeepsPackLoadable(t *testing.T) {
	dir := t.TempDir()
	lex, err := lexicon.Build([]lexicon.Entry{{Surface: "甲", Reading: zhuyin.ParseReading("ㄐㄧㄚˇ"), PRW: 1}})
	if err != nil {
		t.Fatal(err)
	}
	sys, err := lm.BuildCompact(map[string]float64{"甲": 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]any{}
	for name, data := range map[string][]byte{"lexicon.bin": lex.Encode(), "lm.bin": sys.Encode()} {
		os.WriteFile(filepath.Join(dir, name), data, 0o600)
		files[name] = map[string]string{"sha256": fmt.Sprintf("%x", sha256.Sum256(data))}
	}
	manifest, _ := json.Marshal(map[string]any{"format": 1, "files": files})
	os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o600)
	if err := SealDir(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range SealedFiles {
		if b, _ := os.ReadFile(filepath.Join(dir, name)); !bytes.HasPrefix(b, sealMagic) {
			t.Fatalf("%s not sealed", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "manifest.json")); !bytes.Equal(b, manifest) {
		t.Fatal("manifest must stay unchanged so dictionary pairing survives")
	}
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	if err := SealDir(dir); err == nil {
		t.Fatal("sealing a sealed pack must fail")
	}
}
