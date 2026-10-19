import Foundation

// 系統的安全輸入（IsSecureEventInputEnabled）是全系統狀態，CGSession 回報的 PID 也只是當下前景的 App，
// 分不出是誰開的。背景 App 忘了關（密碼管理器、鎖定畫面後的 loginwindow 等）時若一律放行，所有 App 都
// 打不出中文、右 Shift 也沒反應（#71）。密碼欄位系統本來就切到 ABC，不會把按鍵交給輸入法；安全輸入中
// 仍會交給輸入法的是終端機的「安全鍵盤輸入」，只對這些 App 放行（#30：不送模型、不學習、不記錄）。
enum SecureInputPolicy {
    static let terminals: Set<String> = ["com.apple.Terminal", "com.googlecode.iterm2", "com.mitchellh.ghostty"]

    /// 查不到客戶端時視為終端機，維持放行。
    static func bypass(secure: Bool, client: String?) -> Bool {
        guard secure else { return false }
        guard let client else { return true }
        return terminals.contains(client)
    }
}
