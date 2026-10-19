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
    }
    let handled: Bool
    let commit: String?
    let preedit: [Segment]?
    let cursor: Cursor
    let suggestion: String?
    let candidates: Candidates?
    let mode: String
    var englishPending: Bool? = nil
    let contextID: UInt64?
    /// 這次按鍵的提示（例如「已加入詞庫：世傑」，#123）；舊引擎或沒有提示時缺欄位。
    var notices: [String]? = nil
    var readingHint: ReadingHint? = nil
    enum CodingKeys: String, CodingKey {
        case handled, commit, preedit, cursor, suggestion, candidates, mode, notices
        case readingHint = "reading_hint"
        case englishPending = "english_pending"
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
