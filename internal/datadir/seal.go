package datadir

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// 隨套件出貨的字典以 AES-256-CTR 混淆，提高直接擷取的門檻。引擎在本機必須能讀，
// 金鑰隨程式出貨，這不是保密；manifest.json 維持明文與明文雜湊，字典配對不受影響。
var sealMagic = []byte("0cDATA\x00\x01")

var sealKey = [32]byte{
	0x6c, 0x69, 0x6e, 0x67, 0x78, 0x69, 0x2d, 0x7a, 0x68, 0x75, 0x79, 0x69, 0x6e, 0x2d, 0x30, 0x63,
	0x9e, 0x31, 0x5a, 0xc7, 0x04, 0xd2, 0x88, 0x1f, 0x6b, 0xe5, 0x40, 0x93, 0x27, 0xbc, 0x5d, 0x0a,
}

// SealedFiles 是資料包中會被混淆的檔案。
var SealedFiles = []string{"lexicon.bin", "lm.bin"}

// Seal 回傳混淆後的內容；已混淆的內容原樣回傳錯誤，避免重複處理。
func Seal(plain []byte) ([]byte, error) {
	if bytes.HasPrefix(plain, sealMagic) {
		return nil, errors.New("already sealed")
	}
	nonce := make([]byte, aes.BlockSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(sealKey[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(sealMagic)+len(nonce)+len(plain))
	copy(out, sealMagic)
	copy(out[len(sealMagic):], nonce)
	cipher.NewCTR(block, nonce).XORKeyStream(out[len(sealMagic)+len(nonce):], plain)
	return out, nil
}

// unseal 就地解開混淆內容；明文（舊資料包、研究用資料）原樣回傳。
func unseal(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, sealMagic) {
		return b, nil
	}
	head := len(sealMagic) + aes.BlockSize
	if len(b) < head {
		return nil, errors.New("sealed data truncated")
	}
	block, err := aes.NewCipher(sealKey[:])
	if err != nil {
		return nil, err
	}
	body := b[head:]
	cipher.NewCTR(block, b[len(sealMagic):head]).XORKeyStream(body, body)
	return body, nil
}

// SealDir 就地混淆資料包目錄中的字典檔（打包時使用）；manifest.json 不動。
func SealDir(dir string) error {
	names := append([]string(nil), SealedFiles...)
	if info, err := os.Lstat(filepath.Join(dir, "english", "words.bin")); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("英文資料包不是一般檔案")
		}
		names = append(names, "english/words.bin")
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		plain, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sealed, err := Seal(plain)
		if err != nil {
			return fmt.Errorf("%s：%w", name, err)
		}
		tmp := path + ".sealing"
		if err = os.WriteFile(tmp, sealed, 0o644); err != nil {
			return err
		}
		if err = os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	return nil
}
