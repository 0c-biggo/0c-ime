package models

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// Pair 是 scripts/pair_models.py 的 metadata()：把上游的 manifest.json、vocab.json 綁到字典 manifest 的
// SHA-256。vocab 的 dictionary_sha256 改成字典雜湊（沒有就加在最後）；manifest 有 dictionary_sha256 才改；
// manifest 的 files["vocab.json"] 改成新 vocab 的雜湊。其他鍵的順序與值不變，權重不動。
// 回傳 {"manifest.json": …, "vocab.json": …}，逐位元組等於 Python 的輸出。
func Pair(manifest, vocab []byte, dictionary string) (map[string][]byte, error) {
	if !validHex(dictionary) {
		return nil, errors.New("字典雜湊不正確")
	}
	m, err := parsePyJSON(manifest)
	if err != nil {
		return nil, fmt.Errorf("manifest.json：%w", err)
	}
	v, err := parsePyJSON(vocab)
	if err != nil {
		return nil, fmt.Errorf("vocab.json：%w", err)
	}
	if m.kind != pyObject || v.kind != pyObject {
		return nil, errors.New("manifest.json 或 vocab.json 不是物件")
	}
	files := m.get("files")
	if files == nil || files.kind != pyObject {
		return nil, errors.New("manifest.json 沒有 files")
	}
	v.set("dictionary_sha256", pyStr(dictionary))
	if m.get("dictionary_sha256") != nil {
		m.set("dictionary_sha256", pyStr(dictionary))
	}
	vocabOut := dumpPyJSON(v)
	sum := sha256.Sum256(vocabOut)
	files.set("vocab.json", pyStr(hex.EncodeToString(sum[:])))
	return map[string][]byte{"manifest.json": dumpPyJSON(m), "vocab.json": vocabOut}, nil
}

// checkPaired 核對配對結果等於 pairing.lock 記的大小與雜湊。
func checkPaired(p Pairing, role string, out map[string][]byte) error {
	for _, n := range pairedFiles {
		want := p.Packages[role][n]
		sum := sha256.Sum256(out[n])
		if hex.EncodeToString(sum[:]) != want.SHA256 || int64(len(out[n])) != want.Size {
			return &Error{Code: CodePairing, Message: fmt.Sprintf("%s模型配對後的 %s 與配對清單不符", Label(role), n)}
		}
	}
	return nil
}
