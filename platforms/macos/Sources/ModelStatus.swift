import Foundation

// 選單顯示實際模型狀態；獨立連線只查 health，不啟動引擎，也不碰組字 session。
// 查到的 info 也是外殼快取的切換鍵設定來源之一（EngineSession.update）。
final class ModelStatusMonitor {
    static let shared = ModelStatusMonitor()
    private var timer: Timer?
    private var querying = false
    private var runtime = "unknown"
    private var info: [String: String] = [:]

    var labels: [String] {
        ["神經選字：" + Self.label(info["selection_state"], runtime: runtime, fallback: "統計引擎"),
         "補標點：" + Self.label(info["punctuation_state"], runtime: runtime, fallback: "原文提交")]
    }

    static func label(_ state: String?, runtime: String, fallback: String) -> String {
        if runtime == "not_running" { return "引擎未執行" }
        if runtime == "unavailable" { return "狀態無法確認" }
        switch state {
        case "ready": return "已就緒"
        case "loading": return "載入中，暫用" + fallback
        case "fallback": return "已退回" + fallback
        case "disabled_by_setting": return "依設定關閉"
        case "not_installed": return "未附模型，使用" + fallback
        case "closed": return "已停止，使用" + fallback
        default: return "狀態未知"
        }
    }

    func start() {
        refresh()
        timer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in self?.refresh() }
    }

    // 系統每次組字更新後都會要一次選單（一個按鍵一次，#148）；選單觸發的查詢至少隔這麼久，其餘靠每 5 秒的輪詢。
    static let menuRefreshInterval: TimeInterval = 2
    private var lastMenuRefresh = -TimeInterval.infinity

    func refreshForMenu(now: TimeInterval = ProcessInfo.processInfo.systemUptime) {
        guard now - lastMenuRefresh >= Self.menuRefreshInterval else { return }
        lastMenuRefresh = now
        refresh()
    }

    // 所有快取讀寫都在主執行緒；連線／讀取在背景，選單與打字不等待模型。
    func refresh() {
        guard !querying else { return }
        querying = true
        let path = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Caches/0c/engine.sock").path
        DispatchQueue.global(qos: .utility).async {
            let connection = EngineConnection(path: path, timeout: 0.5)
            var runtime = "not_running"
            var info: [String: String] = [:]
            if connection.ready() {
                do {
                    info = try connection.call(Envelope(op: "health")).info ?? [:]
                    runtime = "running"
                } catch { runtime = "unavailable" }
            }
            connection.disconnect()
            DispatchQueue.main.async {
                self.runtime = runtime
                self.info = info
                self.querying = false
                // 切換鍵等設定也跟著更新，單按 Shift 時不必在主執行緒上問引擎（#148）。
                if runtime == "running" { EngineSession.update(from: info) }
            }
        }
    }
}
