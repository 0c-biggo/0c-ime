# 打包與發佈

給發行版維護者與本專案的發佈者。一般使用者請到 [官網下載](https://0c.biggo.com/download/)。

## 原始碼與模型 tarball

每個版本的 [GitHub Release](https://github.com/0c-biggo/0c-ime/releases) 提供 `0c-ime-<版本>.tar.xz`、`0c-ime-models-<版本>.tar.xz` 與 `SHA256SUMS`。

- `0c-ime-<版本>.tar.xz` 交付程式原始碼與可直接使用的統計字典，符合 DFSG（見 [DFSG 檢查](legal/DFSG-free.md)），可編譯出與官網同版本的輸入法，不需要重新產生統計資料。
- `0c-ime-models-<版本>.tar.xz` 是兩個已配對的神經模型。模型沒有公開訓練程式與訓練資料，不屬於 DFSG-free 的內容，因此分開提供；解壓到同一處即補上 `models/select/` 與 `models/punct/`。沒有模型時輸入法以統計選字運作。

| tarball 路徑 | 內容 |
|---|---|
| `assets/data/` | `lexicon.bin`、`lm.bin`、manifest、NOTICE、資料報告與英文字典子包 |
| `SOURCE-RELEASE.json` | 檔案清單、tag／commit 與固定清單 |
| 其他目錄 | Go 核心、平台外殼、推論 worker、建置工具與測試 |
| 模型 tarball：`models/select/`、`models/punct/` | 已配對的 ONNX 權重、vocab、manifest、模型卡與授權 |
| 模型 tarball：`MODELS-RELEASE.json` | 模型檔案清單，以及對應的 tag／commit、模型與配對 lock、字典 manifest 雜湊 |

```sh
sha256sum -c SHA256SUMS
tar -xJf 0c-ime-<版本>.tar.xz
tar -xJf 0c-ime-models-<版本>.tar.xz   # 選用：神經選字與補標點
```

0.2.2 以前的版本只有一個 tarball，模型內附在 `models/` 裡；0.2.1 以前以 `ime-oss-<版本>.tar.xz` 為名。從 git clone 或 Source code 壓縮檔開始時，`make assets` 會下載同版本的 tarball，驗證後把 `assets/data/` 與兩個模型放進原始碼目錄；`make assets MODELS=0` 只取字典。大型 bigram TSV 另見 [從原始碼重建字典](#從原始碼重建字典)；原始語料、checkpoint 與訓練環境不附入。

## 發行版整合

解壓原始碼 tarball，在根目錄執行：

```sh
sha256sum -c SHA256SUMS
tar -xJf 0c-ime-<版本>.tar.xz
cd 0c-ime-<版本>
mkdir -p build
go build -trimpath -ldflags '-X main.buildVersion=<版本>' -o build/0c-server ./cmd/0c-server
go build -trimpath -o build/0c-cli ./cmd/0c-cli
build/0c-server -check-data -data assets/data
build/0c-cli -data assets/data
```

Go module 相依須預先備妥，之後可用 `GOPROXY=off GOSUMDB=off` 離線建置；不需要 `.git` 或重新編譯 bigram。平台外殼依各平台 README 建置。

要把外殼（Fcitx5、IBus）、引擎、設定程式與字典整套裝到系統目錄時，用根目錄的 `make build PREFIX=/usr VERSION=<版本>` 與 `make install PREFIX=/usr VERSION=<版本> DESTDIR=<暫存根目錄>`；這種安裝由套件管理員更新，不會自己檢查或下載更新。變數、安裝路徑與建置／執行相依見 [Linux 系統安裝](platforms/linux-system/README.md)。

神經選字與補標點使用模型 tarball 的 `models/select/`、`models/punct/`，可另外打包成選用套件（例如 Debian 的 non-free）；版本與配對檢查見 [模型說明](models/README.md)，worker 與系統 Python、NumPy、ONNX Runtime 的整合方式見 [神經推論](platforms/neural/README.md)。工具鏈與執行相依套件由發行版自行提供。逐檔的著作權人與授權以 DEP-5 格式列在 [legal/copyright](legal/copyright)，可直接作為 `debian/copyright` 的基礎。

## 從原始碼重建字典

內附的 `lexicon.bin`、`lm.bin` 可以從公開內容重建：Git 內的詞庫與詞頻，加上 [0c-ime-data](https://github.com/0c-biggo/0c-ime-data) Release 的四個詞對統計檔（約 307 MB）。

```sh
tar -xJf 0c-ime-bigram-20261019.tar.xz
python3 scripts/build_release_data.py --bigram-dir 0c-ime-bigram-20261019/bigram --out build/release-data
```

輸出的 `build/release-data/data/` 與 `assets/data/` 位元組相同。各版本使用的統計檔版本見 0c-ime-data 的 README。

## 組裝 Release tarball

Git repo 保存 [字典 lock](data/dictionary.lock.json)、[上游模型 lock](models/models.lock.json) 與 [配對 lock](models/pairing.lock.json)；二進位字典由發佈工具加入原始碼 tarball，配對後的模型另外打成模型 tarball。

先提交原始碼與固定清單、建立版本 tag，再用已驗證的現成字典組裝。輸出目錄須尚未存在：

```sh
python3 scripts/source_release.py build --tag v<版本> --data-dir /path/to/compiled-data \
  --out build/source-release/<版本>
python3 scripts/source_release.py verify build/source-release/<版本>/0c-ime-<版本>.tar.xz \
  --models build/source-release/<版本>/0c-ime-models-<版本>.tar.xz
(cd build/source-release/<版本> && sha256sum -c SHA256SUMS)
```

工具只讀取 tag 的 Git archive，不納入未提交改動。字典必須符合固定清單與公開來源檢查，並與模型配對清單一致；模型權重依上游 commit、大小與 SHA-256 取得，只依已驗證的配對清單產生綁定 metadata。模型快取完整時可加 `--offline`，不連網組裝。

上一版的原始碼 tarball 可用 `source_release.py extract-data` 取出字典重用。repo variable `DICTIONARY_SOURCE_URL` 設定後，tag 的 CI 會從該網址取回字典、依當前 tag 的 lock 校驗並組裝兩個 tarball，解壓後離線建置與載入字典成功，才一起上傳到 Release 草稿；沒有設定時這個工作會略過。

## 更新字典

詞庫、編譯參數或 `0c-compile` 的排序改變時，內附字典要重建，兩個 lock 一起更新：

1. 以 `scripts/build_release_data.py --bigram-dir <符合 bigram lock 的統計檔>` 重建字典。
2. 更新 `data/dictionary.lock.json` 的檔案大小與 SHA-256。
3. 更新 `models/pairing.lock.json` 的字典 manifest 雜湊與配對 metadata，再以 `scripts/pair_models.py` 確認配對；ONNX 權重、token 順序與推論設定不變。
4. 以新字典在本機組裝 Release tarball，`DICTIONARY_SOURCE_URL` 改指向新的原始碼 tarball。
