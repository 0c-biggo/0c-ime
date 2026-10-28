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

}

// 依序請系統啟用父項與模式，一次只送出一個啟用要求（#238）。
//
// 第三方輸入來源的 TISEnableInputSource 只是請「系統設定」跳出「要允許…啟用…嗎？」的詢問，按「允許」後才由系統
// 寫進啟用清單（macOS 26.6.2 全新帳號實測）：
//   - 詢問一次只對應一個來源。父項與模式連續送出時，系統設定只顯示一個詢問，按「允許」後模式沒有啟用、多半連父項也
//     沒有（0.2.7 以前的做法，#238：全新帳號裝完沒有靈犀注音，登入後再問一次也一樣）。
//   - 父項還沒啟用時，模式的詢問按「允許」也不會生效；父項啟用之後再送模式，會再問一次，允許後模式寫進清單。
//   - 詢問開著時再送同一個要求會被忽略；使用者回答後再送則再跳一次詢問（看起來像第一次按的沒有生效）。
//   - 要求送出後不必等在原地：送出的程式結束了，按「允許」照樣生效。
// 所以每個來源只送一次要求，等回讀到已啟用再處理下一個；等太久就留給登入後的排程補做。
// 換掉 App 後幾秒內系統還在重新掃描，剛啟用的紀錄可能被蓋掉（青簡實測約 3 秒）：全部啟用後等一下再回讀，
// 被蓋掉就從頭再走一次（已核准的來源再送要求不會再詢問）。
enum InputSourceApproval {
    // 每個來源等使用者按「允許」的時間：安裝程式這段時間停在「正在執行套件腳本」。
    static let window: TimeInterval = 60
    static let poll: TimeInterval = 1
    static let settle: TimeInterval = 3
    static let passes = 2
    // 整個啟用流程的上限（含啟動系統設定、子行程回讀）；超過就結束，不讓安裝程式或排程一直等（#238）。
    static var deadline: TimeInterval { window * 2 + settle * Double(passes) + 30 }

    enum Outcome: Equatable {
        case enabled
        case pending(String) // 這個來源送出要求後在時限內沒有啟用（還在等「允許」或使用者拒絕）
        case failed(String) // 登記、查詢或啟用回傳錯誤
    }

    // ids：依序要啟用的來源（父項在前，最後一個是可選取的來源）。forced：簽章身分改變時要重新送出要求的父項；
    // 系統仍顯示它已啟用，無法確認使用者允許了沒有，只送一次、不等。state：在新的子行程回讀各來源是否已啟用，
    // 讀不到時為 nil（當成未啟用）。request：對一個來源送出啟用要求，回傳錯誤訊息，成功為 nil。clock：目前時間（秒）。
    static func run(ids: [String], forced: String? = nil, state: () -> [String: Bool]?,
                    request: (String) -> String?, wait: (TimeInterval) -> Void, clock: () -> TimeInterval) -> Outcome {
        guard let selectable = ids.last else { return .enabled }
        var forced = forced
        for _ in 1...passes {
            for id in ids {
                if state()?[id] == true && forced != id { continue }
                if let error = request(id) { return .failed(error) }
                if forced == id {
                    forced = nil
                    continue
                }
                let until = clock() + window
                var enabled = false
                while !enabled && clock() < until {
                    wait(poll)
                    enabled = state()?[id] == true
                }
                if !enabled { return .pending(id) }
            }
            wait(settle)
            if state()?[selectable] == true { return .enabled }
        }
        return .pending(selectable)
    }

    // 解析 `0c --query-input-source`／`--confirm-input-source` 的輸出（每行「<ID> enabled|disabled|missing」，
    // 改選了可選取的來源時多一行「<ID> selected」）。
    static func parse(_ output: String) -> [String: Bool] {
        var state: [String: Bool] = [:]
        for line in output.split(separator: "\n") {
            let fields = line.split(separator: " ")
            guard fields.count == 2 else { continue }
            let id = String(fields[0])
            state[id] = (state[id] ?? false) || fields[1] == "enabled" || fields[1] == "selected"
        }
        return state
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
