import AppKit

/// 組字區（marked text）：每段的底線樣式，以及內容沒變時不重送。
enum MarkedText {
    /// 鎖定的字雙底線、近音更正點線，其餘單底線。字型與顏色交給 App：NSTextView 只用自己的
    /// typingAttributes 排組字，輸入法帶 `.font` 也會被丟掉（#108 實測）。
    /// Shift＋←／→ 標示要加入詞庫的範圍（`marked`，#123）用粗底線，App 把它當成「目前的文節」：
    /// 文字編輯（NSTextView）畫成亮的強調色底線、其他段變暗，Safari 畫成深色粗底線，Chrome 畫成淡藍底色。
    /// 底線顏色與背景色這三個 App 都不採用（2026-10-16 實測），所以不帶；VS Code 自己畫組字、不看任何樣式，
    /// 標示中另由候選面板顯示「Enter 加入詞庫：…」（見 CandidatePanel）。
    /// 中英混打時按空白會組成中文的數字（`reading`，#162）帶虛線底線與灰色字，留給會採用輸入法樣式的 App；
    /// 文字編輯與 Safari 實測都不採用（2026-10-19，照舊畫成一般組字底線），macOS 上主要靠候選面板的讀音（ㄉㄢ）提示。
    static func attributed(_ segments: [Snapshot.Segment]) -> NSAttributedString {
        let marked = NSMutableAttributedString(string: "")
        for segment in segments {
            let style: Int
            var attributes: [NSAttributedString.Key: Any] = [:]
            switch segment.style {
            case "locked": style = NSUnderlineStyle.double.rawValue
            case "corrected": style = NSUnderlineStyle.single.rawValue | NSUnderlineStyle.patternDot.rawValue
            case "marked": style = NSUnderlineStyle.thick.rawValue
            case "reading":
                style = NSUnderlineStyle.single.rawValue | NSUnderlineStyle.patternDash.rawValue
                attributes[.foregroundColor] = readingColor
            default: style = NSUnderlineStyle.single.rawValue
            }
            attributes[.underlineStyle] = style
            marked.append(NSAttributedString(string: segment.text, attributes: attributes))
        }
        return marked
    }

    /// 候選窗開著、卻沒有組字時（沒在打字時按 ` 開的符號表、Emoji 選單，#283）交給 App 的佔位組字：一個帶底線的半形空白。
    /// Office（Word、PowerPoint、Excel）與終端機一類的 App 只在自己有組字時才把方向鍵、Esc、Enter 交給輸入法，
    /// 沒有組字就自己處理（移動游標、叫出上一個指令），候選窗只剩空白鍵翻頁。威注音（vChewing）也是放同樣的佔位。
    /// 選了符號就換成真正的組字；關掉或送出時跟一般組字一樣拿掉，不會留在文件裡。
    static let placeholder = NSAttributedString(string: " ", attributes: [.underlineStyle: NSUnderlineStyle.single.rawValue])

    /// 這一次要交給 App 的組字：引擎的組字；沒有組字但候選窗開著時是佔位（游標在佔位前面，也就是符號會插入的地方）。
    static func presented(_ segments: [Snapshot.Segment], cursor: Int, candidates: Bool) -> Shown {
        let text = attributed(segments)
        if text.length == 0, candidates { return Shown(text: placeholder, cursor: 0) }
        return Shown(text: text, cursor: min(max(0, cursor), text.length))
    }

    /// 讀音數字的灰色：固定的灰（不用動態系統色，跨行程交給 App 時不會解析成別的外觀），淺色、深色背景都看得到。
    static let readingColor = NSColor(calibratedWhite: 0.55, alpha: 1)

    /// 上一次交給 App 的組字（文字含底線樣式，游標以 UTF-16 計）。
    struct Shown: Equatable {
        let text: NSAttributedString
        let cursor: Int
        static func == (a: Shown, b: Shown) -> Bool { a.cursor == b.cursor && a.text.isEqual(to: b.text) }
    }

    /// 和上一次送出的完全相同（文字、底線樣式、游標），而且 App 目前的 markedRange 仍是同樣長度時，
    /// 不必重送。Telegram 等 App 每次收到組字都先用輸入框的預設字級畫一次、下一輪才改回自己的
    /// 字級，相同內容重送（移動候選、重複按同一個注音鍵）也會閃一下（#108）。App 回報不出
    /// markedRange（NSNotFound）或長度不符時照常重送，以免畫面上的組字已被 App 清掉卻不再補上。
    /// `clientMarkedRange` 只在內容相同時才呼叫（跨行程查詢）。
    static func unchanged(_ next: Shown, last: Shown?, clientMarkedRange: () -> NSRange) -> Bool {
        guard next.text.length > 0, let last, last == next else { return false }
        let range = clientMarkedRange()
        return range.location != NSNotFound && range.length == next.text.length
    }
}
