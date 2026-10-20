import AppKit

// The updater's JSON contains only version/preferences/pending status. Fetch it
// asynchronously: building an IMK menu must never wait for IPC or a download.
struct UpdateMenuStatus: Decodable {
    struct Target: Decodable { var version: String; var sha256: String? }
    struct Blocked: Decodable { var reason: String }
    var installed: String?
    var mode: String?
    var stage: String?
    var target: Target?
    var blocked: Blocked?
    var action: String?
    var paused_until: String?
    var notice_snoozed_until: String?
    /// features.vocab：0.2.0「我的詞彙」的入口（0.2.0 起恆為 true）。舊版 0c-update 沒有這個欄位，視為關閉。
    var features: Features?
    /// settings.output_simplified（#169，0.2.1 起的 0c-update）：引擎不能即時切換時，選單的「輸出簡體字」照設定檔打勾。
    var settings: Settings?

    /// 寬鬆解析：output_simplified 不是布林時當成不知道，不能讓整份狀態解析失敗。
    struct Settings: Decodable {
        var outputSimplified: Bool?
        private enum CodingKeys: String, CodingKey { case outputSimplified = "output_simplified" }
        init(from decoder: Decoder) throws {
            outputSimplified = try? decoder.container(keyedBy: CodingKeys.self).decodeIfPresent(Bool.self, forKey: .outputSimplified)
        }
    }

    /// 寬鬆解析：features 不是物件、vocab 不是布林都當成關閉，不能讓整份狀態解析失敗。
    struct Features: Decodable {
        var vocab = false
        private enum CodingKeys: String, CodingKey { case vocab }
        init(from decoder: Decoder) throws {
            vocab = (try? decoder.container(keyedBy: CodingKeys.self).decodeIfPresent(Bool.self, forKey: .vocab)) ?? false
        }
    }

    var vocab: Bool { features?.vocab ?? false }
    var outputSimplified: Bool? { settings?.outputSimplified ?? nil }

    /// 外殼自己的完整版本：打包時寫進 Info.plist 的 ZCBuildVersion；開發建置沒有。
    static let shellVersion = Bundle.main.object(forInfoDictionaryKey: "ZCBuildVersion") as? String

    var label: String { label(shell: Self.shellVersion) }

    func label(shell: String?) -> String {
        let version = installed.flatMap { $0.isEmpty ? nil : $0 } ?? "版本待確認"
        switch blocked?.reason {
        case "needs_relogin":
            // 正在執行的外殼就是更新目標：已經生效，只是更新器還沒確認，不要叫使用者再登出一次（#77）。
            if let shell, !shell.isEmpty, shell == target?.version { return "靈犀注音 \(shell)" }
            return "靈犀注音 \(version) · 重新登入後生效"
        case "health_failed", "install_failed": return "靈犀注音 \(version) · 更新未完成"
        default:
            if let target = target { return "有新版本 \(target.version)" }
            return "靈犀注音 \(version)"
        }
    }

    var pendingReason: String? {
        guard let reason = blocked?.reason else { return nil }
        switch reason {
        case "needs_consent", "needs_relogin": return nil
        case "engine_busy": return "請先完成組字，再完成更新"
        case "waiting_idle": return "使用中，等待閒置"
        case "low_battery": return "電量不足，接上電源後再試"
        case "metered_network": return "等待可用網路"
        case "download_failed": return "下載未完成，稍後會再試"
        case "disk_full": return "磁碟空間不足，清出空間後再試"
        case "health_failed", "install_failed": return "更新未完成，請稍後再試"
        default: return "更新暫時受阻"
        }
    }
}

final class UpdateMenuMonitor {
    typealias Runner = ([String], @escaping (Result<Data, Error>) -> Void) -> Void
    static let shared = UpdateMenuMonitor()
    private let runner: Runner
    private var timer: Timer?
    private var querying = false
    private(set) var busy = false
    private(set) var status: UpdateMenuStatus?
    private(set) var failed = false
    private var operationFailed = false
    private var openingSettings = false

    init(runner: @escaping Runner = UpdateMenuMonitor.runUpdater) { self.runner = runner }

    func start() {
        refresh()
        timer = Timer.scheduledTimer(withTimeInterval: 15, repeats: true) { [weak self] _ in self?.refresh() }
    }

    // 系統每次組字更新後都會要一次選單（一個按鍵一次，#148）；選單觸發的查詢至少隔這麼久才再開一次 0c-update，
    // 其餘靠每 15 秒的輪詢與操作完成後的重新整理。
    static let menuRefreshInterval: TimeInterval = 2
    private var lastMenuRefresh = -TimeInterval.infinity

    func refreshForMenu(now: TimeInterval = ProcessInfo.processInfo.systemUptime) {
        guard now - lastMenuRefresh >= Self.menuRefreshInterval else { return }
        lastMenuRefresh = now
        refresh()
    }

    func refresh() {
        guard !querying && !busy else { return }
        querying = true
        runner(["status", "-json"]) { result in
            DispatchQueue.main.async {
                self.querying = false
                switch result {
                case .success(let data):
                    do { self.status = try JSONDecoder().decode(UpdateMenuStatus.self, from: data); self.failed = false }
                    catch { self.status = nil; self.failed = true }
                case .failure:
                    self.status = nil
                    self.failed = true
                }
            }
        }
    }

    func append(to menu: NSMenu, target: AnyObject, action: Selector) {
        func add(_ title: String, _ args: [String]? = nil, checked: Bool = false) {
            let item = NSMenuItem(title: title, action: args == nil ? nil : action, keyEquivalent: "")
            item.target = target
            item.representedObject = args
            // Opening the settings program does not touch update state, so it stays usable while an update runs.
            item.isEnabled = args != nil && (Self.opensSettings(args!) || !busy)
            item.state = checked ? .on : .off
            menu.addItem(item)
        }
        add("設定…", Self.settingsCommand)
        // 「我的詞彙…」只在 features.vocab 為真時出現（#125）。
        if status?.vocab == true { add("我的詞彙…", Self.vocabCommand) }
        menu.addItem(.separator())
        add(busy ? "正在處理更新…" : status?.label ?? "更新狀態待確認")
        if failed || operationFailed { add("更新操作未完成，請稍後再試") }
        if let reason = status?.pendingReason { add(reason) }
        if let text = status?.notice_snoozed_until {
            let iso = ISO8601DateFormatter()
            iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = iso.date(from: text) ?? ISO8601DateFormatter().date(from: text) {
                let format = DateFormatter()
                format.dateFormat = "MM/dd HH:mm"
                add("更新提醒稍後至 \(format.string(from: date))")
            }
        }
        if status?.action == "finish", let version = status?.target?.version,
           let hash = status?.target?.sha256, let command = Self.finishCommand(version: version, sha256: hash) {
            add("立即安裝 \(version)", command)
        }
        add("檢查更新", ["auto", "-now", "-notify"])
        menu.addItem(.separator())
        add("自動更新", ["mode", "auto"], checked: status?.mode == "auto")
        add("下載後詢問", ["mode", "ask"], checked: status?.mode == "ask")
        add("手動更新", ["mode", "manual"], checked: status?.mode == "manual")
        if status?.paused_until != nil {
            add("恢復更新", ["resume"])
        } else {
            add("暫停更新 7 天", ["pause", "7"])
        }
    }

    // Only fixed commands constructed by this menu are accepted; never invoke a
    // shell or take executable paths from a status file.
    func perform(_ item: NSMenuItem) {
        guard let args = item.representedObject as? [String], Self.allowed(args) else { return }
        if Self.opensSettings(args) {
            // Not an update operation: no "processing" state, and a failure is reported by 0c-update itself.
            guard !openingSettings else { return }
            openingSettings = true
            runner(args) { _ in DispatchQueue.main.async { self.openingSettings = false } }
            return
        }
        guard !busy else { return }
        busy = true
        operationFailed = false
        runner(args) { result in
            DispatchQueue.main.async {
                self.busy = false
                if case .failure = result { self.operationFailed = true }
                self.refresh()
            }
        }
    }

    /// 「輸出簡體字」退回 0c-update 時的命令（#169，舊引擎）：寫設定、請引擎套用（交接重啟），並送通知。
    static func simplifiedCommand(_ on: Bool) -> [String] { ["set", "output-simplified", on ? "on" : "off", "-apply", "-notify"] }

    /// 引擎不能即時切換時的「輸出簡體字」：照 0c-update 執行，完成後重新讀狀態（勾選跟著設定檔）。
    func setSimplified(_ on: Bool) {
        let args = Self.simplifiedCommand(on)
        guard Self.allowed(args), !busy else { return }
        busy = true
        operationFailed = false
        runner(args) { result in
            DispatchQueue.main.async {
                self.busy = false
                if case .failure = result { self.operationFailed = true }
                self.refresh()
            }
        }
    }

    static let settingsCommand = ["open-settings"]
    static let vocabCommand = ["open-settings", "vocab"]
    static func opensSettings(_ args: [String]) -> Bool { args == settingsCommand || args == vocabCommand }

    // IMKit 呼叫選單動作時，sender 是 infoDictionary（"IMKCommandMenuItem" 即 kIMKCommandMenuItemName，對應被選的
    // NSMenuItem），不是 NSMenuItem 本身；項目的 representedObject 會跟著回來（2026-10-06 於 macOS 26 實測）。
    static func selectedItem(from sender: Any?) -> NSMenuItem? {
        if let info = sender as? [AnyHashable: Any] { return info["IMKCommandMenuItem"] as? NSMenuItem }
        return sender as? NSMenuItem
    }

    static func allowed(_ args: [String]) -> Bool {
        if args.count == 5, args[0] == "snooze", args[1] == "-target", args[3] == "-target-sha256" {
            return snoozeCommand(version: args[2], sha256: args[4]) == args
        }
        if args.count == 6, args[0] == "finish", args[1] == "-notify", args[2] == "-target", args[4] == "-target-sha256" {
            return finishCommand(version: args[3], sha256: args[5]) == args
        }
        return [settingsCommand, vocabCommand, ["auto", "-now", "-notify"], ["mode", "auto"],
                ["mode", "ask"], ["mode", "manual"], ["pause", "7"], ["resume"],
                simplifiedCommand(true), simplifiedCommand(false)].contains(args)
    }

    static func finishCommand(version: String, sha256: String) -> [String]? {
        guard version.utf8.count <= 128, version.range(of: #"^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$"#, options: .regularExpression) != nil,
              sha256.range(of: #"^[0-9a-f]{64}$"#, options: .regularExpression) != nil else { return nil }
        return ["finish", "-notify", "-target", version, "-target-sha256", sha256]
    }

    static func snoozeCommand(version: String, sha256: String) -> [String]? {
        guard finishCommand(version: version, sha256: sha256) != nil else { return nil }
        return ["snooze", "-target", version, "-target-sha256", sha256]
    }

    private static func runUpdater(_ args: [String], done: @escaping (Result<Data, Error>) -> Void) {
        DispatchQueue.global(qos: .utility).async {
            guard let executable = Bundle.main.executableURL?.deletingLastPathComponent().appendingPathComponent("0c-update") else {
                done(.failure(NSError(domain: "0c.update", code: 1))); return
            }
            let process = Process()
            let output = Pipe()
            process.executableURL = executable
            process.arguments = args
            process.standardInput = FileHandle.nullDevice
            process.standardOutput = output
            process.standardError = FileHandle.nullDevice
            do {
                try process.run()
                let deadline = DispatchWorkItem { if process.isRunning { process.terminate() } }
                if args == ["status", "-json"] {
                    DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 5, execute: deadline)
                }
                let data = output.fileHandleForReading.readDataToEndOfFile()
                process.waitUntilExit()
                deadline.cancel()
                if process.terminationStatus == 0 { done(.success(data)) }
                else { done(.failure(NSError(domain: "0c.update", code: Int(process.terminationStatus)))) }
            } catch { done(.failure(error)) }
        }
    }
}
