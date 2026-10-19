import Foundation

// 單按 Shift：按下到放開之間沒有其他按鍵、也沒有 Command／Control／Option，而且按住不超過時限（#117，0.3 秒）。
struct RightShiftTap {
    // 核心沒回報 shift_tap_ms 時的單按時限。
    static let defaultLimit: TimeInterval = 0.3
    // 剛切換後這段時間內，Shift 實際上沒按著卻送來的按下是 Chromium 的重複事件（#148）。
    static let duplicateWindow: TimeInterval = 0.5

    private(set) var down = false
    private(set) var armed = false
    private var downAt: TimeInterval = 0
    private var toggledAt = -TimeInterval.infinity

    /// Shift 的 flagsChanged；pressed 表示事件帶 Shift，at 是 NSEvent.timestamp，
    /// held 是處理這個事件當下 Shift 實際上是否還按著（系統的即時按鍵狀態）。
    /// 回傳 true 表示完成一次單按，應切換注音／英文；按住超過 limit 才放開不算（想打大寫最後沒打）。
    mutating func flags(pressed: Bool, modified: Bool, at time: TimeInterval = 0,
                        limit: TimeInterval = RightShiftTap.defaultLimit, held: Bool = true) -> Bool {
        if pressed {
            // Chromium／Electron（Chrome、Arc、VS Code、Claude 桌面版）把每個 flagsChanged 交給輸入法兩次。
            // 重複的按下不能取消這次單按，也不重設按下的時間，否則右 Shift 在這些 App 裡永遠不會切換（#71）。
            if !down {
                // 網頁忙時重複的那一對（按下、放開）晚到使用者放開之後才來：
                // 剛切換過、Shift 又已經放開，就不是新的按下，否則一次單按切換兩次，看起來 Shift 沒反應。
                if !held && time - toggledAt < Self.duplicateWindow { return false }
                down = true
                armed = !modified
                downAt = time
            } else if modified {
                armed = false
            }
            return false
        }
        let tap = down && armed && !modified && time - downAt <= limit
        reset()
        if tap { toggledAt = time }
        return tap
    }

    /// 按住 Shift 時按了其他鍵（例如 Shift＋字母），這次不算單按。
    mutating func otherKey() { armed = false }
    mutating func reset() { down = false; armed = false }

    /// 計時用的事件時間。網頁忙時 Chrome 等頁面回應才把 Shift 交給輸入法，交來的事件是重新產生的、時間是送出當下（#148，
    /// 例如 0.1 秒的單按，放開事件晚到 0.3 秒後），照 NSEvent.timestamp 算會超過 0.3 秒時限而不切換。
    /// 改用系統記下的最近一次修飾鍵變化時間（實際按下／放開的時間）；比事件時間晚或差太多時照舊用事件時間。
    static func eventTime(timestamp: TimeInterval, now: TimeInterval, sinceLastModifierChange: TimeInterval) -> TimeInterval {
        let physical = now - sinceLastModifierChange
        guard physical.isFinite, physical <= timestamp, timestamp - physical < 2 else { return timestamp }
        return physical
    }
}
