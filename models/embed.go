// Package models 帶著神經模型的上游固定清單（#180）。
//
//   - models.lock.json：Hugging Face 上游模型（repo、revision、每個檔案的大小與 SHA-256）；與 0c-ime 的
//     models/models.lock.json 相同。0c-update 編進這一份，下載後逐檔核對；它的 SHA-256 就是 pairing.lock.json
//     的 models_lock_sha256。
//   - upstream/<select|punct>/manifest.json、vocab.json：上游的配對前 metadata（大小與雜湊記在 models.lock），
//     發版時依出貨字典產生 pairing.lock.json（scripts/package_release.py），也是 Go 版配對的測試輸入。
//   - equivalent.json：與 models.lock 的 model.onnx 等價的其他雜湊（0.2.2 以前官網套件內建的內部匯出，權重相同、
//     只差匯出資訊）。models.lock 是上游原檔不改；以模型雜湊判斷「是不是上游那一份」的地方兩個都認。
//
// 權重（model.onnx）不在 repo 裡：使用者在設定程式按下載時才從 Hugging Face 取得；官網套件發版時由
// scripts/hf_models.py 取得同一份原檔、配對後內建。
package models

import _ "embed"

// Lock 是 models.lock.json 的原始位元組（雜湊要跟 pairing.lock 的 models_lock_sha256 對得上，不可重新排版）。
//
//go:embed models.lock.json
var Lock []byte

// Equivalent 是 equivalent.json 的原始位元組。
//
//go:embed equivalent.json
var Equivalent []byte
