import AppKit
import UserNotifications

// 通知授權（#350）。輸入法 App（com.biggo.inputmethod.0c）的所有通知都經過這裡：更新通知（0c --update-notice）、
// 背景執行被關閉（UpdateAgentMonitor），以及之後的其他提醒。
//
// 0.2.14 以前只用暫時授權（provisional）：不跳詢問，但使用者沒在通知中心按過「保留」時，通知只安靜進通知中心、
// 不跳橫幅；關掉自動更新的人靠這則通知得知有新版，看不到就等於沒通知。現在改成：
//   - 正式詢問一次（requestAuthorization 不帶 .provisional）：由常駐的輸入法程式在使用者正在用靈犀、剛停手、
//     沒有組字、螢幕沒鎖時問（NotificationPermissionMonitor）。macOS 的詢問是右上角的通知樣式提示（「選項 → 允許／不允許」），
//     不搶焦點，打字不會被打斷；仍避開組字中與剛按鍵的那一刻。已經是暫時授權的舊安裝同樣會問一次（26.7.3 實測：
//     暫時授權下正式詢問會跳提示，允許後成為 authorized、樣式為橫幅）。
//   - 背景的短命行程（排程發的更新通知）從不跳詢問：還沒決定時照舊以暫時授權安靜送進通知中心，已允許就以橫幅出現。
//   - 使用者按「不允許」或在系統設定關掉時不送；輸入法選單與設定程式的更新入口照常顯示，不靠通知。
// 系統對同一個 App 只詢問一次（之後的 requestAuthorization 不再跳提示），所以這裡只在暫時授權或還沒決定時問。
enum NotificationPermission {
    enum Status: String, Codable {
        case notDetermined = "not-determined", provisional, authorized, denied, ephemeral, unknown
    }

    static func status(_ s: UNAuthorizationStatus) -> Status {
        switch s {
        case .notDetermined: return .notDetermined
        case .provisional: return .provisional
        case .authorized: return .authorized
        case .denied: return .denied
        case .ephemeral: return .ephemeral
        @unknown default: return .unknown
        }
    }

    static func style(_ s: UNAlertStyle) -> String {
        switch s {
        case .banner: return "banner"
        case .alert: return "alert"
        case .none: return "none"
        @unknown default: return "unknown"
        }
    }

    /// 寫在 ~/Library/Application Support/0c/notification-permission.json，0c-update 匯出狀態摘要時附上（shell.notifications）。
    struct Record: Codable, Equatable {
        var status: Status
        var alert_style: String?
        var checked_at: Date
        var asked_at: Date?
        var asked_version: String?
        var asked_from: Status?
        var answer: String? // granted、denied 或錯誤訊息
    }

    /// 同一個版本只問一次；之前問過但沒有回答（關掉提示）的，換了版本而且隔 30 天以上才再問。
    static let reaskAfter: TimeInterval = 30 * 86400
    /// 剛停手多久才問：避開正在打字的那一刻。
    static let quiet: TimeInterval = 3
    /// 停手太久的不問：使用者可能已經離開，提示沒人看到。
    static let recent: TimeInterval = 120

    /// 現在要不要正式詢問。sinceKey：這個輸入法程式最後一次收到按鍵到現在多久（從沒收到為 nil：使用者還沒用靈犀打字）。
    static func shouldAsk(status: Status, record: Record?, version: String, composing: Bool, sinceKey: TimeInterval?,
                          locked: Bool, now: Date) -> Bool {
        guard status == .notDetermined || status == .provisional else { return false }
        if let asked = record?.asked_at {
            guard record?.asked_version != version, now.timeIntervalSince(asked) >= reaskAfter else { return false }
        }
        guard !composing, !locked, let sinceKey else { return false }
        return sinceKey >= quiet && sinceKey <= recent
    }

    /// 送出一則通知。已允許時照使用者設定的樣式（預設橫幅）；還沒決定時以暫時授權安靜送進通知中心，不跳詢問
    /// （詢問只由 NotificationPermissionMonitor 在使用者在場時做）；被拒絕時不送，completion 回 false，呼叫端靠
    /// 輸入法選單與設定程式的固定入口。completion 在任意執行緒呼叫。
    static func deliver(_ request: UNNotificationRequest, center: UNUserNotificationCenter = .current(),
                        completion: @escaping (Bool) -> Void = { _ in }) {
        center.getNotificationSettings { settings in
            let add = {
                center.add(request) { error in completion(error == nil) }
            }
            switch status(settings.authorizationStatus) {
            case .authorized, .provisional, .ephemeral:
                add()
            case .notDetermined:
                center.requestAuthorization(options: [.alert, .provisional]) { granted, error in
                    if granted && error == nil { add() } else { completion(false) }
                }
            case .denied, .unknown:
                completion(false)
            }
        }
    }

    static var recordURL: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/0c/notification-permission.json")
    }

    static func load(_ url: URL = recordURL) -> Record? {
        guard let data = try? Data(contentsOf: url) else { return nil }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try? decoder.decode(Record.self, from: data)
    }

    /// 記下目前的授權狀態（保留詢問紀錄）；輸入法程式啟動時與每次發通知時各記一次，匯出狀態摘要看得到使用者之後在系統設定改了什麼。
    @discardableResult
    static func note(_ settings: UNNotificationSettings, now: Date = Date(), url: URL = recordURL) -> Record {
        let current = status(settings.authorizationStatus)
        var record = load(url) ?? Record(status: current, checked_at: now)
        record.status = current
        record.alert_style = style(settings.alertStyle)
        record.checked_at = now
        save(record, to: url)
        return record
    }

    static func save(_ record: Record, to url: URL = recordURL) {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        encoder.outputFormatting = [.sortedKeys]
        guard let data = try? encoder.encode(record) else { return }
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? data.write(to: url, options: .atomic)
    }
}

/// 常駐的輸入法程式裡負責正式詢問一次（見 NotificationPermission）。只在主執行緒使用。
final class NotificationPermissionMonitor {
    static let shared = NotificationPermissionMonitor()
    static let interval: TimeInterval = 5

    private var timer: Timer?
    private var status: NotificationPermission.Status?
    private var record: NotificationPermission.Record?
    private var asking = false
    private var composing: () -> Bool = { true }
    private var lastKey: () -> Date? = { nil }
    private var servedHere: () -> Bool = { false }

    private var version: String {
        let info = Bundle.main.infoDictionary
        return info?["ZCBuildVersion"] as? String ?? (info?["CFBundleVersion"] as? String).map { "bundle " + $0 } ?? ""
    }

    /// composing：有沒有正在組字；lastKey：最後一次收到按鍵的時間；servedHere：這個行程仍是 Input Methods 裡目前的 App
    /// （被更新移入備份、等著交接的舊程式不問）。
    func start(composing: @escaping () -> Bool, lastKey: @escaping () -> Date?, servedHere: @escaping () -> Bool) {
        guard timer == nil else { return }
        self.composing = composing
        self.lastKey = lastKey
        self.servedHere = servedHere
        refresh()
        timer = Timer.scheduledTimer(withTimeInterval: Self.interval, repeats: true) { [weak self] _ in self?.tick() }
    }

    /// 重讀授權狀態並記下；使用者在系統設定改了，下一次匯出狀態摘要就看得到。
    private func refresh() {
        UNUserNotificationCenter.current().getNotificationSettings { settings in
            DispatchQueue.main.async {
                let record = NotificationPermission.note(settings)
                self.status = record.status
                self.record = record
            }
        }
    }

    private static var screenLocked: Bool {
        guard let session = CGSessionCopyCurrentDictionary() as? [String: Any] else { return true }
        return session["CGSSessionScreenIsLocked"] as? Bool == true || session["kCGSSessionOnConsoleKey"] as? Bool == false
    }

    private func stop() {
        timer?.invalidate()
        timer = nil
    }

    private func tick(now: Date = Date()) {
        guard let status, !asking, servedHere() else { return }
        let record = self.record
        if status != .notDetermined && status != .provisional || record?.asked_version == version {
            stop() // 已允許或已拒絕（系統不會再詢問），或這一版已經問過：這個行程不必再看
            return
        }
        let sinceKey = lastKey().map { now.timeIntervalSince($0) }
        // 先看便宜的條件，螢幕鎖定（對系統的查詢）最後才看。
        guard NotificationPermission.shouldAsk(status: status, record: record, version: version, composing: composing(),
                                               sinceKey: sinceKey, locked: false, now: now),
              !Self.screenLocked else { return }
        ask(from: status, record: record, now: now)
    }

    private func ask(from: NotificationPermission.Status, record: NotificationPermission.Record?, now: Date) {
        asking = true
        var asked = record ?? NotificationPermission.Record(status: from, checked_at: now)
        asked.asked_at = now
        asked.asked_version = version
        asked.asked_from = from
        asked.answer = nil
        NotificationPermission.save(asked) // 先記下：使用者一直沒回答、程式結束了，也不會每次啟動都再問
        self.record = asked
        let center = UNUserNotificationCenter.current()
        center.requestAuthorization(options: [.alert]) { granted, error in
            DispatchQueue.main.async {
                var answered = NotificationPermission.load() ?? asked
                answered.answer = error.map { $0.localizedDescription } ?? (granted ? "granted" : "denied")
                NotificationPermission.save(answered)
                self.record = answered
                self.asking = false
                self.refresh()
                if granted { Self.represent(center) }
            }
        }
    }

    /// 允許之前以暫時授權安靜送進通知中心的更新通知，允許後重送一次，以橫幅出現（例如回來之前背景已發了「有新版本」）。
    private static func represent(_ center: UNUserNotificationCenter) {
        center.getDeliveredNotifications { delivered in
            for note in delivered where !note.request.content.categoryIdentifier.isEmpty {
                center.add(UNNotificationRequest(identifier: note.request.identifier, content: note.request.content, trigger: nil))
            }
        }
    }
}
