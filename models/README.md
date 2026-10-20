# 發佈模型

每個版本的 Release 另附 `0c-ime-models-<版本>.tar.xz`，內含兩個可直接搭配 `assets/data/` 使用的模型，解壓到原始碼 tarball 的同一處即補上本目錄的 `select/` 與 `punct/`（0.2.2 以前內附在原始碼 tarball）。模型沒有公開訓練程式與訓練資料，不屬於 DFSG-free 的原始碼 tarball，見 [DFSG 檢查](../legal/DFSG-free.md)。模型權重與版本固定，建置輸入法不需要訓練模型或重新產生字典；沒有模型時輸入法以統計選字運作。

| 目錄 | 模型 | 授權 |
|---|---|---|
| `select/` | [0c-sequence-d512](https://huggingface.co/Funmula/0c-sequence-d512)，D512 PF512／2026-09-26 | MIT |
| `punct/` | [0c-punct-d512](https://huggingface.co/Funmula/0c-punct-d512)，D512 e60k／2026-09-30 | MIT |

每個目錄包含 `model.onnx`、`vocab.json`、`manifest.json`、`LICENSE` 與上游 `README.md`。Git 只保存固定清單，權重由發佈工具加入模型 tarball，不使用 Git LFS。

## 字典配對

[models.lock.json](models.lock.json) 記錄上游公開套件的 commit、檔案大小與 SHA-256；[pairing.lock.json](pairing.lock.json) 固定本版字典 manifest 的 SHA-256，以及配對後的模型 metadata 雜湊。模型 tarball 中的模型已完成配對，使用者不用執行配對工具。

發佈組裝時只更新 `dictionary_sha256` 與對應 vocab 檔案雜湊；ONNX 權重、token 順序、注音映射與推論參數均維持固定上游版本。模型卡與授權保留上游內容，本版字典綁定以配對清單為準。來源字典不符合公開檢查、固定雜湊或配對清單時即拒絕交付。

字典或模型更新，須由維護者重新驗證並更新固定清單；不在一般建置或安裝時任意改寫綁定。遇到模型不支援的字或讀音，仍依 runtime 的既有規則退回統計選字。

推論 worker 在 `platforms/neural/runtime/`。發行版提供 Python、NumPy 與 ONNX Runtime 執行環境；兩個 tarball 都不攜帶可攜 Python、wheel、訓練 checkpoint 或訓練環境。啟動方式見 [神經推論](../platforms/neural/README.md)。
