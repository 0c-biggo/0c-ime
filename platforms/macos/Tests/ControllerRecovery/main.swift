import AppKit
import Foundation

var client = TextClient()
var controller = LingxiInputController(client: client)
// #308：Parsec 等遠端桌面注入的修飾鍵 keyCode 是 0，只有旗標是對的。
func capsChanged(_ on: Bool, virtualKey: CGKeyCode = 57) {
    let event = CGEvent(keyboardEventSource: nil, virtualKey: virtualKey, keyDown: true)!
    event.type = .flagsChanged
    event.flags = on ? .maskAlphaShift : []
    _ = controller.handle(NSEvent(cgEvent: event)!, client: client)
}

// #146: Caps release restores the engine's shared mode, which can still be
// English. Exercise the real controller and engine rather than mapping a bool
// to a label in isolation.
let peer = ControllerTestHost.session(inputAllowed: { true })
_ = try peer.handle(Request(type: "toggle"))
CandidatePanel.hints = []
capsChanged(true)
capsChanged(false)
let englishHints = CandidatePanel.hints
let englishHintOK = englishHints == ["英", "英"]
_ = try peer.handle(Request(type: "toggle"))
CandidatePanel.hints = []
capsChanged(true)
capsChanged(false)
let chineseHints = CandidatePanel.hints
let chineseHintOK = chineseHints == ["英", "中"]
peer.reset()
controller.inputControllerWillClose()
client = TextClient()
controller = LingxiInputController(client: client)
func input(caps: Bool) -> Bool {
    // Synthetic fixture, never an event from a real keyboard or user application.
    let event = NSEvent.keyEvent(with: .keyDown, location: .zero,
                                modifierFlags: caps ? [.capsLock] : [], timestamp: 0,
                                windowNumber: 0, context: nil, characters: "5",
                                charactersIgnoringModifiers: "5", isARepeat: false, keyCode: 23)!
    return controller.handle(event, client: client)
}
var results: [[String: Any]] = []
func record(_ name: String, _ handled: Bool) {
    results.append(["step": name, "handled": handled, "marked_length": client.markedLength,
                    "inserted_length": client.insertedLength])
}
record("caps_warm", input(caps: true))
try Data().write(to: URL(fileURLWithPath: CommandLine.arguments[2]))
let before = ProcessInfo.processInfo.systemUptime
record("warm_timeout", input(caps: true))
let elapsed = ProcessInfo.processInfo.systemUptime - before
record("caps_recovered", input(caps: true))
record("caps_repeated", input(caps: true))
record("caps_released", input(caps: false))
controller.inputControllerWillClose()
let warmOK = results[0]["handled"] as? Bool == false && results[0]["marked_length"] as? Int == 0
let failedOK = results[1]["handled"] as? Bool == false && elapsed >= 0.8 && elapsed < 2
let recoveredOK = results[2]["handled"] as? Bool == false && results[2]["marked_length"] as? Int == 0
let repeatedOK = results[3]["handled"] as? Bool == false && results[3]["marked_length"] as? Int == 0
let releasedOK = results[4]["handled"] as? Bool == true && results[4]["marked_length"] as? Int == 1
let noUnexpectedCommit = results.allSatisfy { $0["inserted_length"] as? Int == 0 }
let capsOK = warmOK && failedOK && recoveredOK && repeatedOK && releasedOK && noUnexpectedCommit

// A failed request must preserve the composition already shown to the host,
// exactly once, without retrying an ambiguously processed request.
client = TextClient()
controller = LingxiInputController(client: client)
record("composition_warm", input(caps: false))
let visible = client.marked
try Data().write(to: URL(fileURLWithPath: CommandLine.arguments[2]))
let failedHandled = input(caps: false)
record("composition_timeout", failedHandled)
let preserved = !visible.isEmpty && !failedHandled && client.inserted == [visible] && client.marked.isEmpty
let recoveredHandled = input(caps: false)
record("composition_recovered", recoveredHandled)
let compositionOK = preserved && recoveredHandled && !client.marked.isEmpty && client.inserted == [visible]
controller.inputControllerWillClose()

// Cold startup queues the Caps mode change before its keys. The real run loop
// replays them only once the private listener becomes available.
ControllerTestHost.socketPath = CommandLine.arguments[3]
client = TextClient()
controller = LingxiInputController(client: client)
CandidatePanel.hints = []
capsChanged(true)
let coldHintOK = CandidatePanel.hints.isEmpty
let coldFirst = input(caps: true)
record("cold_first", coldFirst)
let coldSecond = input(caps: true)
record("cold_second", coldSecond)
let buffered = coldFirst && coldSecond && client.marked.isEmpty && client.inserted.isEmpty
try Data().write(to: URL(fileURLWithPath: CommandLine.arguments[4]))
let replayDeadline = Date(timeIntervalSinceNow: 5)
while client.inserted.count < 2 && Date() < replayDeadline {
    RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.02))
}
let replayed = client.insertedLength == 2 && client.inserted.count == 2 && client.marked.isEmpty
let coldRecovered = input(caps: true)
record("cold_recovered", coldRecovered)
let coldReleased = input(caps: false)
record("cold_released", coldReleased)
let coldOK = buffered && replayed && !coldRecovered && coldReleased && client.markedLength == 1 && client.insertedLength == 2
controller.inputControllerWillClose()

// #219: Caps release returns to the mode from before Caps was turned on, unless
// a single Shift switched modes while Caps was on. Per-field mode memory, so
// only this controller's own session decides (shared mode is covered above).
ControllerTestHost.socketPath = CommandLine.arguments[5]
var capsHeld = false
func shiftTap(virtualKey: CGKeyCode = 60) {
    for flags: CGEventFlags in [.maskShift, []] {
        let event = CGEvent(keyboardEventSource: nil, virtualKey: virtualKey, keyDown: true)!
        event.type = .flagsChanged
        event.flags = capsHeld ? flags.union(.maskAlphaShift) : flags
        // Synthetic events carry no time of their own; the tap rules compare press, release and the last toggle.
        event.timestamp = CGEventTimestamp(ProcessInfo.processInfo.systemUptime * 1_000_000_000)
        _ = controller.handle(NSEvent(cgEvent: event)!, client: client)
    }
    Thread.sleep(forTimeInterval: 0.6) // past the duplicate-press window after a toggle
}
func nativeScenario(_ steps: [String]) -> [String: Any] {
    client = TextClient()
    controller = LingxiInputController(client: client)
    capsHeld = false
    CandidatePanel.hints = []
    for step in steps {
        switch step {
        case "shift": shiftTap()
        case "remote-shift": shiftTap(virtualKey: 0)
        case "caps+": capsHeld = true; capsChanged(true)
        case "remote-caps+": capsHeld = true; capsChanged(true, virtualKey: 0)
        case "remote-caps-": capsHeld = false; capsChanged(false, virtualKey: 0)
        default: capsHeld = false; capsChanged(false)
        }
    }
    let typedZhuyin = input(caps: false) // "5" is ㄓ in Zhuyin, handed back to the app in English
    controller.inputControllerWillClose()
    return ["steps": steps.joined(separator: " "), "hints": CandidatePanel.hints, "zhuyin_after": typedZhuyin]
}
let nativeCases: [([String], String)] = [
    (["shift", "caps+", "caps-"], "英"),          // English before Caps stays English
    (["caps+", "caps-"], "中"),                   // Zhuyin before Caps returns to Zhuyin (#84 unchanged)
    (["caps+", "shift", "shift", "caps-"], "英"), // switched back to English by hand while Caps was on
    (["shift", "caps+", "shift", "caps-"], "中"), // switched to Zhuyin by hand while Caps was on
    (["caps+", "shift", "caps-"], "中"),
    // #308：遠端的 Shift、Caps Lock（keyCode 0）跟本機一樣切換。
    (["remote-shift", "caps+", "caps-"], "英"),
    (["remote-caps+", "remote-caps-"], "中"),
    (["remote-caps+", "remote-shift", "remote-shift", "remote-caps-"], "英"),
]
var nativeResults: [[String: Any]] = []
var nativeOK = true
for (steps, want) in nativeCases {
    let result = nativeScenario(steps)
    nativeResults.append(result)
    let hints = result["hints"] as? [String] ?? []
    // Every case shows 英 right after Caps turns on (the step before it may be a Shift hint).
    let capsOnIndex = steps.firstIndex { $0.hasSuffix("caps+") }!
    let capsOnHint = hints.count == steps.count && hints[capsOnIndex] == "英"
    nativeOK = nativeOK && capsOnHint && hints.last == want && (result["zhuyin_after"] as? Bool) == (want == "中")
}

// #84：模擬 Chrome 裡每次 input 事件把輸入框 blur／focus 的欄位。組字一交給它就被結束、留成文字（殘字），
// App 隨即要求結束組字；插入文字後同樣再要求一次。真正的控制器與引擎，只有 App 是替身。
final class PageClient: IMKTextInput {
    var document = ""
    var marked = ""
    var endsComposition = true
    var setMarkedCount = 0
    var bundle = "test.page"
    // 網頁攔下 Enter 的 keydown（cmdk 等搜尋框按 Enter 開啟結果時 preventDefault）。
    var preventsEnter = false
    weak var controller: LingxiInputController?
    private var pendingEnd = false
    // Chromium 處理按鍵時：沒給位置的 insertText 先記下，按鍵處理完才決定怎麼送（render_widget_host_view_cocoa.mm keyEvent:）。
    private(set) var handlingKey = false
    private var markedBeforeKey = false
    private var toBeInserted = ""
    var caret: Int { (document as NSString).length }
    func bundleIdentifier() -> String! { bundle }
    func overrideKeyboard(withKeyboardNamed: String!) {}
    func insertText(_ text: Any!, replacementRange: NSRange) {
        let string = (text as? String) ?? (text as? NSAttributedString)?.string ?? ""
        if handlingKey, replacementRange.location == NSNotFound {
            toBeInserted += string
            marked = ""
            return
        }
        let base = document as NSString
        if replacementRange.location != NSNotFound {
            document = base.replacingCharacters(in: replacementRange, with: string)
        } else {
            document += string
        }
        marked = ""
        if endsComposition { pendingEnd = true }
    }
    func selectedRange() -> NSRange { NSRange(location: caret, length: 0) }
    func markedRange() -> NSRange {
        marked.isEmpty ? NSRange(location: NSNotFound, length: 0) : NSRange(location: caret, length: (marked as NSString).length)
    }
    func attributedSubstring(from range: NSRange) -> NSAttributedString! {
        guard range.location != NSNotFound, NSMaxRange(range) <= caret else { return nil }
        return NSAttributedString(string: (document as NSString).substring(with: range))
    }
    func attributes(forCharacterIndex: Int, lineHeightRectangle: UnsafeMutablePointer<NSRect>!) -> [AnyHashable: Any]! { [:] }
    func firstRect(forCharacterRange: NSRange, actualRange: NSRangePointer!) -> NSRect { .zero }
    func windowLevel() -> Int32 { 0 }
    func setMarkedText(_ text: Any!, selectionRange: NSRange, replacementRange: NSRange) {
        marked = (text as? NSAttributedString)?.string ?? ""
        setMarkedCount += 1
        if endsComposition, !marked.isEmpty { pendingEnd = true }
    }
    func beginKey() {
        handlingKey = true
        markedBeforeKey = !marked.isEmpty
        toBeInserted = ""
    }
    /// 按鍵處理完，照 Chromium 的規則送出記下的文字：App 裡前後有組字或文字超過一個 UTF-16 單位時當成輸入法送出的文字直接插入；
    /// 只有一個單位時當成這個鍵打出的字元，跟在 keydown 後面——網頁攔下這個鍵（preventDefault）就不會插入。
    func endKey(enter: Bool) {
        handlingKey = false
        let text = toBeInserted
        toBeInserted = ""
        guard !text.isEmpty else { return }
        let viaIME = markedBeforeKey || !marked.isEmpty || (text as NSString).length > 1
        if viaIME || !(enter && preventsEnter) { insertText(text, replacementRange: NSRange(location: NSNotFound, length: 0)) }
    }
    /// 網頁的 input 事件處理完：blur／focus 讓 Chrome 把組字留成文字，IMK 隨後呼叫 commitComposition。
    func settle() {
        guard pendingEnd else { return }
        pendingEnd = false
        document += marked
        marked = ""
        controller?.commitComposition(self)
    }
}
func pageKey(_ page: PageClient, _ characters: String, code: UInt16 = 0) {
    let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: 0, context: nil,
                                 characters: characters, charactersIgnoringModifiers: characters, isARepeat: false, keyCode: code)!
    page.beginKey()
    _ = page.controller?.handle(event, client: page)
    page.endKey(enter: code == 36)
    page.settle()
}
func typeWord(_ page: PageClient, end: (String, UInt16) = ("\r", 36)) {
    for k in ["a", "8", "6", "q", "i", "6"] { pageKey(page, k) }
    pageKey(page, end.0, code: end.1)
}
ControllerTestHost.socketPath = CommandLine.arguments[5]
let page = PageClient()
controller = LingxiInputController(client: page)
page.controller = controller
ShellStats.shared.floating = []
CandidatePanel.compositions = []
typeWord(page)
let firstWord = page.document
let firstFloated = CandidatePanel.compositions.count >= 5 && ShellStats.shared.floating.count == 1
typeWord(page)
let secondWord = page.document
let markedBeforeSticky = page.setMarkedCount
let stuck = ShellStats.shared.floating.count == 2 && ShellStats.shared.floating[1].1 && ShellStats.shared.floating[1].0 == "test.page"
typeWord(page)
let thirdWord = page.document
let stickyNeverMarked = page.setMarkedCount == markedBeforeSticky
// 固定浮動的欄位送出一個字、網頁攔下 Enter（搜尋框按 Enter 開啟結果）：那個字照樣要進到欄位，不能跟著按鍵被網頁攔掉
page.preventsEnter = true
for k in ["a", "8", "6"] { pageKey(page, k) }
pageKey(page, "\r", code: 36)
let singleWord = page.document
let singleKept = singleWord.hasPrefix(thirdWord) && singleWord.count == thirdWord.count + 1
page.preventsEnter = false
// 救回的組字按 Esc 取消：殘字也刪掉
let cancelPage = PageClient()
controller.inputControllerWillClose()
controller = LingxiInputController(client: cancelPage)
cancelPage.controller = controller
pageKey(cancelPage, "a")
pageKey(cancelPage, "8")
pageKey(cancelPage, "\u{1b}", code: 53)
let cancelled = cancelPage.document
// 一般欄位：組字留在 App 裡；按鍵後過一陣子 App 才結束組字（點到別處），照常送出，不改成浮動
let normal = PageClient()
normal.endsComposition = false
controller.inputControllerWillClose()
controller = LingxiInputController(client: normal)
normal.controller = controller
ShellStats.shared.floating = []
for k in ["a", "8", "6"] { pageKey(normal, k) }
Thread.sleep(forTimeInterval: FloatingComposition.earlyEnd + 0.1)
let shownBeforeClick = normal.marked
controller.commitComposition(normal)
let normalOK = !shownBeforeClick.isEmpty && normal.document == shownBeforeClick && normal.marked.isEmpty && ShellStats.shared.floating.isEmpty && normal.setMarkedCount == 3
controller.inputControllerWillClose()
// Electron App 的一般欄位照常 inline（負責人 2026-11-06：不依 App 種類一律浮動）：每鍵都交給 App 組字，沒有改畫在面板。
// 前面的測試已讓引擎學到「嘛」，所以只比對兩個字。
let electron = PageClient()
electron.endsComposition = false
electron.bundle = "test.electron"
controller = LingxiInputController(client: electron)
electron.controller = controller
CandidatePanel.compositions = []
ShellStats.shared.floating = []
for k in ["a", "8", "6", "q", "i", "6"] { pageKey(electron, k) }
let electronInline = !electron.marked.isEmpty && electron.setMarkedCount >= 6 && CandidatePanel.compositions.isEmpty
pageKey(electron, "\r", code: 36)
let electronWord = electron.document
let electronOK = electronInline && electronWord.count == 2 && electron.marked.isEmpty && ShellStats.shared.floating.isEmpty
controller.inputControllerWillClose()
// 會結束組字的 Electron 欄位（Claude 桌面版的搜尋框）：跟 Chrome 一樣靠偵測救回、殘字換掉，兩次後固定浮動
let electronBad = PageClient()
electronBad.bundle = "test.electron"
controller = LingxiInputController(client: electronBad)
electronBad.controller = controller
ShellStats.shared.floating = []
typeWord(electronBad)
typeWord(electronBad)
let electronBadOK = electronBad.document == electronWord + electronWord && ShellStats.shared.floating.count == 2 &&
    ShellStats.shared.floating[1].1 && ShellStats.shared.floating[1].0 == "test.electron"
let markedBeforeThird = electronBad.setMarkedCount
typeWord(electronBad)
let electronBadSticky = electronBad.document == electronWord + electronWord + electronWord && electronBad.setMarkedCount == markedBeforeThird
controller.inputControllerWillClose()

let floatingOK = firstWord == "麻婆" && firstFloated && secondWord == "麻婆麻婆" && stuck &&
    thirdWord == "麻婆麻婆麻婆" && stickyNeverMarked && singleKept && cancelled.isEmpty && normalOK &&
    electronOK && electronBadOK && electronBadSticky
let floatingResult: [String: Any] = ["first": firstWord, "second": secondWord, "third": thirdWord, "cancelled": cancelled, "single": singleWord,
                                     "normal": normal.document, "sticky_never_marked": stickyNeverMarked, "stuck": stuck,
                                     "electron_inline": electronOK, "electron": electronWord, "electron_bad": electronBad.document,
                                     "electron_bad_sticky": electronBadSticky]

// #283：沒在組字時按 ` 開的符號表。Office（Word／PowerPoint／Excel）與終端機一類的 App 只在自己有組字（marked text）時
// 才把方向鍵、Esc 交給輸入法，沒有組字就自己處理（移動游標、叫出上一個指令）。符號表開著時要在 App 裡放一個佔位的組字，
// 方向鍵才進得到候選窗；關掉或選了符號之後佔位要拿掉，不能在文件裡留下空白。
final class OfficeClient: IMKTextInput {
    var document = ""
    var marked = ""
    var leaked: [String] = [] // App 自己處理掉的具名鍵
    var setMarkedCount = 0
    weak var controller: LingxiInputController?
    func bundleIdentifier() -> String! { "test.office" }
    func overrideKeyboard(withKeyboardNamed: String!) {}
    func insertText(_ text: Any!, replacementRange: NSRange) {
        document += (text as? String) ?? (text as? NSAttributedString)?.string ?? ""
        marked = ""
    }
    func selectedRange() -> NSRange { NSRange(location: (document as NSString).length, length: 0) }
    func markedRange() -> NSRange {
        marked.isEmpty ? NSRange(location: NSNotFound, length: 0)
            : NSRange(location: (document as NSString).length, length: (marked as NSString).length)
    }
    func attributedSubstring(from range: NSRange) -> NSAttributedString! { nil }
    func attributes(forCharacterIndex: Int, lineHeightRectangle: UnsafeMutablePointer<NSRect>!) -> [AnyHashable: Any]! { [:] }
    func firstRect(forCharacterRange: NSRange, actualRange: NSRangePointer!) -> NSRect { .zero }
    func windowLevel() -> Int32 { 0 }
    func setMarkedText(_ text: Any!, selectionRange: NSRange, replacementRange: NSRange) {
        marked = (text as? NSAttributedString)?.string ?? (text as? String) ?? ""
        setMarkedCount += 1
    }
    /// 具名鍵（方向鍵、Esc、Enter）沒有組字時 App 自己處理，不問輸入法；有字的鍵一律先問輸入法。
    func key(_ characters: String, code: UInt16, named: Bool = false) {
        if named && marked.isEmpty { leaked.append(characters); return }
        let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: 0, context: nil,
                                     characters: characters, charactersIgnoringModifiers: characters, isARepeat: false, keyCode: code)!
        if controller?.handle(event, client: self) != true, named { leaked.append(characters) }
    }
}
let down = "\u{F701}", right = "\u{F703}"
var office = OfficeClient()
controller = LingxiInputController(client: office)
office.controller = controller
CandidatePanel.candidates = []
office.key("`", code: 50)
let menuMarked = office.marked
office.key(down, code: 125, named: true)
office.key(down, code: 125, named: true)
let highlightAfterDown = CandidatePanel.candidates.last?.highlight
let markedCountBeforeRight = office.setMarkedCount
office.key(right, code: 124, named: true)
let pageAfterRight = CandidatePanel.candidates.last?.page
let placeholderNotResent = office.setMarkedCount == markedCountBeforeRight // 同樣的佔位不重送（#108 閃爍）
office.key("\u{1b}", code: 53, named: true)
let closedClean = office.marked.isEmpty && office.document.isEmpty
office.key("`", code: 50)
office.key("1", code: 18)
let symbolMarked = office.marked
office.key("\r", code: 36, named: true)
let symbolCommitted = office.document
// 符號表開著時點到別處（App 結束組字）與切走輸入法：佔位不能變成文件裡的空白。
office.key("`", code: 50)
Thread.sleep(forTimeInterval: FloatingComposition.earlyEnd + 0.1)
controller.commitComposition(office)
let clickedAway = office.document == symbolCommitted && office.marked.isEmpty
office.key("`", code: 50)
controller.deactivateServer(office)
let switchedAway = office.document == symbolCommitted && office.marked.isEmpty
controller.inputControllerWillClose()
let officeOK = !menuMarked.isEmpty && highlightAfterDown == 2 && pageAfterRight == 1 && placeholderNotResent && closedClean &&
    symbolMarked == "，" && symbolCommitted == "，" && clickedAway && switchedAway && office.leaked.isEmpty
let officeResult: [String: Any] = ["menu_marked_length": (menuMarked as NSString).length, "highlight_after_down": highlightAfterDown ?? -1,
                                   "page_after_right": pageAfterRight ?? -1, "placeholder_not_resent": placeholderNotResent,
                                   "closed_clean": closedClean, "symbol_marked": symbolMarked, "committed": symbolCommitted,
                                   "clicked_away": clickedAway, "switched_away": switchedAway, "leaked": office.leaked.count]

// #347：組字中從選單或 ⌃⇧F11 切「輸出簡體字」。App 回報不出 markedRange（NSNotFound，浮動組字一類的 App）時
// 組字一定重送給 App；這一步不在請求佇列的處理中，RequestQueue.present 的 precondition 會讓整個輸入法當掉。
final class NoMarkedRangeClient: IMKTextInput {
    var document = ""
    var marked = ""
    var setMarkedCount = 0
    func bundleIdentifier() -> String! { "test.no-marked-range" }
    func overrideKeyboard(withKeyboardNamed: String!) {}
    func insertText(_ text: Any!, replacementRange: NSRange) {
        document += (text as? String) ?? (text as? NSAttributedString)?.string ?? ""
        marked = ""
    }
    func selectedRange() -> NSRange { NSRange(location: (document as NSString).length, length: 0) }
    func markedRange() -> NSRange { NSRange(location: NSNotFound, length: 0) }
    func attributedSubstring(from range: NSRange) -> NSAttributedString! { nil }
    func attributes(forCharacterIndex: Int, lineHeightRectangle: UnsafeMutablePointer<NSRect>!) -> [AnyHashable: Any]! { [:] }
    func firstRect(forCharacterRange: NSRange, actualRange: NSRangePointer!) -> NSRect { .zero }
    func windowLevel() -> Int32 { 0 }
    func setMarkedText(_ text: Any!, selectionRange: NSRange, replacementRange: NSRange) {
        marked = (text as? NSAttributedString)?.string ?? (text as? String) ?? ""
        setMarkedCount += 1
    }
}
let loose = NoMarkedRangeClient()
controller = LingxiInputController(client: loose)
func looseKey(_ characters: String, code: UInt16 = 0) {
    let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: 0, context: nil,
                                 characters: characters, charactersIgnoringModifiers: characters, isARepeat: false, keyCode: code)!
    _ = controller.handle(event, client: loose)
}
for k in ["a", "8", "6"] { looseKey(k) }
let looseComposing = !loose.marked.isEmpty && loose.document.isEmpty
let simplifiedLiveKnown = EngineSession.simplifiedLive
let markedBeforeSwitch = loose.setMarkedCount
controller.simplifiedMenuAction(nil)
let switchedOn = EngineSession.outputSimplified && loose.setMarkedCount > markedBeforeSwitch && !loose.marked.isEmpty && loose.document.isEmpty
controller.simplifiedMenuAction(nil)
let switchedOff = !EngineSession.outputSimplified && !loose.marked.isEmpty && loose.document.isEmpty
let composedBeforeCommit = loose.marked
looseKey("\r", code: 36)
let simplifiedSwitchOK = looseComposing && simplifiedLiveKnown && switchedOn && switchedOff &&
    !composedBeforeCommit.isEmpty && loose.document == composedBeforeCommit && loose.marked.isEmpty
let simplifiedSwitchResult: [String: Any] = ["composing": looseComposing, "live": simplifiedLiveKnown, "on": switchedOn,
                                             "off": switchedOff, "committed": loose.document]
controller.inputControllerWillClose()

// #337：開始組字的第一鍵讀游標前一個字是不是阿拉伯數字，只帶 digit_before 是／不是。在 ABC 打的數字不經過輸入法，
// 沒在組字時點到別處 IMKit 也不通知輸入法，這裡用改文件內容代替（不呼叫控制器）。代理只記每個 key 請求的 digit_before。
final class CaretClient: IMKTextInput {
    var document = ""
    var marked = ""
    var selectionLength = 0
    var readable = true
    var reads = 0
    func bundleIdentifier() -> String! { "test.caret" }
    func overrideKeyboard(withKeyboardNamed: String!) {}
    func insertText(_ text: Any!, replacementRange: NSRange) {
        document += (text as? String) ?? (text as? NSAttributedString)?.string ?? ""
        marked = ""
    }
    func selectedRange() -> NSRange {
        NSRange(location: (document as NSString).length - selectionLength, length: selectionLength)
    }
    func markedRange() -> NSRange {
        marked.isEmpty ? NSRange(location: NSNotFound, length: 0)
            : NSRange(location: (document as NSString).length, length: (marked as NSString).length)
    }
    func attributedSubstring(from range: NSRange) -> NSAttributedString! {
        reads += 1
        guard readable, range.location != NSNotFound, NSMaxRange(range) <= (document as NSString).length else { return nil }
        return NSAttributedString(string: (document as NSString).substring(with: range))
    }
    func attributes(forCharacterIndex: Int, lineHeightRectangle: UnsafeMutablePointer<NSRect>!) -> [AnyHashable: Any]! { [:] }
    func firstRect(forCharacterRange: NSRange, actualRange: NSRangePointer!) -> NSRect { .zero }
    func windowLevel() -> Int32 { 0 }
    func setMarkedText(_ text: Any!, selectionRange: NSRange, replacementRange: NSRange) {
        marked = (text as? NSAttributedString)?.string ?? (text as? String) ?? ""
    }
}
let digitLog = URL(fileURLWithPath: CommandLine.arguments[6])
func digitLines() -> [String] {
    ((try? String(contentsOf: digitLog, encoding: .utf8)) ?? "").split(separator: "\n").map(String.init)
}
let caret = CaretClient()
func caretKey(_ characters: String, code: UInt16, caps: Bool = false) -> [String] {
    let before = digitLines().count
    let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: caps ? [.capsLock] : [], timestamp: 0,
                                 windowNumber: 0, context: nil, characters: characters, charactersIgnoringModifiers: characters,
                                 isARepeat: false, keyCode: code)!
    _ = controller.handle(event, client: caret)
    return Array(digitLines().dropFirst(before))
}
func caretCaps(_ on: Bool) {
    let event = CGEvent(keyboardEventSource: nil, virtualKey: 57, keyDown: true)!
    event.type = .flagsChanged
    event.flags = on ? .maskAlphaShift : []
    _ = controller.handle(NSEvent(cgEvent: event)!, client: caret)
}
func caretShift() {
    for flags: CGEventFlags in [.maskShift, []] {
        let event = CGEvent(keyboardEventSource: nil, virtualKey: 60, keyDown: true)!
        event.type = .flagsChanged
        event.flags = flags
        event.timestamp = CGEventTimestamp(ProcessInfo.processInfo.systemUptime * 1_000_000_000)
        _ = controller.handle(NSEvent(cgEvent: event)!, client: caret)
    }
    Thread.sleep(forTimeInterval: 0.6)
}
let esc = "\u{1b}"
ControllerTestHost.socketPath = CommandLine.arguments[1]
controller = LingxiInputController(client: caret)
var digitCases: [String: Any] = [:]
caret.document = "3" // 在 ABC 打的 3
let afterDigit = caretKey("5", code: 23) // ㄓ
let digitRead = caret.reads
let composingKey = caretKey("j", code: 38) // 組字中：不讀、不帶
let digitTrue = afterDigit == ["true"] && digitRead == 1 && composingKey == ["none"] && caret.reads == 1 && !caret.marked.isEmpty
_ = caretKey(esc, code: 53)
caret.document = "3\n" // 點到沒有數字的空行（沒有組字，IMKit 不通知輸入法）
let afterClick = caretKey("5", code: 23)
let digitFalse = afterClick == ["false"] && caret.reads == 2
_ = caretKey(esc, code: 53)
caret.document = ""
let atStart = caretKey("5", code: 23)
_ = caretKey(esc, code: 53)
caret.document = "3"
caret.selectionLength = 1
let readsBeforeSelection = caret.reads
let withSelection = caretKey("5", code: 23)
let selectionNone = withSelection == ["none"] && caret.reads == readsBeforeSelection
_ = caretKey(esc, code: 53)
caret.selectionLength = 0
caret.readable = false
let unreadable = caretKey("5", code: 23)
_ = caretKey(esc, code: 53)
caret.readable = true
let readsBeforeCaps = caret.reads
caretCaps(true)
let capsEnglish = caretKey("5", code: 23, caps: true) // 英文模式：數字交還 App，不讀
caretCaps(false)
let capsNotRead = capsEnglish == ["none"] && caret.reads == readsBeforeCaps
controller.inputControllerWillClose()
// 右 Shift 的英文模式也不讀（每欄位記模式、右 Shift 切換的引擎）；切回注音再讀。
ControllerTestHost.socketPath = CommandLine.arguments[5]
controller = LingxiInputController(client: caret)
caret.document = "a"
_ = caretKey("5", code: 23) // 先讓快照知道是注音
_ = caretKey(esc, code: 53)
let readsBeforeShift = caret.reads
caretShift()
_ = caretKey("3", code: 20)
let shiftNotRead = caret.reads == readsBeforeShift && caret.marked.isEmpty
caretShift()
_ = caretKey("5", code: 23)
let shiftBackRead = caret.reads == readsBeforeShift + 1
_ = caretKey(esc, code: 53)
controller.inputControllerWillClose()
let digitBeforeOK = digitTrue && digitFalse && atStart == ["false"] && selectionNone && unreadable == ["none"] && capsNotRead &&
    shiftNotRead && shiftBackRead
digitCases = ["after_digit": afterDigit, "composing": composingKey, "after_click": afterClick, "document_start": atStart,
              "selection": withSelection, "unreadable": unreadable, "caps_english": capsEnglish, "caps_not_read": capsNotRead,
              "shift_not_read": shiftNotRead, "shift_back_read": shiftBackRead, "reads": caret.reads]

let passed = capsOK && compositionOK && coldOK && englishHintOK && chineseHintOK && coldHintOK && nativeOK && floatingOK && officeOK &&
    simplifiedSwitchOK && digitBeforeOK
let output: [String: Any] = ["passed": passed, "floating": floatingOK, "floating_cases": floatingResult,
                          "digit_before": digitBeforeOK, "digit_before_cases": digitCases,
                          "simplified_switch_composing": simplifiedSwitchOK, "simplified_switch_cases": simplifiedSwitchResult,
                          "symbol_menu_office": officeOK, "symbol_menu_cases": officeResult,
                          "caps_recovery": capsOK, "composition_preserved_once": compositionOK,
                          "native_caps_restore": nativeOK, "native_cases": nativeResults,
                          "cold_queue": coldOK, "timeout_seconds": elapsed, "steps": results,
                          "english_hints": englishHints, "chinese_hints": chineseHints,
                          "cold_hint_suppressed": coldHintOK]
print(String(data: try JSONSerialization.data(withJSONObject: output, options: [.sortedKeys]), encoding: .utf8)!)
exit(passed ? 0 : 1)
