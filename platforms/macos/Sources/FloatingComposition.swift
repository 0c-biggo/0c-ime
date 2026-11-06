import Foundation
import CoreGraphics

/// 浮動組字（#84）：有些欄位在每次 input 事件把輸入框 blur／focus 或整個換掉元件（chatgpt.com 的 ⌘K 搜尋框、
/// Claude 桌面版的搜尋框），Chrome 隨即結束組字、把畫面上的注音留成文字，IMK 約 10 ms 內呼叫 commitComposition。
/// 照一般做法送出組字，每個鍵都從空組字開始，gj4jo4 打成「ㄕㄨ4ㄨㄟ4」。
///
/// 威注音的「客體管理」要使用者自己把 App 加進名單；這裡改成自動偵測：setMarkedText 之後很短的時間內，
/// 不是使用者按鍵、也不是輸入法自己要求的結束組字，就是 App 自己結束的。
/// - 第一次：這段組字不送出，改畫在輸入法自己的面板（救回這段組字），App 留下的那段文字（殘字）記下來，
///   送出時游標仍緊接在它後面就一起換掉。
/// - 同一個欄位連續兩次（中間沒有一段組字在 App 裡撐到下一個按鍵）：這個欄位之後的組字一開始就畫在面板，
///   App 裡不放組字，送出時才插入文字。
/// 一個 IMK client 是整個網頁（Chrome 一個分頁一個），網頁裡換輸入框時 IMK 不會 activate／deactivate，分不出是哪個輸入框。
/// 改用兩個線索：組字開頭時游標所在的行（App 給得出位置時），以及游標位置是否接在上次送出的文字之後（Chrome 沒有組字時
/// 給不出位置，只給得出游標在第幾個字）。任一個對不上（例如從 ⌘K 搜尋框回到主輸入框）就回到一般組字重新試，
/// 這時再被結束一次就又固定用浮動。換 App、換視窗（activate／deactivate）全部重來。
///
/// 只靠偵測，不依 App 種類一律浮動（負責人 2026-11-06：VS Code、Slack 等 Electron App 的一般欄位 inline 正常，不改變觀感）。
/// 浮動時 App 裡不放組字：佔位空白（威注音的做法）在這種欄位一樣會被結束、留成多餘的空白。代價是 Chromium 沒有組字時
/// 網頁收到原本的 keydown（不是 keyCode 229）：←→↑↓、Esc、Backspace 網頁也會照常處理（Backspace 多刪框裡一個字），
/// 送出兩個字以上時 Enter 是 229、isComposing 是 false。這類欄位在 0.2.9 本來也沒有組字，按鍵一樣都到網頁。
/// 試過每個按鍵放零寬空白當組字讓按鍵都變 229（2026-11-06，.12 cmdk 測試頁）：網頁結束組字後 Chrome 自己記的游標與組字位置
/// 對不上實際內容，佔位字清不掉、留在搜尋字串裡看不見，沒有採用。
///
/// 只有邏輯與時間判斷，時間由呼叫端給（systemUptime），方便測試。
struct FloatingComposition {
    enum Verdict: Equatable {
        /// 一般的結束組字（點到別處等）：照常送出。
        case commit
        /// App 在組字中途自己結束：改畫在面板、App 裡不再放組字、不送出。
        case rescue
        /// 浮動組字中，App 對剛送出的文字（或剛改成浮動）起的反應：不理會。
        case ignore
    }

    /// App 結束組字時留下的文字：當時放在 App 裡的組字。location 是當時 App 回報的組字位置，查不到時是 nil。
    struct Leftover: Equatable {
        let text: String
        let location: Int?
    }

    /// setMarkedText 之後這段時間內 App 就結束組字，算是 App 自己結束的（.12 實測約 10–20 ms，留給忙碌的網頁與機器）。
    /// 使用者按鍵後這麼快就點到別處的機會很小，而且要連續兩次才會讓欄位一直用浮動。
    static let earlyEnd: TimeInterval = 0.25
    /// 浮動組字中，插入文字（或剛改成浮動）之後這段時間內 App 結束組字，是網頁對 input 事件的反應，不是使用者點到別處。
    static let echo: TimeInterval = 0.5
    static let strikesToStick = 2

    /// 目前這段組字畫在輸入法的面板，App 裡不放組字。
    private(set) var floating = false
    /// 這個欄位之後的組字一開始就用浮動。
    private(set) var sticky = false
    /// 連續被 App 結束組字的次數；一段組字在 App 裡撐到下一個按鍵就歸零。
    private(set) var strikes = 0
    /// App 結束組字時留下的文字；送出或取消時取走。
    private(set) var leftover: Leftover?
    private var stickyLine: CGRect?
    /// 浮動送出文字後游標應在的位置（UTF-16）；下一段組字開始時游標不在這裡，就是換了輸入框或移了游標。
    private var expectedCaret: Int?
    private var markedAt: TimeInterval?
    private var touchedAt: TimeInterval?

    /// 換 App、換視窗：全部重來。
    mutating func reset() { self = FloatingComposition() }

    /// 新的一段組字開始（前一個快照沒有組字），在交給 App 任何東西之前。line 是游標所在的行（螢幕座標）、caret 是游標位置，
    /// 查不到時是 nil；只在 sticky 時需要（needsField），一般欄位不多問 App。
    mutating func compositionStarts(line: CGRect?, caret: Int? = nil) {
        markedAt = nil
        if sticky {
            let lineMoved = line.flatMap { line in stickyLine.map { !Self.sameLine(line, $0) } } ?? false
            let caretMoved = caret.flatMap { caret in expectedCaret.map { caret != $0 } } ?? false
            if lineMoved || caretMoved {
                // 可能換了輸入框：先回到一般組字試；同一個欄位的話再被結束一次就又固定。
                sticky = false
                strikes = Self.strikesToStick - 1
                expectedCaret = nil
            }
        }
        if sticky, stickyLine == nil { stickyLine = line }
        floating = sticky
    }

    var needsField: Bool { sticky }

    /// 浮動組字送出文字後游標所在的位置（查不到時 nil）。
    mutating func caretAfterInsert(_ caret: Int?) { expectedCaret = caret }

    /// 組字結束（送出、取消）；浮動只維持到這段組字結束，下一段由 compositionStarts 決定。
    mutating func compositionEnded() {
        floating = false
        markedAt = nil
    }

    /// 把組字交給 App（setMarkedText，非空）。
    mutating func markedTextShown(at now: TimeInterval) { markedAt = now }

    /// 輸入法插入文字之後（浮動組字中，網頁對 input 事件的反應不算使用者結束組字）。
    mutating func textInserted(at now: TimeInterval) { touchedAt = now }

    /// 使用者的下一個按鍵到了：App 裡的組字撐到這時還在，這個欄位正常。
    mutating func keyArrives(composing: Bool) {
        if composing, !floating, markedAt != nil { strikes = 0 }
        markedAt = nil
    }

    /// App 要求結束組字（IMK 的 commitComposition，不含輸入法自己因按鍵、失焦而送出）。
    /// shown 是當時放在 App 裡的組字，location 是 App 當時回報的組字位置；line 是這段組字開頭時（App 裡還有組字時）
    /// 記下的游標行，欄位改成固定浮動時用來認欄位。
    mutating func appEndsComposition(at now: TimeInterval, composing: Bool, shown: String?, location: Int?, line: CGRect? = nil) -> Verdict {
        guard composing else { return .commit }
        if floating {
            if let touchedAt, now - touchedAt <= Self.echo { return .ignore }
            return .commit
        }
        guard let markedAt, now - markedAt <= Self.earlyEnd else { return .commit }
        strikes += 1
        floating = true
        leftover = shown.flatMap { $0.isEmpty ? nil : Leftover(text: $0, location: location) }
        touchedAt = now
        self.markedAt = nil
        if strikes >= Self.strikesToStick, !sticky {
            sticky = true
            stickyLine = line // 查不到時等下一段組字開始再記
        }
        return .rescue
    }

    /// 送出或取消救回的組字時取走殘字（只試一次）。
    mutating func takeLeftover() -> Leftover? {
        defer { leftover = nil }
        return leftover
    }

    /// 殘字要不要換掉：游標（沒有選取）必須緊接在殘字後面；App 讀得到那段文字時必須正好是它，
    /// 讀不到（Chrome 不給游標前的文字）時改以 App 當時回報的位置核對。回傳要換掉的範圍。
    static func leftoverRange(_ leftover: Leftover, selection: NSRange, textBefore: (NSRange) -> String?) -> NSRange? {
        let length = (leftover.text as NSString).length
        guard length > 0, selection.location != NSNotFound, selection.length == 0, selection.location >= length else { return nil }
        let range = NSRange(location: selection.location - length, length: length)
        if let location = leftover.location, location != range.location { return nil }
        if let actual = textBefore(range) { return actual == leftover.text ? range : nil }
        return leftover.location == nil ? nil : range
    }

    /// 同一個欄位：游標行的垂直位置差不到一行半。單行的搜尋框打字時只會左右移動。
    static func sameLine(_ a: CGRect, _ b: CGRect) -> Bool {
        let height = max(a.height, b.height, 16)
        return abs(a.midY - b.midY) <= height * 1.5
    }
}
