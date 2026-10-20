# 打包與發佈

給發行版維護者與本專案的發佈者。一般使用者請到 [官網下載](https://0c.biggo.com/download/)。

## 完整 tarball

每個版本的 [GitHub Release](https://github.com/0c-biggo/ime-oss/releases) 提供 `ime-oss-<版本>.tar.xz` 與 `SHA256SUMS`。tarball 交付程式原始碼、可直接使用的統計字典與兩個已配對模型，可編譯出與官網同版本的輸入法，不需要重新產生統計資料或訓練模型。

| tarball 路徑 | 內容 |
|---|---|
| `assets/data/` | `lexicon.bin`、`lm.bin`、manifest、NOTICE、資料報告與英文字典子包 |
| `models/select/`、`models/punct/` | 已配對的 ONNX 權重、vocab、manifest、模型卡與授權 |
| `SOURCE-RELEASE.json` | 檔案清單、tag／commit 與固定清單 |
| 其他目錄 | Go 核心、平台外殼、推論 worker、建置工具與測試 |

GitHub 自動附上的 Source code ZIP／tar.gz 不含字典與模型，請用上面的完整 tarball。大型 bigram TSV、原始語料、checkpoint 與訓練環境不附入。

## 發行版整合

解壓完整 tarball，在根目錄執行：

```sh
sha256sum -c SHA256SUMS
tar -xJf ime-oss-<版本>.tar.xz
cd ime-oss-<版本>
mkdir -p build
go build -trimpath -ldflags '-X main.buildVersion=<版本>' -o build/0c-server ./cmd/0c-server
go build -trimpath -o build/0c-cli ./cmd/0c-cli
build/0c-server -check-data -data assets/data
build/0c-cli -data assets/data
```

Go module 相依須預先備妥，之後可用 `GOPROXY=off GOSUMDB=off` 離線建置；不需要 `.git` 或重新編譯 bigram。平台外殼依各平台 README 建置。

神經選字與補標點直接使用 `models/select/`、`models/punct/`；版本與配對檢查見 [模型說明](models/README.md)，worker 與系統 Python、NumPy、ONNX Runtime 的整合方式見 [神經推論](platforms/neural/README.md)。工具鏈、執行相依套件與安裝規則由發行版自行提供。

## 組裝完整 tarball

Git repo 保存 [字典 lock](data/dictionary.lock.json)、[上游模型 lock](models/models.lock.json) 與 [配對 lock](models/pairing.lock.json)；二進位字典與模型由發佈工具加入 tarball。

先提交原始碼與固定清單、建立版本 tag，再用已驗證的現成字典組裝。輸出目錄須尚未存在：

```sh
python3 scripts/source_release.py build --tag v<版本> --data-dir /path/to/compiled-data \
  --out build/source-release/<版本>
python3 scripts/source_release.py verify build/source-release/<版本>/ime-oss-<版本>.tar.xz
(cd build/source-release/<版本> && sha256sum -c SHA256SUMS)
```

工具只讀取 tag 的 Git archive，不納入未提交改動。字典必須符合固定清單與公開來源檢查，並與模型配對清單一致；模型權重依上游 commit、大小與 SHA-256 取得，只依已驗證的配對清單產生綁定 metadata。模型快取完整時可加 `--offline`，不連網組裝。

上一版的完整 tarball 可用 `source_release.py extract-data` 取出字典重用。repo variable `DICTIONARY_SOURCE_URL` 設定後，tag 的 CI 會從該網址取回字典、依當前 tag 的 lock 校驗並組裝，解壓後離線建置與載入字典成功，才上傳到 Release 草稿；沒有設定時這個工作會略過。

## 更新字典

詞庫、編譯參數或 `0c-compile` 的排序改變時，內附字典要重建，兩個 lock 一起更新：

1. 以 `scripts/build_release_data.py --bigram-dir <符合 bigram lock 的統計檔>` 重建字典。
2. 更新 `data/dictionary.lock.json` 的檔案大小與 SHA-256。
3. 更新 `models/pairing.lock.json` 的字典 manifest 雜湊與配對 metadata，再以 `scripts/pair_models.py` 確認配對；ONNX 權重、token 順序與推論設定不變。
4. 以新字典在本機組裝完整 tarball，`DICTIONARY_SOURCE_URL` 改指向新的 Release 附件。
