import Foundation

/// 安全輸入被背景程式卡住時提醒（#349）。這裡只做判斷；讀系統狀態與提醒面板在 SecureInputMonitor.swift。
///
/// 安全輸入（EnableSecureEventInput）開著時，系統停用第三方輸入法：靈犀在選單變灰、打字變成英文。
/// 每個程式只能放掉自己開的那一份，輸入法替別人關不掉，所以只能提醒使用者怎麼恢復。
/// IORegistry 的 kCGSSessionSecureInputPID 記的是開啟當下的前景 App，不一定是持有者（#245、#342；Apple DTS
/// forums/thread/726353、OpenRadar 48953777，1Password 開的卻記成 Edge），所以提醒裡只寫成線索「開啟時在用的是 X」
/// （負責人 2026-11-24），不說是它卡住的。判斷時這個 PID 用來看「是不是前景 App 自己正在用」：
/// - 前景 App 就是記錄的 PID（密碼欄、密碼管理程式的小視窗；這時記到的也是前景 App），或前景是終端機
///   （「安全鍵盤輸入」），是正常使用，不提醒。
/// - 記錄的 PID 不是前景 App，而且同一個 PID 撐過 `grace` 秒還開著（切走後正常的程式會放掉），才算卡住；
///   之後就算使用者又切回那個 App，等到它不在前景時照樣提醒。
/// - 系統的 SecurityAgent（登入、授權對話框）不算；睡眠、鎖定、螢幕保護、切換使用者期間不判斷。
/// - 同一個 PID 只提醒一次（唯音 SecurityAgentHelper 的做法），卡住與放掉都記進診斷計數。
struct SecureInputWatch {
    struct Observation {
        var secure: Bool
        /// kCGSSessionSecureInputPID：開啟當下的前景 App。
        var holder: Int32?
        var holderBundle: String?
        var front: Int32?
        var frontBundle: String?
        /// 睡眠、鎖定、螢幕保護或切換到別的使用者。
        var paused = false
    }

    enum Action: Equatable {
        case none
        /// 確定卡住（每段一次）；warn 是這個 PID 還沒提醒過。
        case stuck(warn: Bool)
        /// 確定卡住之後放掉了。
        case released
    }

    static let grace: TimeInterval = 25
    static let ignoredHolders: Set<String> = ["com.apple.SecurityAgent"]

    private var suspect: (pid: Int32, since: Date)?
    private(set) var confirmed = false
    private(set) var reported: Set<Int32> = []

    /// 提醒裡當線索寫出的 App。loginwindow、SecurityAgent、對不到 App（沒有名稱）時不點名，只列步驟。
    struct Clue: Equatable {
        let name: String
        /// 瀏覽器：第一步另外提到它的自動填密碼擴充功能。
        let browser: Bool
    }

    static let unnamedHolders: Set<String> = ["com.apple.loginwindow", "com.apple.SecurityAgent"]
    /// Chromium 系、Safari（含網頁內容行程）與 Firefox。用前綴比對，Beta／Dev／Canary 版也算。
    static let browserPrefixes = ["com.google.Chrome", "com.microsoft.edgemac", "com.brave.Browser", "company.thebrowser.",
                                  "com.vivaldi.Vivaldi", "com.operasoftware.Opera", "org.chromium.Chromium", "com.apple.Safari",
                                  "com.apple.WebKit.WebContent", "org.mozilla.firefox"]

    static func clue(bundle: String?, name: String?) -> Clue? {
        guard let name = name?.trimmingCharacters(in: .whitespacesAndNewlines), !name.isEmpty,
              !unnamedHolders.contains(bundle ?? "") else { return nil }
        let browser = bundle.map { id in browserPrefixes.contains { id.hasPrefix($0) } } ?? false
        return Clue(name: String(name.prefix(60)), browser: browser)
    }

    static let title = "靈犀注音暫時不能用"

    static func message(_ clue: Clue?) -> String {
        let lock = "鎖定螢幕（⌃⌘Q），再用密碼解鎖，不要用 Touch ID；再不行就登出再登入。"
        let steps: [String]
        let opening: String
        if let clue {
            opening = "macOS 的「安全輸入」被開著沒放掉（開啟時在用的是 \(clue.name)），靈犀注音會變灰、打字變成英文。可以照順序試："
            steps = [clue.browser ? "先結束 \(clue.name) 再開，或停用它會自動填密碼的擴充功能，通常就會恢復。"
                                  : "先結束 \(clue.name) 再開，通常就會恢復。",
                     "沒用的話，結束密碼管理程式（例如 1Password）或瀏覽器的自動填密碼擴充功能。",
                     "還是不行就" + lock]
        } else {
            opening = "macOS 的「安全輸入」被開著沒放掉，靈犀注音會變灰、打字變成英文。可以照順序試："
            steps = ["結束密碼管理程式（例如 1Password）或瀏覽器的自動填密碼擴充功能。",
                     "結束剛才輸入過密碼的 App。",
                     "還是不行就" + lock]
        }
        let numbered = steps.enumerated().map { "\($0.offset + 1). \($0.element)" }.joined(separator: "\n")
        return opening + "\n\n" + numbered + "\n\n恢復之後這個視窗會自己關掉。"
    }

    mutating func check(_ observation: Observation, at now: Date) -> Action {
        guard !observation.paused else { return .none }
        guard observation.secure else {
            let wasStuck = confirmed
            suspect = nil
            confirmed = false
            return wasStuck ? .released : .none
        }
        guard let pid = observation.holder, !Self.ignoredHolders.contains(observation.holderBundle ?? "") else { return .none }
        if let current = suspect, current.pid != pid {
            // 有程式重新開了安全輸入：從頭觀察。
            suspect = nil
            confirmed = false
        }
        let inUse = observation.front == pid || SecureInputPolicy.terminals.contains(observation.frontBundle ?? "")
        guard let current = suspect else {
            if !inUse { suspect = (pid, now) }
            return .none
        }
        guard !confirmed, !inUse, now.timeIntervalSince(current.since) >= Self.grace else { return .none }
        confirmed = true
        return .stuck(warn: reported.insert(pid).inserted)
    }
}
