import Foundation
import ServiceManagement
import UserNotifications

// 自動更新排程以 SMAppService 註冊 App 內附的 launchd agent（Contents/Library/LaunchAgents），
// 系統設定的「登入項目與延伸功能 → 允許在背景執行」才會顯示「靈犀注音」與 App 圖示，而不是簽章的公司名稱；
// 使用者在那裡關閉時，狀態為 requiresApproval，0c-update 與輸入法可以據此提醒（#25）。
// 0c-update 以 `0c --update-agent register|unregister|status` 呼叫：SMAppService 以呼叫者的 main bundle
// 找 plist，必須由 App 本身的執行檔呼叫。
enum UpdateAgent {
    static let plistName = "com.biggo.0c.update.plist"

    static var service: SMAppService { SMAppService.agent(plistName: plistName) }

    static func label(_ status: SMAppService.Status) -> String {
        switch status {
        case .enabled: return "enabled"
        case .requiresApproval: return "requires-approval"  // 使用者在系統設定關閉了背景執行
        case .notRegistered: return "not-registered"
        case .notFound: return "not-found"
        @unknown default: return "unknown"
        }
    }

    /// 結束碼：0 已啟用、3 使用者已關閉、4 未註冊、5 找不到 plist、1 其他錯誤。狀態字串印在標準輸出第一行。
    static func run(_ action: String) -> Int32 {
        let agent = service
        switch action {
        case "register":
            do {
                try agent.register()
            } catch {
                // 使用者關閉背景執行時 register 會失敗，狀態仍可讀到；把狀態交給呼叫端判斷。
                FileHandle.standardError.write(Data("\(error.localizedDescription)\n".utf8))
            }
        case "unregister":
            do {
                try agent.unregister()
            } catch {
                FileHandle.standardError.write(Data("\(error.localizedDescription)\n".utf8))
            }
        case "status":
            break
        case "open-settings":
            SMAppService.openSystemSettingsLoginItems()
        default:
            FileHandle.standardError.write(Data("用法：0c --update-agent register|unregister|status|open-settings\n".utf8))
            return 1
        }
        let status = agent.status
        print(label(status))
        switch status {
        case .enabled: return 0
        case .requiresApproval: return 3
        case .notRegistered: return 4
        case .notFound: return 5
        @unknown default: return 1
        }
    }
}

// 背景執行被關閉時的提醒（docs/automatic-updates.md「macOS：背景執行被關閉」）。排程被關掉後無法執行，
// 只有輸入法 App（使用者打字時一定在執行、在圖形工作階段中）看得到，所以由它偵測並提醒：
// 第一次發現時一則通知，仍關閉時 30 天後再一則，之後每 90 天最多一則；手動模式、暫停中或使用者以
// 0c-update schedule remove 關閉排程時不發。輸入法選單另有固定入口。通知以暫時授權發送，不跳權限詢問。

final class UpdateAgentMonitor: NSObject, UNUserNotificationCenterDelegate {
    static let shared = UpdateAgentMonitor()
    static let category = "update-agent-disabled"
    private var timer: Timer?

    private static var updateDir: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/0c/update")
    }

    func start() {
        UNUserNotificationCenter.current().delegate = self
        UNUserNotificationCenter.current().setNotificationCategories(UpdateNotification.categories())
        refreshStatus()
        DispatchQueue.main.asyncAfter(deadline: .now() + 30) { [weak self] in self?.check() }
        timer = Timer.scheduledTimer(withTimeInterval: 24 * 3600, repeats: true) { [weak self] _ in self?.check() }
    }

    /// 最近一次查到的排程狀態（選單入口據此顯示）；還沒查到時為 nil。
    /// 系統每次組字更新後都會要一次選單（一個按鍵一次），SMAppService 的查詢是對系統服務的同步呼叫，
    /// 在主執行緒上每鍵要 30–90ms，服務忙時更久；所以選單只讀快取，查詢放到背景（#148）。
    private(set) var status: SMAppService.Status?
    private var statusQuerying = false
    private var lastStatusRefresh = -TimeInterval.infinity

    /// 被更新移入備份後仍在執行時不碰 SMAppService：macOS 15 會把背景項目的登記改成呼叫者現在所在的位置（備份），
    /// 排程就一直執行備份裡的舊 0c-update（#205）。這時選單不顯示排程狀態，交接後由新版的行程接手。
    private var servedHere: Bool { HandoverMonitor.shared.atLaunchPath }

    func refreshStatus(minimumInterval: TimeInterval = 0, now: TimeInterval = ProcessInfo.processInfo.systemUptime) {
        guard servedHere else { status = nil; return }
        guard !statusQuerying, now - lastStatusRefresh >= minimumInterval else { return }
        statusQuerying = true
        lastStatusRefresh = now
        DispatchQueue.global(qos: .utility).async {
            let current = UpdateAgent.service.status
            DispatchQueue.main.async {
                self.status = current
                self.statusQuerying = false
            }
        }
    }

    struct Preferences: Decodable {
        var mode: String?
        var paused_until: String?
        var schedule_removed: Bool?
    }

    struct Notice: Codable {
        var count = 0
        var last: Date?
    }

    func check(now: Date = Date()) {
        guard servedHere else { return } // 見 servedHere：備份裡的 App 補註冊會讓排程指著備份
        defer { refreshStatus() } // 補註冊後選單跟著顯示新狀態
        let agent = UpdateAgent.service
        let prefs = Self.loadPreferences()
        switch agent.status {
        case .notRegistered:
            // 排程遺失（例如註冊時不在圖形工作階段）：直接補註冊，不打擾使用者。使用者自己移除的不重建。
            if prefs.schedule_removed != true { try? agent.register() }
        case .enabled:
            Self.saveNotice(Notice())  // 重新打開後清除提醒紀錄，下次關閉時從第一則開始
        case .requiresApproval:
            if prefs.mode == "manual" || prefs.schedule_removed == true || Self.paused(prefs, now: now) { return }
            var notice = Self.loadNotice()
            guard Self.due(notice, now: now) else { return }
            post()
            notice.count += 1
            notice.last = now
            Self.saveNotice(notice)
        default:
            break
        }
    }

    /// 第一則立即；第二則距上一則 30 天；之後每 90 天。
    static func due(_ notice: Notice, now: Date) -> Bool {
        guard let last = notice.last else { return true }
        let days: Double = notice.count <= 1 ? 30 : 90
        return now.timeIntervalSince(last) >= days * 86400
    }

    private static func paused(_ prefs: Preferences, now: Date) -> Bool {
        guard let text = prefs.paused_until else { return false }
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let date = iso.date(from: text) ?? ISO8601DateFormatter().date(from: text)
        return date.map { $0 > now } ?? false
    }

    private static func loadPreferences() -> Preferences {
        guard let data = try? Data(contentsOf: updateDir.appendingPathComponent("state.json")),
              let prefs = try? JSONDecoder().decode(Preferences.self, from: data) else { return Preferences() }
        return prefs
    }

    private static var noticeURL: URL { updateDir.appendingPathComponent("agent-notice.json") }

    private static func loadNotice() -> Notice {
        guard let data = try? Data(contentsOf: noticeURL) else { return Notice() }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return (try? decoder.decode(Notice.self, from: data)) ?? Notice()
    }

    private static func saveNotice(_ notice: Notice) {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        guard let data = try? encoder.encode(notice) else { return }
        try? FileManager.default.createDirectory(at: updateDir, withIntermediateDirectories: true)
        try? data.write(to: noticeURL, options: .atomic)
    }

    private func post() {
        let center = UNUserNotificationCenter.current()
        center.requestAuthorization(options: [.alert, .provisional]) { granted, _ in
            guard granted else { return }
            let content = UNMutableNotificationContent()
            content.title = "靈犀注音的自動更新已關閉"
            content.body = "在系統設定關閉背景執行後，不會再收到更新與修正。點一下開啟「登入項目」，打開「靈犀注音」即可恢復。"
            content.categoryIdentifier = Self.category
            center.add(UNNotificationRequest(identifier: Self.category, content: content, trigger: nil))
        }
    }

    static func openSettings() { SMAppService.openSystemSettingsLoginItems() }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        UpdateNotification.handle(category: response.notification.request.content.categoryIdentifier,
                                  action: response.actionIdentifier,
                                  userInfo: response.notification.request.content.userInfo)
        if response.notification.request.content.categoryIdentifier == Self.category,
           response.actionIdentifier == "open" || response.actionIdentifier == UNNotificationDefaultActionIdentifier {
            Self.openSettings()
        }
        completionHandler()
    }

    // 輸入法是背景 App，系統通常不在前景呈現；仍允許以橫幅顯示（暫時授權下系統會安靜送進通知中心）。
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .list])
    }
}
