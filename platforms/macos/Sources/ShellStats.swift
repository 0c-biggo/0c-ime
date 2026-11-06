import Foundation

/// 輸入法外殼的診斷計數（#266）：喚醒後打不出中文時，分辨按鍵有沒有進到輸入法。
/// 只記次數、時間與浮動組字（#84）用在哪些 App（bundle ID），不記按鍵、文字或欄位內容。
/// 寫進 ~/Library/Application Support/0c/input-method-stats.json，由 0c-update status -json -diagnostics
/// 附在「匯出狀態」的 shell.input_method_stats。合併寫入：有變化後最多每 writeDelay 秒寫一次。
final class ShellStats {
    static let shared = ShellStats(path: FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/Application Support/0c/input-method-stats.json"))
    static let fileName = "input-method-stats.json"
    static let maxApps = 20

    struct Counts: Equatable {
        var activations = 0
        var deactivations = 0
        var keys = 0
        var activationsSinceWake = 0
        var keysSinceWake = 0
        var lastActivate: Date?
        var lastKey: Date?
        var lastWake: Date?
        var lastSleep: Date?
        var floatingRescues = 0
        var floatingFields = 0
        var floatingApps: [String: Int] = [:]
    }

    private(set) var counts = Counts()
    private let path: URL
    private let started: Date
    private let now: () -> Date
    private let writeDelay: TimeInterval
    private var pending: Timer?

    init(path: URL, now: @escaping () -> Date = Date.init, writeDelay: TimeInterval = 5) {
        self.path = path
        self.now = now
        self.started = now()
        self.writeDelay = writeDelay
    }

    func noteActivate() {
        counts.activations += 1
        counts.activationsSinceWake += 1
        counts.lastActivate = now()
        changed()
    }

    func noteDeactivate() {
        counts.deactivations += 1
        changed()
    }

    /// handle(_:client:) 收到的 keyDown（不含 Shift 等修飾鍵的 flagsChanged）。
    func noteKey() {
        counts.keys += 1
        counts.keysSinceWake += 1
        counts.lastKey = now()
        changed()
    }

    func noteWake() {
        counts.lastWake = now()
        counts.activationsSinceWake = 0
        counts.keysSinceWake = 0
        changed(soon: true)
    }

    func noteSleep() {
        counts.lastSleep = now()
        changed(soon: true)
    }

    /// 浮動組字：rescue 是一段組字被 App 結束、改畫在面板；field 是欄位改成一開始就用浮動。
    func noteFloating(bundle: String?, field: Bool) {
        if field { counts.floatingFields += 1 } else { counts.floatingRescues += 1 }
        let id = bundle.flatMap { $0.isEmpty ? nil : String($0.prefix(128)) } ?? "unknown"
        if counts.floatingApps[id] != nil || counts.floatingApps.count < Self.maxApps {
            counts.floatingApps[id, default: 0] += 1
        }
        changed(soon: true)
    }

    private func changed(soon: Bool = false) {
        if soon {
            pending?.invalidate()
            pending = nil
            write()
            return
        }
        guard pending == nil else { return }
        pending = Timer.scheduledTimer(withTimeInterval: writeDelay, repeats: false) { [weak self] _ in
            self?.pending = nil
            self?.write()
        }
    }

    func json() -> [String: Any] {
        let format = ISO8601DateFormatter()
        var out: [String: Any] = [
            "schema": 1,
            "pid": Int(ProcessInfo.processInfo.processIdentifier),
            "started_at": format.string(from: started),
            "written_at": format.string(from: now()),
            "activations": counts.activations,
            "deactivations": counts.deactivations,
            "keys": counts.keys,
            "activations_since_wake": counts.activationsSinceWake,
            "keys_since_wake": counts.keysSinceWake,
            "floating": ["rescues": counts.floatingRescues, "fields": counts.floatingFields, "apps": counts.floatingApps],
        ]
        if let version = Bundle.main.object(forInfoDictionaryKey: "ZCBuildVersion") as? String { out["version"] = version }
        for (key, date) in [("last_activate_at", counts.lastActivate), ("last_key_at", counts.lastKey),
                            ("last_wake_at", counts.lastWake), ("last_sleep_at", counts.lastSleep)] {
            if let date { out[key] = format.string(from: date) }
        }
        return out
    }

    func write() {
        guard let data = try? JSONSerialization.data(withJSONObject: json(), options: [.sortedKeys]) else { return }
        try? FileManager.default.createDirectory(at: path.deletingLastPathComponent(), withIntermediateDirectories: true,
                                                 attributes: [.posixPermissions: 0o700])
        try? data.write(to: path, options: .atomic)
    }
}
