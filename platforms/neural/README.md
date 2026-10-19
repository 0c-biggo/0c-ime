# 神經模型 runtime

完整桌面套件於系統字典旁提供 `neural/`，由 `0c-server` 依設定啟動 worker。神經選字預設開啟；補標點為 Beta、預設關閉。

`runtime/sequence_runtime.py` 與 `runtime/punct_runtime.py` 是 stdio worker，依賴 numpy 與 onnxruntime。固定模型版本、授權及系統字典配對要求見 [模型說明](../../models/README.md)。

## 使用發行版提供的 Python

從完整 tarball 根目錄啟動已編譯的 server：

```sh
build/0c-server -data assets/data \
  -neural-python /usr/bin/python3 \
  -neural-package models/select -neural-worker platforms/neural/runtime/sequence_runtime.py \
  -punct-package models/punct -punct-worker platforms/neural/runtime/punct_runtime.py
```

系統 Python 須已安裝 numpy 與 onnxruntime；server 依使用者設定啟動模型。補標點仍預設關閉。安裝時依發行版目錄配置調整上述路徑，並保留字典與模型的固定配對。

## 官網可攜套件

```sh
python3 scripts/prepare_neural.py --platform linux-amd64 \
  --select models/select --punct models/punct \
  --dictionary assets/data --out build/neural-linux-amd64
```

Windows 使用 `windows-amd64`，Apple Silicon 使用 `macos-arm64`。工具下載並校驗固定版本的 Python 與 wheel，核對模型雜湊及字典綁定；快取在 `build/cache/neural`。完整 tarball 已附上現成字典與配對模型，可直接使用上例。

以 `--neural` 將輸出目錄交給平台打包工具，並用同一份字典作為 `--data`。發行版離線建置應自行提供相依套件，不直接使用會下載 runtime 的可攜套件流程。

worker 必須以 `-I -B -X utf8` 執行；安裝後檔案不得在執行時改變。換模型須同步更換配對字典並重新產生 runtime，不手動改寫 manifest。
