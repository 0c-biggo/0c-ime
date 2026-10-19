# E2E 測試用發行金鑰

`release-test.key` 是**只供 E2E 測試**的 ed25519 私鑰，對應的公鑰只編入 `-tags e2etest` 的測試版 `0c-update`（`internal/update/trust_e2etest.go`）。正式建置不信任這把金鑰，因此公開它不影響正式更新的安全性。正式發行私鑰不在版本庫內。

`feed-server.key` 與 `feed-server.pem` 是本機 HTTPS 測試伺服器的私鑰與憑證，使用 `test-ca.pem`。只有 `e2etest` 建置加入此測試 CA；一般建置使用系統信任根。`TestTestCATrustedOnlyInE2EBuild` 分別驗證一般建置拒絕、測試建置接受此憑證。這些公開測試金鑰不得用於正式服務。
