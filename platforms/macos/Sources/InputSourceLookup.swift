import Foundation

// TIS 登記成功後，清單可能尚未包含新的來源。只重查清單，不重複登記或要求使用者允許。
enum InputSourceLookup {
    static func find<Source>(
        list: () -> [Source],
        matches: (Source) -> Bool,
        wait: (TimeInterval) -> Void,
        missing: (Int) -> Void
    ) -> Source? {
        let delays: [TimeInterval] = [0.5, 1.5]
        for attempt in 0...delays.count {
            if attempt > 0 { wait(delays[attempt - 1]) }
            if let source = list().first(where: matches) { return source }
            missing(attempt + 1)
        }
        return nil
    }

    // 換掉 App 後幾秒內系統還在重新掃描，剛啟用的紀錄可能被蓋掉（青簡實測約 3 秒）；
    // 第一次啟用模式時系統會詢問使用者是否允許，允許前模式維持未啟用；從沒有模式的版本升級時，
    // 從 ssh 執行的安裝在同一個登入工作階段內不被接受、重新登入後才會詢問；背景自動更新則當場詢問，
    // 但允許後要重新登入，選單才顯示並選得到新模式（#130，macOS 26.6.2 實測）。
    // 每一輪啟用後等一下再回讀確認，沒啟用就整輪重來（詢問中重複啟用只會留一個對話框），
    // 最多約 15 秒；回傳成功的輪次，全部失敗為 nil，由登入後的排程補做。之後才按「允許」也會生效。
    static let confirmDelay: TimeInterval = 3
    static let rounds = 5

    static func confirm(enable: (Int) -> Bool, confirmed: () -> Bool, wait: (TimeInterval) -> Void) -> Int? {
        for round in 1...rounds {
            guard enable(round) else { return nil }
            wait(confirmDelay)
            if confirmed() { return round }
        }
        return nil
    }
}

// 依 App 的 Info.plist 決定要登記、啟用與選取的輸入來源。
// 0.1.0-dev.59 以前只有頂層 TISInputSourceID（系統登記成不分模式的輸入法）；之後在 ComponentInputModeDict
// 宣告模式，頂層 ID 成為父項，可選取的是模式（#87）。
struct InputSourcePlan: Equatable {
    // 靈犀注音用過的 bundle ID：0.1.0-dev.21 以前是 org.zuin.inputmethod.Zuin，之後是 com.biggo.inputmethod.0c。
    static let products: Set<String> = ["com.biggo.inputmethod.0c", "org.zuin.inputmethod.Zuin"]

    let bundle: String
    let parent: String
    let mode: String?

    var selectable: String { mode ?? parent }
    // 啟用順序：先父項，再模式。
    var enabled: [String] { mode.map { [parent, $0] } ?? [parent] }

    init?(bundle: String, info: [String: Any]) {
        guard Self.products.contains(bundle) else { return nil }
        self.bundle = bundle
        parent = info["TISInputSourceID"] as? String ?? bundle
        let modes = info["ComponentInputModeDict"] as? [String: Any]
        mode = (modes?["tsVisibleInputModeOrderedArrayKey"] as? [String])?.first
    }

    // 屬於靈犀注音、但不是這一版要用的來源：舊名稱的 App，或回復到沒有模式的版本後留下的模式。
    func stale(id: String, bundle sourceBundle: String?) -> Bool {
        guard let sourceBundle, Self.products.contains(sourceBundle) else { return false }
        return !enabled.contains(id)
    }

    // 目前選取的是靈犀注音的其他來源（例如升級前不分模式的父項）時，換成這一版可選取的來源；
    // 使用者正在用別的輸入法時不切換。
    func shouldSelect(currentID: String?, currentBundle: String?) -> Bool {
        guard let currentBundle, Self.products.contains(currentBundle) else { return false }
        return currentID != selectable
    }
}
