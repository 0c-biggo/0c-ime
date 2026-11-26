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
//
// 0.2.15 起模式預設啟用（Info.plist 的 tsInputModeDefaultStateKey 為 true，#348，參考小麥注音：安裝程式只啟用頂層來源）。
// .13 Intel macOS 15.8.2 全新帳號實測（#238 在 26.6.2 也試過同樣的做法）：
//   - 只送父項的要求：問一次，按「允許」後系統把父項與模式一起寫進啟用清單，選單列馬上有靈犀注音。
//   - 只送模式的要求（鼠鬚管的做法）：問一次，按「允許」後什麼都沒寫進清單。
//   - 啟用清單裡只有父項（0.2.14 以前晚按「允許」、dev.60 以前沒有模式的版本升級上來）：TIS 把模式顯示成已啟用，
//     送模式的要求照樣會問，但按了「允許」也不寫進清單；重新送父項的要求、先 TISDisableInputSource 再啟用（從背景、
//     LaunchAgent、用 open 開的 App 呼叫都一樣）也都沒用，重新登入後選單列沒有靈犀注音（#87 看到的就是這個）。
//     只有使用者在系統設定把「靈犀注音」移除（清單變空）後，再送父項的要求才會把兩個一起寫進清單。
// 所以 follows（模式隨父項啟用）時只送父項的要求；父項啟用後最多等 follow 秒讓模式出現在清單裡，還是沒有就回報
// .unlisted，不送沒有用的模式要求、不讓使用者白按「允許」。state 回報的「已啟用」必須看系統真正的啟用清單
// （InputSourcePlan.listed）：預設啟用的模式在 TIS 裡一直顯示 enabled，連父項還沒被允許時也是。
enum InputSourceApproval {
    // 每個來源等使用者按「允許」的時間：安裝程式這段時間停在「正在執行套件腳本」。
    static let window: TimeInterval = 60
    static let poll: TimeInterval = 1
    static let settle: TimeInterval = 3
    // 父項被允許後，等系統把預設啟用的模式一起寫進清單的時間（#348；實測按下後幾秒內兩個一起出現）。
    static let follow: TimeInterval = 5
    static let passes = 2
    // 整個啟用流程的上限（含啟動系統設定、子行程回讀）；超過就結束，不讓安裝程式或排程一直等（#238）。
    static var deadline: TimeInterval { window * 2 + (settle + follow) * Double(passes) + 30 }

    enum Outcome: Equatable {
        case enabled
        case pending(String) // 這個來源送出要求後在時限內沒有啟用（還在等「允許」或使用者拒絕）
        case failed(String) // 登記、查詢或啟用回傳錯誤
        case unlisted(String) // 父項已啟用，預設啟用的模式卻不在啟用清單裡：自動加不進去，要使用者在系統設定移除再加入
    }

    // ids：依序要啟用的來源（父項在前，最後一個是可選取的來源）。forced：簽章身分改變時要重新送出要求的父項；
    // 系統仍顯示它已啟用，無法確認使用者允許了沒有，只送一次、不等。follows：模式預設啟用、隨父項一起寫進清單，
    // 只送父項的要求。state：在新的子行程回讀各來源是否已啟用，讀不到時為 nil（當成未啟用）。request：對一個來源
    // 送出啟用要求，回傳錯誤訊息，成功為 nil。clock：目前時間（秒）。
    static func run(ids: [String], forced: String? = nil, follows: Bool = false, state: () -> [String: Bool]?,
                    request: (String) -> String?, wait: (TimeInterval) -> Void, clock: () -> TimeInterval) -> Outcome {
        guard let selectable = ids.last else { return .enabled }
        var forced = forced
        func waitFor(_ id: String, _ limit: TimeInterval) -> Bool {
            let until = clock() + limit
            var enabled = false
            while !enabled && clock() < until {
                wait(poll)
                enabled = state()?[id] == true
            }
            return enabled
        }
        for _ in 1...passes {
            for (index, id) in ids.enumerated() {
                if state()?[id] == true && forced != id { continue }
                if follows && index > 0 {
                    if !waitFor(id, follow) { return .unlisted(id) }
                    continue
                }
                if let error = request(id) { return .failed(error) }
                if forced == id {
                    forced = nil
                    continue
                }
                if !waitFor(id, window) { return .pending(id) }
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
    // 模式預設啟用（tsInputModeDefaultStateKey，0.2.15 起，#348）：系統隨父項一起把模式寫進啟用清單，只送父項的要求。
    let modeFollowsParent: Bool

    var selectable: String { mode ?? parent }
    // 啟用順序：先父項，再模式。
    var enabled: [String] { mode.map { [parent, $0] } ?? [parent] }

    init?(bundle: String, info: [String: Any]) {
        guard Self.products.contains(bundle) else { return nil }
        self.bundle = bundle
        parent = info["TISInputSourceID"] as? String ?? bundle
        let modes = info["ComponentInputModeDict"] as? [String: Any]
        mode = (modes?["tsVisibleInputModeOrderedArrayKey"] as? [String])?.first
        let declared = (modes?["tsInputModeListKey"] as? [String: Any])?[mode ?? ""] as? [String: Any]
        modeFollowsParent = mode != nil && (declared?["tsInputModeDefaultStateKey"] as? Bool ?? false)
    }

    // 系統真正的啟用清單（com.apple.inputsources 的 AppleEnabledThirdPartyInputSources）裡有沒有各個來源（#348）。
    // 使用者按「允許」、或在系統設定加入時，系統才寫進這份清單；重新登入後選單列照它列出輸入來源。TIS 的
    // kTISPropertyInputSourceIsEnabled 不可靠：預設啟用的模式在父項還沒啟用、甚至還沒被允許時就顯示 enabled（.13 macOS 15.8.2
    // 實測），從沒有模式的版本升級後也是（#87）；小麥注音的安裝程式也記載 macOS 12 起 enabled 為 true 時不一定在使用者的清單裡。
    // 父項是 InputSourceKind「Keyboard Input Method」、Bundle ID 相同的那筆；模式是「Input Mode」等於模式 ID 的那筆。
    // entries 為 nil（兩份偏好設定都沒有這個鍵）時回傳 nil：判斷不了，由呼叫端退回 TIS 的狀態。
    func listed(_ entries: [[String: Any]]?) -> [String: Bool]? {
        guard let entries else { return nil }
        var result: [String: Bool] = [:]
        for id in enabled { result[id] = false }
        for entry in entries where entry["Bundle ID"] as? String == bundle {
            if let mode = entry["Input Mode"] as? String {
                if enabled.contains(mode) && mode != parent { result[mode] = true }
            } else if entry["InputSourceKind"] as? String == "Keyboard Input Method" {
                result[parent] = true
            }
        }
        return result
    }

    // 合併兩份啟用清單：第三方輸入來源的 com.apple.inputsources AppleEnabledThirdPartyInputSources，與系統的
    // com.apple.HIToolbox AppleEnabledInputSources（較舊的 macOS 第三方輸入法也記在這裡）。全新帳號在第一次允許第三方
    // 輸入法之前，前者整個不存在（.13 macOS 15.8.2），這時不能說「判斷不了」：預設啟用的模式在 TIS 裡是 enabled，
    // 退回 TIS 就會把還沒允許的模式當成已啟用（#348 驗收 S2 實際發生，補做把記號清掉了）。後者幾乎一定存在（至少有 ABC），
    // 所以合併後只有兩個都讀不到時才是 nil。
    static func mergeLists(thirdParty: [[String: Any]]?, system: [[String: Any]]?) -> [[String: Any]]? {
        if thirdParty == nil && system == nil { return nil }
        return (thirdParty ?? []) + (system ?? [])
    }

    // 對外回報的「已啟用」：TIS 顯示已啟用，而且（讀得到時）在系統的啟用清單裡。
    // 兩份清單都讀不到時，預設啟用的模式不能照 TIS（它一直是 enabled），改成跟著父項的 TIS 狀態：父項還沒啟用就不算。
    // 驗收時重新開機的那幾秒（cfprefsd 正在結束）補做讀不到清單，照 TIS 把還沒允許的模式當成已啟用、清掉了記號。
    func effective(tis: [String: Bool], listed: [String: Bool]?) -> [String: Bool] {
        var state: [String: Bool] = [:]
        for id in enabled {
            if let listed {
                state[id] = (tis[id] ?? false) && (listed[id] ?? false)
            } else if modeFollowsParent && id == mode {
                state[id] = (tis[id] ?? false) && (tis[parent] ?? false)
            } else {
                state[id] = tis[id] ?? false
            }
        }
        return state
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
