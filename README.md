# 靈犀注音（0c）開源版

`ime-oss` 包含輸入法核心、平台外殼、設定程式、詞庫工具與回歸測試，提供 Linux 發行版維護者及自行建置的開發者使用。

支援注音組字、候選選字、個人詞彙、Tab 補全、中英混打與模糊音。統計引擎負責候選；神經選字預設開啟，模型不可用時退回統計引擎。自動補標點為 Beta，預設關閉。

| 平台 | 外殼與建置說明 |
|---|---|
| Linux Fcitx5 | [C++ 外殼](platforms/linux-fcitx5/README.md) |
| Linux IBus | [Go 外殼](platforms/linux-ibus/README.md) |
| macOS | [InputMethodKit 外殼](platforms/macos/README.md) |
| Windows | [TSF 外殼](platforms/windows/README.md) |

## 開始開發

核心需要 Go 1.26 以上；測試與發佈工具使用 Python 3.11 以上。首次建置須先備妥 `go.mod`／`go.sum` 所列模組。以下指令在 repo 根目錄執行，不安裝或重啟桌面輸入法：

```sh
go test ./...
go vet ./...
go run ./cmd/0c-compile -public -bigram '' -out build/public-data
go run ./cmd/0c-cli -data build/public-data
```

這會從 repo 內的詞庫與 unigram 編譯公開資料包，並檢查實際使用來源的版本、可散布標記與授權聲明。它沒有 bigram，不等同完整出貨字典，也尚未與內附神經模型配對。詞庫格式與完整資料限制見 [詞庫資料](data/README.md)，目錄配置見 [專案範圍](REPOSITORIES.md)。

平台外殼另需各自的 C++／Rust／Swift 工具鏈與系統函式庫。設定程式建置入口見 [設定程式](platforms/settings/README.md)。

## 檢查與測試

```sh
python3 scripts/check_public_tree.py
go test ./...
go vet ./...
make -C platforms/linux-fcitx5 test
make test-upgrade
make test-release
```

Fcitx5 測試需要 Linux 與 C++ 建置工具。設定程式和各平台原生建置另依其 README 驗證。一般測試不修改日常桌面或個人詞庫。

`check_public_tree.py` 檢查 Git 已追蹤及可加入的路徑；原始碼發佈工具也檢查 tag 的完整 Git tree。被 `export-ignore` 隱藏的私人檔案仍會被拒絕。提交前仍須審閱內容；路徑檢查不等同完整機密掃描。

## 原始碼發佈

GitHub Release 提供 `ime-oss-<版本>.tar.xz` 與 `SHA256SUMS`。tarball 交付程式原始碼、可直接使用的統計字典與兩個已配對模型，供維護者編譯同一版本的輸入法；不需要重新生成統計資料或訓練模型。

| tarball 路徑 | 內容 |
|---|---|
| `assets/data/` | `lexicon.bin`、`lm.bin`、manifest、NOTICE、資料報告與英文字典子包 |
| `models/select/`、`models/punct/` | 已配對的 ONNX 權重、vocab、manifest、模型卡與授權 |
| 其他原始碼目錄 | Go 核心、平台外殼、推論 worker、建置工具與測試 |

大型 bigram TSV、原始語料、checkpoint 與訓練環境不附入 tarball。Git repo 保存 [字典 lock](data/dictionary.lock.json)、[上游模型 lock](models/models.lock.json) 與 [配對 lock](models/pairing.lock.json)；二進位字典與模型由發佈工具加入 tarball。GitHub 自動產生的 Source code ZIP／tar.gz 不包含這些產物。

維護者先提交原始碼與固定清單、建立版本 tag，再用已驗證的現成字典組裝。以下以首版 `v0.2.0` 為例；輸出目錄須尚未存在：

```sh
python3 scripts/source_release.py build --tag v0.2.0 --data-dir /path/to/compiled-data \
  --out build/source-release/0.2.0
python3 scripts/source_release.py verify build/source-release/0.2.0/ime-oss-0.2.0.tar.xz
(cd build/source-release/0.2.0 && sha256sum -c SHA256SUMS)
```

工具只讀取 tag 的 Git archive，不納入未提交改動。字典必須符合固定清單及公開來源檢查，且與模型配對清單一致；不刪除來源紀錄、不重建字典。模型權重依上游 commit、大小與 SHA-256 取得，僅依已驗證配對清單產生綁定 metadata。模型快取完整時可加 `--offline`，不連網組裝。

首次完整 tarball 在本機建立；尚未設定 `DICTIONARY_SOURCE_URL` 時，tag 的自動組裝工作會略過。發布首版後，將 repo variable `DICTIONARY_SOURCE_URL` 設為該 tarball 的 HTTPS 下載網址。後續 tag CI 取回其中的現成字典、按當前 tag 的 lock 校驗，再組裝新 tarball；解壓後離線建置輸入法並載入字典成功，才上傳 tarball 與 SHA256SUMS 至 Release 草稿。字典版本更新時，以新字典在本機建立完整 tarball，再更新網址。

## 發行版整合

解壓完整 tarball，在根目錄執行：

```sh
mkdir -p build
go build -trimpath -o build/0c-server ./cmd/0c-server
go build -trimpath -o build/0c-cli ./cmd/0c-cli
build/0c-server -check-data -data assets/data
build/0c-cli -data assets/data
```

Go module 相依須預先備妥，之後可用 `GOPROXY=off GOSUMDB=off` 離線建置。上例編譯輸入法程式，直接使用內附字典，不需要 `.git` 或重新編譯 bigram。平台外殼另依各平台 README 建置。

神經選字與補標點直接使用 `models/select/`、`models/punct/`；版本與配對檢查見 [模型說明](models/README.md)，worker 及系統 Python、NumPy、ONNX Runtime 整合方式見 [神經推論](platforms/neural/README.md)。發行版自行提供工具鏈、執行相依套件與暫存安裝規則。

## 完整安裝包

`.exe`、`.deb`、`.pkg` 等使用者安裝包由官網提供；官網上傳與索引續簽流程不在本 repo。

本 repo 保留三平台的本機打包工具。除配對字典、模型與工具鏈外，須以 `--release-docs /path/to/package-documents` 提供一般檔案 `INSTALL.md`、`PRIVACY.md`、`UPDATING.md`。文件必須非空，會原樣附入安裝包，不會從本機私人文件目錄自動取得，也不會刪除政策狀態標記。各平台選項可用 `python3 scripts/package_release.py --help`、`package_windows.py --help` 或 `package_macos.py --help` 查詢。

`--preview` 僅供測試，不代表獲准公開散布。公開資料包要求 `0c-compile -public`；打包不刪除來源、不改寫 NOTICE，也不重新綁定模型。`legal/TERMS.md` 已核可；隱私政策另行核可，一般使用者安裝包的政策核可與發布檢查由發行者負責。

## 參與開發與授權

- [參與開發](CONTRIBUTING.md)
- [專案範圍](REPOSITORIES.md)
- [授權範圍](legal/README.md)：自有程式採 AGPL-3.0-only，另可洽談商業授權；資料、模型與第三方元件依各自授權。
