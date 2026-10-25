import Foundation

// Wire definitions mirror core/proto and bridge/ipc. Positions are UTF-16 on macOS.
struct Request: Codable {
    var type: String
    var key: String? = nil
    var index: Int? = nil
    var on: Bool? = nil
    var contextEnabled: Bool? = nil
    var contextID: UInt64? = nil
    /// toggle：上一次切換之後沒有任何按鍵或其他操作，請核心放回切到英文時清掉的未打完注音（#90）。
    var restore: Bool? = nil
    enum CodingKeys: String, CodingKey {
        case type, key, index, on, restore
        case contextEnabled = "context_enabled"
        case contextID = "context_id"
    }
}

struct Snapshot: Decodable {
    struct Segment: Decodable { let text: String; let style: String }
    struct Cursor: Decodable { let utf16: Int }
    /// note 是低調附註（模糊音近音為「近音 ㄓ」）；沒有時缺欄位。
    struct Candidate: Decodable {
        let id: Int
        let text: String
        var note: String? = nil
        var label: String? = nil // common / related / personal；舊引擎缺欄位時照常顯示
        var key: String? = nil // 選字鍵（#112）：1、q、;；舊引擎缺欄位，英文模式的補字候選沒有
    }
    /// 中英混打時照字面顯示、按空白會組成中文的數字按鍵的讀音（20 → ㄉㄢ，#162）；舊引擎或沒有時缺欄位。
    struct ReadingHint: Decodable { let text: String }
    struct Candidates: Decodable {
        let items: [Candidate]
        let page: Int
        let pages: Int
        let highlight: Int
        var columns: Int? = nil
        var kind: String? = nil
        var title: String? = nil
        /// 改字的候選，j／k 換前後一個字（#193）；由核心判斷（選字鍵含 j、k、Emoji、混打按鍵、注音打到一半等都不算）。舊引擎缺欄位。
        var stepKeys: Bool? = nil
        enum CodingKeys: String, CodingKey {
            case items, page, pages, highlight, columns, kind, title
            case stepKeys = "step_keys"
        }
    }
    let handled: Bool
    let commit: String?
    let preedit: [Segment]?
    let cursor: Cursor
    let suggestion: String?
    let candidates: Candidates?
    let mode: String
    var englishPending: Bool? = nil
    var fullWidth: Bool? = nil
    var capsLock: Bool? = nil
    let contextID: UInt64?
    /// 這次按鍵的提示（例如「已加入詞庫：世傑」，#123）；舊引擎或沒有提示時缺欄位。
    var notices: [String]? = nil
    var readingHint: ReadingHint? = nil
    /// 送出的文字目前會轉成簡體（#169）；只有回報 output_simplified_live 的引擎才有這一項，舊引擎缺欄位。
    var outputSimplified: Bool? = nil
    enum CodingKeys: String, CodingKey {
        case handled, commit, preedit, cursor, suggestion, candidates, mode, notices
        case readingHint = "reading_hint"
        case outputSimplified = "output_simplified"
        case englishPending = "english_pending"
        case fullWidth = "full_width"
        case capsLock = "caps_lock"
        case contextID = "context_id"
    }
    var text: String { (preedit ?? []).map(\.text).joined() }
    var composing: Bool { !text.isEmpty || candidates != nil }
    /// Shift＋←／→ 標示中的文字（#123）；沒有標示時為 nil。
    var markedText: String? {
        let text = (preedit ?? []).filter { $0.style == "marked" }.map(\.text).joined()
        return text.isEmpty ? nil : text
    }
    /// 要顯示的讀音提示（#162）；沒有或空字串時為 nil。
    var visibleReading: String? {
        guard let text = readingHint?.text, !text.isEmpty else { return nil }
        return text
    }
    /// 第 index 項候選的鍵標（#112）：引擎給的選字鍵；舊引擎沒有時依位置 1–0。
    /// 英文模式只有自動補字候選（按 Tab 接受、數字是文字），不畫鍵標。
    func candidateKey(_ index: Int) -> String {
        guard let items = candidates?.items, index < items.count else { return "" }
        if let key = items[index].key, !key.isEmpty { return key }
        return mode == "english" ? "" : String((index + 1) % 10)
    }
    /// 要顯示的提示文字（去掉空字串）。
    var visibleNotices: [String] { (notices ?? []).filter { !$0.isEmpty } }
}

struct Envelope: Encodable {
    let op: String
    var session: UInt64? = nil
    var req: Request? = nil
    var version: Int? = nil
}

struct Reply: Decodable {
    let ok: Bool
    let error: String?
    let session: UInt64?
    let snapshot: Snapshot?
    let info: [String: String]?
}

// starting：引擎剛被啟動、還沒開始服務；呼叫端可暫存請求稍後重送（請求尚未送出，重送安全）。
// disconnected：這個欄位已經連著的引擎結束了（更新交接、套用設定重啟、當掉），請求沒有被新引擎處理過（#119）。
enum EngineError: Error { case unavailable, starting, disconnected, secureInput, invalidFrame, rejected(String) }
