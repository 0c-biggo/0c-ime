import AppKit
import Foundation
import Darwin

var checks = 0
func check(_ condition: Bool, _ message: String) {
    checks += 1
    if !condition { fatalError(message) }
}

func run() throws {
    guard CommandLine.arguments.count == 3 else { fatalError("usage: integration-test server data") }
    let fm = FileManager.default
    // Short, isolated paths also work on Darwin's 104-byte sockaddr_un.
    let directory = URL(fileURLWithPath: "/tmp/zuin-mac-test-\(UUID().uuidString)")
    try fm.createDirectory(at: directory, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
    defer { try? fm.removeItem(at: directory) }
    let unavailable = EngineSession(connection: EngineConnection(path: directory.appendingPathComponent("absent.sock").path), inputAllowed: { true })
    check(!unavailable.shiftToggles(left: false) && !unavailable.shiftToggles(left: true),
          "before the engine reports settings, neither Shift key toggles on macOS")
    try Data(#"{"schema":1,"selection":"statistical","punctuation":false,"candidate_layout":"grid","emoji_shortcut":"equals","emoji_equals_first":false}"#.utf8)
        .write(to: directory.appendingPathComponent("settings.json"))
    // 較新版本寫成的詞彙檔：引擎照常輸入，但不能改自訂詞彙，open 不回報 vocab_keys（#123）。vocabKeyChecks 才移掉它。
    try Data(#"{"format":99,"words":[]}"#.utf8).write(to: directory.appendingPathComponent("vocab.json"))
    let socketPath = directory.appendingPathComponent("engine.sock").path
    var process: Process?
    func stop() {
        if let p = process, p.isRunning { p.terminate(); p.waitUntilExit() }
        process = nil
    }
    defer { stop() }
    let start = {
        if process?.isRunning == true { return }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: CommandLine.arguments[1])
        p.arguments = ["-socket", socketPath, "-data", CommandLine.arguments[2],
                       "-user", directory.appendingPathComponent("user.json").path]
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        try p.run()
        process = p
    }
    let session = EngineSession(connection: EngineConnection(path: socketPath, start: start), inputAllowed: { true })
    // 與外殼相同：引擎冷啟動時回報 starting，稍後重送同一個（尚未送出的）請求。
    func key(_ key: String) throws -> Snapshot {
        let deadline = ProcessInfo.processInfo.systemUptime + 10
        while true {
            do { return try session.handle(Request(type: "key", key: key)) }
            catch EngineError.starting where ProcessInfo.processInfo.systemUptime < deadline { Thread.sleep(forTimeInterval: 0.05) }
        }
    }
    var snapshot = try key("a")
    check(!session.shiftToggles(left: false) && !session.shiftToggles(left: true),
          "an engine without a saved toggle_key reports both Shift keys off on macOS")
    for k in ["8", "6", "q", "i", "6"] { snapshot = try key(k) }
    check(snapshot.text == "麻婆", "preedit: \(snapshot.text)")
    check(snapshot.cursor.utf16 == 2, "UTF-16 cursor")
    check(snapshot.suggestion == "豆腐", "completion")
    check(try key("Tab").text == "麻婆豆腐", "Tab accepts completion")
    snapshot = try key("Enter")
    check(snapshot.commit == "麻婆豆腐" && snapshot.text.isEmpty, "commit and clear")
    check(try !key("Enter").handled, "empty Enter passes through")
    check(try !key("Tab").handled, "empty Tab passes through")
    check(try key("<").text == "，", "full-width punctuation")
    check(try key("Escape").text.isEmpty, "cancel")
    check(!session.vocabKeys, "the #123 key names stay off while vocab cannot be changed")
    // 引擎沒回報 vocab_keys：Shift＋←／→ 照舊對應成 ←／→，組字中不會選到 App 的文字。
    check(KeyMapping.key(code: 123, characters: nil, modifiers: [.shift], vocabKeys: session.vocabKeys) == "Left",
          "Shift+Left is Left when the engine has no vocab keys")

    // Actual engine requests with simulated synchronous IMKit render callbacks.
    // These 20 rounds test ordering; they are not logged-in IMKit GUI acceptance.
    for round in 0..<20 {
        _ = try session.handle(Request(type: "cancel"))
        let queue = RequestQueue()
        for k in ["a", "8", "6", "q", "i", "6"] { queue.append(Request(type: "key", key: k), characters: k) }
        var delivered = 0
        var commits: [String] = []
        try queue.drain { item in
            let next = try session.handle(item.request)
            delivered += 1
            if let text = next.commit, !text.isEmpty {
                check(delivered == 7, "reentrant commit must follow all six old keys (round \(round))")
                commits.append(text)
            }
            if delivered == 1 {
                queue.append(Request(type: "commit"))
                queue.append(Request(type: "mode", on: true))
                queue.append(Request(type: "mode", on: false))
                for k in ["q", "i", "6"] { queue.append(Request(type: "key", key: k), characters: k) }
            }
            snapshot = next
        }
        check(commits == ["麻婆"], "old composition must be committed exactly once")
        check(snapshot.text == "婆" && delivered == 12, "new keys must follow mode requests in order")
    }
    _ = try session.handle(Request(type: "cancel"))

    snapshot = try key("=")
    check(snapshot.candidates?.columns == 5 && snapshot.candidates?.kind == "emoji", "palette grid protocol")
    _ = try key("Right")
    snapshot = try key("Down")
    check(snapshot.candidates?.highlight == 6, "palette spatial keyboard navigation")
    check(try key("Enter").commit == "😂", "palette sends exactly the selected emoji")
    snapshot = try key("=")
    check(snapshot.candidates?.items.first?.text == "😂", "recent choice first")
    check(try key("=").commit == "=", "literal equals escape")
    for k in ["j", "i", "3"] { snapshot = try key(k) }
    _ = try key("=")
    snapshot = try session.handle(Request(type: "page", index: 11))
    guard let family = snapshot.candidates?.items.first(where: { $0.text == "👨‍👩‍👧‍👦" }) else {
        fatalError("family emoji missing from fixed catalog")
    }
    snapshot = try session.handle(Request(type: "select", index: family.id))
    check(snapshot.text == "我👨‍👩‍👧‍👦" && snapshot.cursor.utf16 == 12 && (snapshot.commit ?? "").isEmpty,
          "full family sequence inserted into Chinese composition")
    check(try key("Left").cursor.utf16 == 1, "navigation preserves emoji grapheme")
    check(try key("Delete").text == "我", "forward delete removes whole emoji")
    _ = try key("=")
    snapshot = try key("Tab")
    check(snapshot.candidates?.columns == 2 && snapshot.candidates?.kind == "kaomoji", "wide kaomoji cells")
    check(try key("Enter").text == "我(^_^)", "complete kaomoji insertion")
    check(try key("Backspace").text == "我", "immediate kaomoji insertion undo")
    _ = try key("Escape")

    _ = try key("]")
    snapshot = try key("Down")
    guard let candidates = snapshot.candidates, candidates.items.count >= 2 else { fatalError("missing candidates") }
    let chosen = candidates.items[1]
    snapshot = try session.handle(Request(type: "select", index: chosen.id))
    check(snapshot.text == chosen.text && snapshot.candidates == nil, "mouse selects candidate ID")
    _ = try key("Escape")

    for k in ["v", "u", "l", "3", "x", "j", "/", "6"] { snapshot = try key(k) }
    snapshot = try key("Tab")
    check(snapshot.text == "小龍" && snapshot.suggestion == "小龍 → 小籠包", "replacement preview is separate from preedit")
    check(try key("Tab").text == "小籠包", "second Tab accepts replacement")
    _ = try key("Escape")

    // With no saved candidate_cursor setting, macOS modifies the character
    // before the caret. Exercise the same key requests used by the IMK shell.
    for k in ["j", "i", "3", "t", " "] { snapshot = try key(k) }
    _ = try key("Left")
    snapshot = try key("Down")
    check(snapshot.candidates?.items.contains(where: { $0.text == "我" }) == true &&
          snapshot.candidates?.items.contains(where: { $0.text == "吃" }) == false,
          "macOS default candidates are before the caret")
    check(snapshot.cursor.utf16 == 1, "opening candidates preserves caret")
    check(snapshot.candidates?.title == "修改「我」 · Tab 切換詞長", "single-character replacement heading")
    _ = try key("Escape")
    check(try key("Escape").text.isEmpty, "cancel before-caret composition")
    for k in ["a", "8", "6", "q", "i", "6"] { snapshot = try key(k) }
    snapshot = try key("Tab")
    check(snapshot.text == "麻婆豆腐", "complete-word fixture from accepted completion")
    snapshot = try key("Down")
    check(snapshot.candidates?.items.contains(where: { $0.text == "麻婆豆腐" }) == true,
          "before-caret candidates include the complete final word")
    check(snapshot.candidates?.title == "修改「麻婆豆腐」 · Tab 切換詞長", "whole-word replacement heading")
    snapshot = try key("Tab")
    check(snapshot.candidates?.items.contains(where: { $0.text == "腐" }) == true &&
          snapshot.candidates?.items.contains(where: { $0.text == "麻婆豆腐" }) == false,
          "candidate length changes keep the right edge at the caret")
    check(snapshot.candidates?.title == "修改「腐」 · Tab 切換詞長", "Tab must refresh the replacement heading")
    _ = try key("Escape")
    _ = try key("Escape")

    // Distinct clients must not inherit another field's composition.
    _ = try key("a")
    let other = EngineSession(connection: EngineConnection(path: socketPath, start: start), inputAllowed: { true })
    check(try !other.handle(Request(type: "key", key: "Enter")).handled, "field isolation")
    other.reset()
    session.reset()
    check(try !key("Enter").handled, "deactivation resets session")

    // A sensitive request must not connect, start the engine or open a session.
    var sensitiveLaunches = 0
    let protected = EngineSession(connection: EngineConnection(path: directory.appendingPathComponent("never.sock").path,
        start: { sensitiveLaunches += 1 }), inputAllowed: { false })
    for request in [Request(type: "key", key: "a"), Request(type: "key", key: "="), Request(type: "commit"),
                    Request(type: "select", index: 0), Request(type: "mode", on: true),
                    Request(type: "context", on: true)] {
        var denied = false
        do { _ = try protected.handle(request) } catch EngineError.secureInput { denied = true }
        check(denied, "secure input must reject every engine request path")
    }
    check(sensitiveLaunches == 0 && !protected.isOpen && !protected.ready(), "secure input must not start or connect")
    var ordinary = true
    let transitioning = EngineSession(connection: EngineConnection(path: socketPath, start: start), inputAllowed: { ordinary })
    check(try transitioning.handle(Request(type: "key", key: "a")).text == "ㄇ", "ordinary input before secure transition")
    ordinary = false
    do { _ = try transitioning.handle(Request(type: "commit")); fatalError("secure commit was accepted") }
    catch EngineError.secureInput { }
    check(!transitioning.isOpen, "secure transition disconnects old composition and context")
    ordinary = true
    check(try !transitioning.handle(Request(type: "key", key: "Enter")).handled, "ordinary input resumes in a fresh session")
    transitioning.reset()

    var shift = RightShiftTap()
    check(!shift.flags(pressed: true, modified: false) && shift.flags(pressed: false, modified: false), "a lone right Shift tap toggles")
    // #71：Chromium／Electron 把每個 flagsChanged 交給輸入法兩次（例如 VS Code、Chrome）。
    check(!shift.flags(pressed: true, modified: false) && !shift.flags(pressed: true, modified: false), "duplicate presses do not toggle")
    check(shift.flags(pressed: false, modified: false), "Chromium's duplicated press still toggles once on release")
    check(!shift.flags(pressed: false, modified: false), "the duplicated release does not toggle back")
    _ = shift.flags(pressed: true, modified: false); shift.otherKey()
    check(!shift.flags(pressed: false, modified: false), "Shift plus a letter is not a tap")
    _ = shift.flags(pressed: true, modified: false); _ = shift.flags(pressed: true, modified: true)
    check(!shift.flags(pressed: false, modified: false), "Command during the press cancels the tap")
    check(!shift.flags(pressed: true, modified: true) && !shift.flags(pressed: false, modified: true), "modified press is not a tap")
    _ = shift.flags(pressed: true, modified: false); shift.reset()
    check(!shift.flags(pressed: false, modified: false), "focus loss forgets the press")
    // #117：按住超過 0.3 秒才放開不算單按（想打大寫最後沒打）；重複的按下不重設計時。
    check(!shift.flags(pressed: true, modified: false, at: 10) && shift.flags(pressed: false, modified: false, at: 10.25),
          "a 0.25 s tap toggles")
    _ = shift.flags(pressed: true, modified: false, at: 20)
    check(!shift.flags(pressed: false, modified: false, at: 20.4), "holding Shift for 0.4 s is not a tap")
    _ = shift.flags(pressed: true, modified: false, at: 30); _ = shift.flags(pressed: true, modified: false, at: 30.2)
    check(!shift.flags(pressed: false, modified: false, at: 30.35), "Chromium's duplicated press does not restart the timer")
    _ = shift.flags(pressed: true, modified: false, at: 40)
    check(shift.flags(pressed: false, modified: false, at: 40.4, limit: 0.5), "the engine's shift_tap_ms sets the limit")
    // #148：網頁忙時 Chrome 把重複的那一對（按下、放開）延到使用者放開之後才送（測試以 0.1 秒單按，
    // 重複的按下晚 0.13 秒、送來時 Shift 早已放開）。照舊會切換兩次、看起來 Shift 沒反應；Shift 實際上沒按著就不是新的按下。
    var busy = RightShiftTap()
    check(!busy.flags(pressed: true, modified: false, at: 50, held: true) && busy.flags(pressed: false, modified: false, at: 50.1),
          "a 0.1 s tap in a busy Chrome page toggles")
    check(!busy.flags(pressed: true, modified: false, at: 50.23, held: false) && !busy.flags(pressed: false, modified: false, at: 50.24),
          "Chrome's late duplicate press and release after the key is up do not toggle back")
    check(!busy.flags(pressed: true, modified: false, at: 50.4, held: true) && busy.flags(pressed: false, modified: false, at: 50.48),
          "a real second tap soon after (Shift held when it arrives) still toggles, e.g. #90 restore")
    // 遠端桌面一次送出按下與放開：第一個按下處理時鍵已放開，但前面沒有剛完成的切換，仍算單按。
    check(!busy.flags(pressed: true, modified: false, at: 60, held: false) && busy.flags(pressed: false, modified: false, at: 60.004),
          "a press processed after a quick release still counts when no toggle just happened")
    check(!busy.flags(pressed: true, modified: false, at: 60.1, held: false) && !busy.flags(pressed: false, modified: false, at: 60.101),
          "its delayed duplicate pair is ignored")
    check(!busy.flags(pressed: true, modified: false, at: 61, held: false) && busy.flags(pressed: false, modified: false, at: 61.01),
          "the duplicate window ends; a later lagging press counts again")
    // 忙碌的 Chrome 晚 0.3 秒才交來放開，事件時間是送出當下；計時改用系統記下的實際修飾鍵變化時間。
    check(abs(RightShiftTap.eventTime(timestamp: 70.42, now: 70.43, sinceLastModifierChange: 0.33) - 70.1) < 1e-9,
          "a release Chrome delivers late is timed at the real release")
    var late = RightShiftTap()
    _ = late.flags(pressed: true, modified: false, at: RightShiftTap.eventTime(timestamp: 70.001, now: 70.002, sinceLastModifierChange: 0.002))
    check(late.flags(pressed: false, modified: false, at: RightShiftTap.eventTime(timestamp: 70.42, now: 70.43, sinceLastModifierChange: 0.33)),
          "a 0.1 s tap still toggles when Chrome hands over the release 0.3 s late")
    check(RightShiftTap.eventTime(timestamp: 80, now: 80.01, sinceLastModifierChange: 3600) == 80,
          "no recent modifier change in the session falls back to the event time")
    check(RightShiftTap.eventTime(timestamp: 80, now: 80.5, sinceLastModifierChange: 0.1) == 80,
          "a modifier change after the event (e.g. Command pressed since) falls back to the event time")
    // 舊核心沒有 right_shift：右 Shift 照舊切換、左 Shift 看 left_shift，時限 0.3 秒。
    let legacy = ShiftToggleSetting(info: ["left_shift": "false"])
    check(legacy.right && !legacy.left && legacy.tapLimit == 0.3, "an old engine keeps right Shift on and left off")
    check(ShiftToggleSetting(info: ["left_shift": "true"]).left, "an old engine's left_shift still turns left Shift on")
    let leftOnly = ShiftToggleSetting(info: ["left_shift": "true", "right_shift": "false", "shift_tap_ms": "250"])
    check(leftOnly.left && !leftOnly.right && leftOnly.tapLimit == 0.25, "right_shift and shift_tap_ms from a new engine")

    // #71：背景 App 留著的安全輸入不能讓一般 App 停擺；終端機的安全鍵盤輸入仍放行（#30）。
    check(!SecureInputPolicy.bypass(secure: false, client: "com.apple.Terminal"), "no secure input means ordinary input")
    check(!SecureInputPolicy.bypass(secure: true, client: "com.anthropic.claudefordesktop"),
          "another app's secure input must not disable typing in an ordinary app")
    check(!SecureInputPolicy.bypass(secure: true, client: "com.apple.TextEdit"), "TextEdit keeps typing while a background app holds secure input")
    check(SecureInputPolicy.bypass(secure: true, client: "com.apple.Terminal"), "Terminal Secure Keyboard Entry still bypasses")
    check(SecureInputPolicy.bypass(secure: true, client: "com.googlecode.iterm2"), "iTerm2 Secure Keyboard Entry still bypasses")
    check(SecureInputPolicy.bypass(secure: true, client: nil), "unknown client stays protected")

    let screens = [NSRect(x: 0, y: 0, width: 1920, height: 1080), NSRect(x: -1920, y: 0, width: 1920, height: 1080)]
    let currentCaret = NSRect(x: 771, y: 631, width: 0, height: 16)
    let staleLine = NSRect(x: 371, y: 815, width: 1, height: 16)
    check(CaretGeometry.resolve(cursor: currentCaret, line: staleLine, screens: screens) == NSRect(x: 771, y: 615, width: 1, height: 16),
          "moved window uses both coordinates of the current caret")
    let safariLine = NSRect(x: 430, y: 720, width: 1, height: 28)
    check(CaretGeometry.resolve(cursor: NSRect(x: 0, y: 720, width: 0, height: 0), line: safariLine, screens: screens) == safariLine,
          "zero-height Safari reply must not override a valid horizontal anchor")
    check(CaretGeometry.resolve(cursor: NSRect(x: 0, y: 2138328, width: 0, height: 1), line: safariLine, screens: screens) == safariLine,
          "off-screen Safari reply must not override the line rectangle")
    // macOS 26 實測：Chrome、VS Code、Warp 的 firstRect 是約 1.6e-314 的未初始化值，attributes 的行矩形才是組字位置。
    let garbage = NSRect(x: 1.60807e-314, y: 0, width: 1.60807e-314, height: 1.60807e-314)
    let chromiumLine = NSRect(x: 350, y: 660, width: 1, height: 23)
    check(CaretGeometry.resolve(cursor: garbage, line: chromiumLine, screens: screens) == chromiumLine,
          "Chromium's uninitialized firstRect must not pin candidates to the screen origin")
    check(CaretGeometry.resolve(cursor: .zero, line: .zero, screens: screens) == nil, "missing geometry is not a screen-origin caret")
    check(CaretGeometry.valid(NSRect(x: 0, y: 0, width: 0, height: 16)), "screen-origin caret may be valid")
    check(CaretGeometry.valid(NSRect(x: -800, y: 400, width: 0, height: 16)), "left monitor supports negative coordinates")
    check(!CaretGeometry.valid(NSRect(x: CGFloat.nan, y: 400, width: 1, height: 16)), "nonfinite coordinates are rejected")

    // A dead server fails once without replaying an ambiguous commit; the next
    // request launches a new engine and succeeds with a fresh composition.
    _ = try key("a")
    stop()
    var failed = false
    do { _ = try key("Enter") } catch { failed = true }
    check(failed, "disconnection must be reported to the controller")
    check(try key("a").text == "ㄇ", "restart after disconnection")
    _ = try key("Escape")
    // #119：引擎在欄位還連著時結束（更新交接、套用設定重啟）要回報 disconnected，外殼才知道這個鍵沒被處理、
    // 沒有組字時可以開新連線重送；不能跟連不上（unavailable）混在一起，否則鍵會原樣交給 App（句首 5＋空白變成「5 」）。
    stop()
    var restarted = false
    do { _ = try session.handle(Request(type: "key", key: "5")) } catch EngineError.disconnected { restarted = true }
    check(restarted, "a connection the engine closed is reported as disconnected")
    check(!session.isOpen, "the closed engine's session is forgotten")
    check(try key("a").text == "ㄇ", "the resent key reaches a new engine in a fresh session")
    _ = try key("Escape")
    check(!session.shiftToggles(left: true) && !session.shiftToggles(left: false), "neither Shift key toggles by default on macOS")
    check(session.shiftTapLimit == 0.3, "the engine's 300 ms tap limit")
    session.reset()
    stop()

    // #117：toggle-key 決定哪一側切換；改設定後引擎重啟，下一次查詢就照新設定。
    for (value, left, right) in [("right", false, true), ("left", true, false), ("none", false, false), ("both", true, true)] {
        try Data(#"{"schema":1,"selection":"statistical","punctuation":false,"english_autocomplete":true,"toggle_key":"\#(value)"}"#.utf8)
            .write(to: directory.appendingPathComponent("settings.json"))
        _ = try key(" ")
        check(session.shiftToggles(left: true) == left && session.shiftToggles(left: false) == right,
              "toggle-key \(value) chooses which Shift toggles")
        session.reset()
        stop()
    }
    // Profile handoff: the first event is a modifier, with no ordinary key to
    // reopen the old session. Capability probes must not swallow its EOF.
    try Data(#"{"schema":1,"selection":"statistical","toggle_key":"none"}"#.utf8)
        .write(to: directory.appendingPathComponent("settings.json"))
    _ = try key(" ")
    check(!session.shiftToggles(left: true), "old profile disables Shift")
    stop()
    try Data(#"{"schema":1,"selection":"statistical","toggle_key":"both"}"#.utf8)
        .write(to: directory.appendingPathComponent("settings.json"))
    try start()
    let modifierReady = EngineConnection(path: socketPath)
    let modifierDeadline = Date(timeIntervalSinceNow: 5)
    while !modifierReady.ready() && Date() < modifierDeadline { Thread.sleep(forTimeInterval: 0.02) }
    modifierReady.disconnect()
    let freshModifierSession = EngineSession(connection: EngineConnection(path: socketPath), inputAllowed: { true })
    check(freshModifierSession.shiftToggles(left: true), "new controller must refresh process-wide stale Shift settings before first modifier")
    EngineSession.update(from: ["left_shift": "false", "right_shift": "false"])
    check(session.shiftToggles(left: true), "first Shift after handoff reads new profile without a warmup key")
    check(session.isOpen, "capability probe does not consume the retired session's recovery state")
    var modifierEOF = false
    do { _ = try session.handle(Request(type: "key", key: " ")) } catch EngineError.disconnected { modifierEOF = true }
    check(modifierEOF, "normal input still receives the original session disconnection")
    _ = try key(" ")
    EngineSession.update(from: ["left_shift": "true", "right_shift": "true", "input_style_shift_space": "fullwidth", "input_style_caps_letter": "lower"])
    stop()
    try start()
    let styleDeadline = Date(timeIntervalSinceNow: 5)
    while !modifierReady.ready() && Date() < styleDeadline { Thread.sleep(forTimeInterval: 0.02) }
    modifierReady.disconnect()
    check(!session.styleShiftSpace && !session.styleCapsLower, "first style key reads replacement capabilities without ordinary input")
    session.reset()
    stop()

    // #192：「用快捷鍵切換簡體字」預設關（設定檔沒有這一項）；打開後引擎重啟，下一次 open 就回報，⌃⇧F11 才攔。
    check(!session.simplifiedHotkey, "simplified_hotkey defaults off")
    try Data(#"{"schema":1,"selection":"statistical","punctuation":false,"simplified_hotkey":true}"#.utf8)
        .write(to: directory.appendingPathComponent("settings.json"))
    _ = try key(" ")
    check(session.simplifiedHotkey, "open reply carries simplified_hotkey=true once the setting is on")
    session.reset()
    stop()

    try Data(#"{"schema":1,"selection":"statistical","punctuation":false,"english_autocomplete":true,"left_shift":true}"#.utf8)
        .write(to: directory.appendingPathComponent("settings.json"))
    _ = try key(" ") // reopen after the isolated engine stop above
    check(session.shiftToggles(left: true) && session.shiftToggles(left: false), "an old left_shift=true reads as both")
    session.reset()
    stop()
    check(session.shiftToggles(left: true), "a cold-starting engine keeps the last known left Shift setting")
    // #148：單按 Shift 不在主執行緒上等引擎。引擎收下連線卻不回覆（卡住、忙著）時，主執行緒等滿逾時，
    // 這段期間系統把按鍵原樣交給 App（變英文）、選單裡靈犀的項目只剩「……」。已知設定時直接用快取。
    do {
        let path = directory.appendingPathComponent("stuck.sock").path
        unlink(path)
        let listener = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        defer { Darwin.close(listener); unlink(path) }
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutableBytes(of: &address.sun_path) { $0.copyBytes(from: Array(path.utf8) + [0]) }
        let bound = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.bind(listener, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard bound == 0, Darwin.listen(listener, 4) == 0 else { fatalError("stuck peer bind") }
        // 不 accept、不回覆：連線停在 backlog，請求送得出去但永遠等不到回覆。
        let stuck = EngineSession(connection: EngineConnection(path: path), inputAllowed: { true })
        let started = ProcessInfo.processInfo.systemUptime
        let toggles = stuck.shiftToggles(left: true)
        let waited = ProcessInfo.processInfo.systemUptime - started
        check(waited < 0.1, "a Shift tap must not wait for an unresponsive engine (waited \(waited) s)")
        check(toggles, "the cached left Shift setting still applies while the engine does not answer")
    }
    _ = try key(" ")
    _ = try session.handle(Request(type: "mode", on: true))
    for boundary in ["Enter", " ", "Left", "Delete", "."] {
        for letter in ["h", "e", "l"] { snapshot = try key(letter) }
        check(snapshot.englishPending == true && snapshot.candidates != nil, "English prefix candidates")
        snapshot = try key(boundary)
        // #235：Enter 只送出單字、不交給 App；其他邊界鍵送出後交給 App。
        check(snapshot.handled == (boundary == "Enter") && snapshot.commit == "hel" && snapshot.englishPending != true,
              "English boundary commits raw word (host action except Enter): \(boundary)")
        _ = try key(" ")
    }
    for letter in ["h", "e", "l"] { snapshot = try key(letter) }
    snapshot = try key("Tab")
    let englishChosen = snapshot.text
    check(snapshot.handled && englishChosen != "hel", "explicit Tab selects completion")
    snapshot = try key("Enter")
    check(snapshot.handled && snapshot.commit == englishChosen, "Enter commits the selected word only (#235)")
    session.reset()
    stop()
    try vocabKeyChecks(directory: directory, session: session, key: key)
    stop()

    check(KeyMapping.key(code: 49, characters: " ", modifiers: [.shift], styleShiftSpace: true) == "ShiftSpace", "style Shift Space reaches core")
    check(KeyMapping.key(code: 49, characters: " ", modifiers: [.shift]) == " ", "old engine receives plain space")
    check(KeyMapping.key(code: 49, characters: " ", modifiers: [.control, .shift], styleShiftSpace: true) == nil, "Ctrl Shift Space remains host shortcut")
    check(KeyMapping.key(code: 0, characters: "A", modifiers: [.capsLock], capsLower: true) == "a", "style Caps lower")
    check(KeyMapping.key(code: 0, characters: "A", modifiers: [.capsLock, .shift], capsLower: true) == "A", "style Caps Shift preserves uppercase")
    check(KeyMapping.text(for: "ShiftSpace") == " ", "cold-start fallback inserts space")
    check(KeyMapping.key(code: 36, characters: "\r", modifiers: []) == "Enter", "Return mapping")
    check(KeyMapping.key(code: 123, characters: nil, modifiers: []) == "Left", "arrow mapping")
    check(KeyMapping.key(code: 43, characters: "<", modifiers: [.shift]) == "<", "shift punctuation")
    // Caps Lock 是英文大寫模式（#84，同 Windows）：字母大寫，Shift＋字母照 macOS 系統也是大寫；非字母照原字元。
    check(KeyMapping.key(code: 0, characters: "A", modifiers: [.capsLock]) == "A", "Caps Lock keeps letters upper-case")
    check(KeyMapping.key(code: 0, characters: "a", modifiers: [.capsLock]) == "A", "Caps Lock upper-cases letters")
    check(KeyMapping.key(code: 0, characters: "A", modifiers: [.capsLock, .shift]) == "A", "Caps Lock + Shift is upper-case")
    check(KeyMapping.key(code: 0, characters: "a", modifiers: [.capsLock, .shift]) == "A", "Caps Lock + Shift never lower-cases")
    check(KeyMapping.key(code: 0, characters: "a", modifiers: []) == "a", "letters stay lower-case without Caps Lock")
    check(KeyMapping.key(code: 43, characters: "<", modifiers: [.capsLock, .shift]) == "<", "Caps Lock keeps punctuation")
    check(KeyMapping.key(code: 18, characters: "1", modifiers: [.capsLock]) == "1", "Caps Lock keeps digits")
    for flags: NSEvent.ModifierFlags in [.command, .control, .option, [.capsLock, .command]] {
        check(KeyMapping.key(code: 0, characters: "a", modifiers: flags) == nil, "shortcut passes through")
    }
    // #168：Control＋標點的鍵名；Command／Option、少了或多了 Shift 都不是。
    for (code, flags, want) in [(UInt16(43), NSEvent.ModifierFlags.control, "Ctrl+,"), (47, .control, "Ctrl+."), (39, .control, "Ctrl+'"),
                                (41, .control, "Ctrl+;"), (44, [.control, .shift], "Ctrl+?"), (18, [.control, .shift], "Ctrl+!"),
                                (43, [.control, .capsLock], "Ctrl+,"),
                                (41, [.control, .shift], "Ctrl+:"), (25, [.control, .shift], "Ctrl+("), (29, [.control, .shift], "Ctrl+)")] {
        check(KeyMapping.ctrlPunct(code: code, modifiers: flags) == want, "ctrl punct \(want)")
        check(KeyMapping.ctrlPunctText[want] != nil, "ctrl punct text \(want)")
    }
    for (code, flags) in [(UInt16(43), NSEvent.ModifierFlags.command), (43, []), (43, [.control, .option]), (43, [.control, .command]),
                          (43, [.control, .shift]), (44, .control), (18, .control), (33, .control), (0, .control),
                          (25, .control), (29, .control), (29, [.control, .shift, .option])] {
        check(KeyMapping.ctrlPunct(code: code, modifiers: flags) == nil, "not ctrl punct: \(code) \(flags.rawValue)")
    }
    // #231：Ctrl+: ( ) 要引擎的 ctrl_punctuation_keys 列了才送；最早的六個鍵舊引擎也認得。
    check(KeyMapping.ctrlPunctKnown("Ctrl+,", engineKeys: "") && KeyMapping.ctrlPunctKnown("Ctrl+!", engineKeys: ""), "legacy ctrl punct keys")
    check(!KeyMapping.ctrlPunctKnown("Ctrl+(", engineKeys: "") && !KeyMapping.ctrlPunctKnown("Ctrl+:", engineKeys: ""), "old engine lacks Ctrl+( :")
    check(KeyMapping.ctrlPunctKnown("Ctrl+)", engineKeys: ",.';?!:()"), "new engine knows Ctrl+)")
    check(!KeyMapping.ctrlPunctKnown("Ctrl+[", engineKeys: ",.';?!:()"), "Ctrl+[ is not ctrl punct")
    // #192：⌃⇧F11（F 鍵事件帶 .function，Caps Lock 不影響）；少了 Shift／Control、多了 Command／Option、別的 F 鍵（F2 是 120）、字母 F 都不是。
    for flags: NSEvent.ModifierFlags in [[.control, .shift], [.control, .shift, .function], [.control, .shift, .function, .capsLock]] {
        check(KeyMapping.simplifiedHotkey(code: KeyMapping.f11Code, modifiers: flags), "simplified hotkey \(flags.rawValue)")
    }
    let notHotkey: [(UInt16, NSEvent.ModifierFlags)] = [(KeyMapping.f11Code, [.control, .function]), (KeyMapping.f11Code, [.shift, .function]),
        (KeyMapping.f11Code, [.function]), (KeyMapping.f11Code, [.control, .shift, .command, .function]),
        (KeyMapping.f11Code, [.control, .shift, .option, .function]), (120, [.control, .shift, .function]), (3, [.control, .shift])]
    for (code, flags) in notHotkey {
        check(!KeyMapping.simplifiedHotkey(code: code, modifiers: flags), "not simplified hotkey: \(code) \(flags.rawValue)")
    }
    check(KeyMapping.key(code: 117, characters: "\u{F728}", modifiers: []) == "Delete", "forward delete maps to core grapheme editing")
    check(KeyMapping.key(code: 0, characters: "😀", modifiers: []) == nil, "non-ASCII passes through")
    // 數字鍵盤不是注音鍵（#75）：與主鍵盤的 1（keyCode 18）分開；補送給 App 的是字元本身。
    for (code, characters) in [(UInt16(83), "1"), (82, "0"), (92, "9"), (65, "."), (67, "*"), (69, "+"), (75, "/"), (78, "-"), (81, "=")] {
        check(KeyMapping.key(code: code, characters: characters, modifiers: [.numericPad]) == "KP" + characters, "keypad \(characters)")
        check(KeyMapping.text(for: "KP" + characters) == characters, "keypad \(characters) hands back itself")
    }
    check(KeyMapping.key(code: 18, characters: "1", modifiers: []) == "1", "main-row 1 stays a layout key")
    check(KeyMapping.key(code: 76, characters: "\u{3}", modifiers: [.numericPad]) == "Enter", "keypad Enter is Enter")
    // 引擎沒處理而要補送給 App 的文字：具名鍵沒有文字，絕不能把 "Backspace"、"Enter" 當字送出（issue #46）。
    // Home／End（筆電是 Fn＋←／→，characters 為 NSHomeFunctionKey／NSEndFunctionKey）交給核心，組字中移到頭尾（#106）。
    check(KeyMapping.key(code: 115, characters: "\u{F729}", modifiers: [.function]) == "Home", "Home mapping")
    check(KeyMapping.key(code: 119, characters: "\u{F72B}", modifiers: [.function, .shift]) == "End", "Shift+End mapping")
    check(KeyMapping.key(code: 115, characters: "\u{F729}", modifiers: [.command]) == nil, "Command+Home passes through")
    check(KeyMapping.key(code: 116, characters: "\u{F72C}", modifiers: [.function]) == "PageUp", "PageUp mapping")
    check(KeyMapping.key(code: 121, characters: "\u{F72D}", modifiers: [.function]) == "PageDown", "PageDown mapping")
    check(KeyMapping.key(code: 48, characters: "\t", modifiers: []) == "Tab", "Tab mapping")
    check(KeyMapping.key(code: 48, characters: "\u{19}", modifiers: [.shift]) == "BackTab", "Shift+Tab is BackTab")
    for named in ["Enter", "Backspace", "Delete", "Tab", "BackTab", "Escape", "Left", "Right", "Up", "Down", "PageUp", "PageDown", "Home", "End"] {
        check(KeyMapping.text(for: named) == nil, "\(named) has no text to hand back")
    }
    for (code, characters) in [(UInt16(51), "\u{7F}"), (36, "\r"), (76, "\u{3}")] {
        let key = KeyMapping.key(code: code, characters: characters, modifiers: [])
        check(key != nil && KeyMapping.text(for: key!) == nil, "mapped named key \(code) has no text")
    }
    for printable in ["a", "A", "<", "&", "1", " "] {
        check(KeyMapping.text(for: printable) == printable, "printable \(printable) is handed back as itself")
    }

    // Deliberately broken peers check bounded frames, fragmented reads, and a
    // single transaction deadline. Never connect to the user's daily engine.
    func fakePeer(_ bytes: [UInt8], delay: useconds_t = 0, timeout: TimeInterval = 0.2) throws -> Bool {
        let path = directory.appendingPathComponent("fake.sock").path
        unlink(path)
        let listener = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        defer { Darwin.close(listener); unlink(path) }
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutableBytes(of: &address.sun_path) { $0.copyBytes(from: Array(path.utf8) + [0]) }
        let bound = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.bind(listener, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard bound == 0, Darwin.listen(listener, 1) == 0 else { fatalError("fake peer bind") }
        let finished = DispatchSemaphore(value: 0)
        DispatchQueue.global().async {
            let peer = Darwin.accept(listener, nil, nil)
            defer { Darwin.close(peer); finished.signal() }
            var yes: Int32 = 1
            _ = setsockopt(peer, SOL_SOCKET, SO_NOSIGPIPE, &yes, 4)
            var request = [UInt8](repeating: 0, count: 1024)
            _ = Darwin.recv(peer, &request, request.count, 0)
            for var byte in bytes {
                if delay > 0 { usleep(delay) }
                if Darwin.send(peer, &byte, 1, 0) <= 0 { break }
            }
        }
        let connection = EngineConnection(path: path, timeout: timeout)
        var ok = false
        do { ok = try connection.call(Envelope(op: "ping")).ok } catch { }
        connection.disconnect()
        check(finished.wait(timeout: .now() + 2) == .success, "fake peer terminated")
        return ok
    }
    let payload = Array("{\"ok\":true}".utf8)
    check(try fakePeer([UInt8(payload.count), 0, 0, 0] + payload, delay: 1000), "fragmented response")
    check(try !fakePeer([0, 0, 16, 1]), "oversized response rejected")
    check(try !fakePeer([10, 0, 0, 0, 123]), "truncated response rejected")
    let before = ProcessInfo.processInfo.systemUptime
    check(try !fakePeer([UInt8(payload.count), 0, 0, 0] + payload, delay: 50_000, timeout: 0.1), "slow peer times out")
    check(ProcessInfo.processInfo.systemUptime - before < 1, "timeout is bounded")
    check(ModelStatusMonitor.label("fallback", runtime: "running", fallback: "統計引擎") == "已退回統計引擎", "model fallback is visible")
    check(ModelStatusMonitor.label("fallback", runtime: "running", fallback: "原文提交") == "已退回原文提交", "punctuation fallback is visible")
    check(ModelStatusMonitor.label("ready", runtime: "running", fallback: "統計引擎") == "已就緒", "ready is explicit")
    check(ModelStatusMonitor.label(nil, runtime: "running", fallback: "統計引擎") == "狀態未知", "old engine is not assumed ready")
    check(ModelStatusMonitor.label("ready", runtime: "not_running", fallback: "統計引擎") == "引擎未執行", "stopped engine overrides cached state")
    markedTextChecks()
    vocabMappingChecks()
    noticeChecks()
    print("Passed \(checks) macOS integration checks")
}

// 組字區的底線樣式與「內容相同不重送」（#108）。
func markedTextChecks() {
    func segments(_ json: String) -> [Snapshot.Segment] {
        try! JSONDecoder().decode([Snapshot.Segment].self, from: Data(json.utf8))
    }
    func underline(_ text: NSAttributedString, _ index: Int) -> Int? {
        text.attribute(.underlineStyle, at: index, effectiveRange: nil) as? Int
    }
    let styled = MarkedText.attributed(segments(#"[{"text":"我今","style":"locked"},{"text":"天","style":"corrected"},{"text":"ㄒㄧ","style":"input"}]"#))
    check(styled.string == "我今天ㄒㄧ", "marked text concatenates segments")
    check(underline(styled, 0) == NSUnderlineStyle.double.rawValue && underline(styled, 1) == NSUnderlineStyle.double.rawValue,
          "locked segment keeps the double underline")
    check(underline(styled, 2) == NSUnderlineStyle.single.rawValue | NSUnderlineStyle.patternDot.rawValue, "corrected segment keeps the dotted underline")
    check(underline(styled, 3) == NSUnderlineStyle.single.rawValue, "input segment keeps the single underline")
    var keys = Set<NSAttributedString.Key>()
    styled.enumerateAttributes(in: NSRange(location: 0, length: styled.length)) { attrs, _, _ in keys.formUnion(attrs.keys) }
    check(keys == [.underlineStyle], "font and colour stay with the app")
    // 標示範圍（#123）：粗底線（App 當成目前的文節畫得比較顯眼）；顏色仍交給 App；樣式改變要重送。
    let withMark = MarkedText.attributed(segments(#"[{"text":"我","style":"normal"},{"text":"世傑","style":"marked"}]"#))
    check(underline(withMark, 0) == NSUnderlineStyle.single.rawValue, "unmarked text keeps the single underline")
    check(underline(withMark, 1) == NSUnderlineStyle.thick.rawValue && underline(withMark, 2) == NSUnderlineStyle.thick.rawValue,
          "marked range has the thick underline")
    var markKeys = Set<NSAttributedString.Key>()
    withMark.enumerateAttributes(in: NSRange(location: 0, length: withMark.length)) { attrs, _, _ in markKeys.formUnion(attrs.keys) }
    check(markKeys == [.underlineStyle], "the marked range leaves font and colour to the app too")
    let unmarked = MarkedText.attributed(segments(#"[{"text":"我","style":"normal"},{"text":"世傑","style":"locked"}]"#))
    check(!MarkedText.unchanged(MarkedText.Shown(text: withMark, cursor: 1), last: MarkedText.Shown(text: unmarked, cursor: 1),
                                clientMarkedRange: { NSRange(location: 0, length: 3) }), "marking the same text is re-sent")
    check(MarkedText.unchanged(MarkedText.Shown(text: withMark, cursor: 1),
                               last: MarkedText.Shown(text: MarkedText.attributed(segments(#"[{"text":"我","style":"normal"},{"text":"世傑","style":"marked"}]"#)), cursor: 1),
                               clientMarkedRange: { NSRange(location: 0, length: 3) }), "an unchanged mark is not re-sent")

    let shown = MarkedText.Shown(text: styled, cursor: 5)
    let same = MarkedText.Shown(text: MarkedText.attributed(segments(#"[{"text":"我今","style":"locked"},{"text":"天","style":"corrected"},{"text":"ㄒㄧ","style":"input"}]"#)), cursor: 5)
    var asked = 0
    let marked = { (range: NSRange) in { () -> NSRange in asked += 1; return range } }
    check(MarkedText.unchanged(same, last: shown, clientMarkedRange: marked(NSRange(location: 3, length: 5))),
          "identical marked text still shown by the app is not re-sent")
    check(!MarkedText.unchanged(same, last: nil, clientMarkedRange: marked(NSRange(location: 0, length: 5))), "first marked text is sent")
    check(!MarkedText.unchanged(MarkedText.Shown(text: styled, cursor: 4), last: shown, clientMarkedRange: marked(NSRange(location: 0, length: 5))),
          "caret move is sent")
    let restyled = MarkedText.attributed(segments(#"[{"text":"我今天","style":"locked"},{"text":"ㄒㄧ","style":"input"}]"#))
    check(!MarkedText.unchanged(MarkedText.Shown(text: restyled, cursor: 5), last: shown, clientMarkedRange: marked(NSRange(location: 0, length: 5))),
          "underline change with the same text is sent")
    check(!MarkedText.unchanged(MarkedText.Shown(text: MarkedText.attributed(segments(#"[{"text":"我今天ㄒㄧㄤ","style":"input"}]"#)), cursor: 6),
                                last: shown, clientMarkedRange: marked(NSRange(location: 0, length: 6))), "new text is sent")
    asked = 0
    _ = MarkedText.unchanged(MarkedText.Shown(text: restyled, cursor: 5), last: shown, clientMarkedRange: marked(NSRange(location: 0, length: 5)))
    check(asked == 0, "the app is only asked when the content is identical")
    check(!MarkedText.unchanged(same, last: shown, clientMarkedRange: marked(NSRange(location: NSNotFound, length: 0))),
          "re-send when the app reports no marked text")
    check(!MarkedText.unchanged(same, last: shown, clientMarkedRange: marked(NSRange(location: 0, length: 3))),
          "re-send when the app's marked range differs")
    let empty = MarkedText.Shown(text: NSAttributedString(string: ""), cursor: 0)
    check(!MarkedText.unchanged(empty, last: empty, clientMarkedRange: marked(NSRange(location: 0, length: 0))), "clearing is always sent")
}

// #123：引擎在 open 的 info 回報 vocab_keys，外殼才送新鍵名；真引擎走一次加詞與反悔鍵。0.2.0 起一律開放，
// 舊設定檔寫著 preview_0_2=false 也一樣。
func vocabKeyChecks(directory: URL, session: EngineSession, key: (String) throws -> Snapshot) throws {
    try Data(#"{"schema":1,"selection":"statistical","punctuation":false,"preview_0_2":false}"#.utf8)
        .write(to: directory.appendingPathComponent("settings.json"))
    try FileManager.default.removeItem(at: directory.appendingPathComponent("vocab.json"))
    _ = try key(" ") // 重新 open，從 info 取得 vocab_keys
    check(session.vocabKeys, "open reply carries vocab_keys=true even with the retired preview_0_2=false")
    // #168：設定檔沒有 ctrl_punctuation 時預設開；Ctrl+, 沒在組字也插進組字區，Esc 清掉。
    check(session.ctrlPunct, "open reply carries ctrl_punctuation=true by default")
    check(session.ctrlPunct(key: "Ctrl+("), "open reply carries ctrl_punctuation_keys with Ctrl+( (#231)")
    let paren = try key("Ctrl+(")
    check(paren.handled && paren.text == "（", "Ctrl+( types a full-width parenthesis: \(paren.text)")
    _ = try key("Escape")
    let comma = try key("Ctrl+,")
    check(comma.handled && comma.text == "，", "Ctrl+, types a full-width comma: \(comma.text)")
    check(try key("Escape").text.isEmpty, "Escape clears the comma")
    // #169：open 的 info 回報能即時切換「輸出簡體字」；切換後組字照樣是繁體、送出是簡體，快照帶狀態與提示，設定檔跟著寫。
    check(EngineSession.simplifiedLive && !EngineSession.outputSimplified, "open reply carries output_simplified_live and the traditional state")
    var scenery = try key("z")
    for k in ["/", " ", "r", "u", "/", "3"] { scenery = try key(k) } // ㄈㄥ ㄐㄧㄥˇ
    check(scenery.text == "風景" && scenery.outputSimplified != true, "traditional composition before switching: \(scenery.text)")
    let switched = try session.handle(Request(type: "output_simplified", on: true))
    check(switched.handled && switched.outputSimplified == true && switched.text == "風景" && switched.visibleNotices == ["已切換為輸出簡體字"],
          "switch keeps the composition and reports the new state: \(switched.visibleNotices)")
    EngineSession.note(switched)
    check(EngineSession.outputSimplified, "the menu check follows the snapshot")
    let settingsText = (try? String(contentsOf: directory.appendingPathComponent("settings.json"), encoding: .utf8)) ?? ""
    check(settingsText.contains("\"output_simplified\": true") && settingsText.contains("\"selection\": \"statistical\""),
          "the engine writes the switch into settings.json and keeps the rest: \(settingsText)")
    check(try key("Enter").commit == "风景", "text already composing is sent simplified")
    let back = try session.handle(Request(type: "output_simplified", on: false))
    EngineSession.note(back)
    check(back.outputSimplified != true && !EngineSession.outputSimplified && back.visibleNotices == ["已切換為輸出繁體字"], "switch back")
    let shiftLeft = KeyMapping.key(code: 123, characters: nil, modifiers: [.shift, .function, .numericPad], vocabKeys: session.vocabKeys)
    check(shiftLeft == "ShiftLeft", "Shift+Left maps to ShiftLeft once the engine supports it")
    // 沒在組字：引擎交還，App 照常選取文字。
    check(try !key("ShiftLeft").handled, "ShiftLeft without a composition goes back to the app")
    var snapshot = try key("a")
    for k in ["8", "6", "q", "i", "6"] { snapshot = try key(k) }
    check(snapshot.text == "麻婆", "vocab preedit: \(snapshot.text)")
    snapshot = try key("ShiftLeft")
    snapshot = try key("ShiftLeft")
    check(snapshot.handled && snapshot.cursor.utf16 == 0, "ShiftLeft moves the caret while marking")
    check(snapshot.preedit?.contains { $0.style == "marked" && $0.text == "麻婆" } == true,
          "marked range comes back as the marked style: \(snapshot.preedit?.map { "\($0.text)/\($0.style)" } ?? [])")
    check(MarkedText.attributed(snapshot.preedit ?? []).attribute(.underlineStyle, at: 0, effectiveRange: nil) as? Int == NSUnderlineStyle.thick.rawValue,
          "marked range is drawn with the thick underline")
    check(snapshot.markedText == "麻婆", "marked text for the panel hint")
    check(snapshot.visibleNotices == ["Enter 加入詞庫：麻婆（ㄇㄚˊ ㄆㄛˊ）　Esc 取消"], "engine hint while marking: \(snapshot.visibleNotices)")
    snapshot = try key("Enter")
    check(snapshot.handled && (snapshot.commit ?? "").isEmpty && snapshot.text == "麻婆", "Enter adds the word and keeps composing")
    check(snapshot.cursor.utf16 == 2, "the caret returns to where marking started")
    check(snapshot.visibleNotices == ["已加入詞庫：麻婆"], "add notice: \(snapshot.visibleNotices)")
    check(snapshot.preedit?.contains { $0.style == "marked" } != true && snapshot.markedText == nil, "marking ends after adding")
    snapshot = try key("ShiftLeft") // 加入後游標回到開始標示的位置（句尾）
    check(snapshot.preedit?.contains { $0.style == "marked" } == true, "ShiftLeft marks again")
    snapshot = try key("Escape")
    check(snapshot.text == "麻婆" && snapshot.preedit?.contains { $0.style == "marked" } != true, "Escape cancels only the mark")
    // 反悔鍵：候選窗開著時刪掉自訂詞，提示後候選重開。
    snapshot = try key("End")
    snapshot = try key("Down")
    check(snapshot.candidates != nil, "Down opens candidates")
    for _ in 0..<3 where snapshot.candidates?.items.contains(where: { $0.text == "麻婆" }) != true {
        snapshot = try key("Tab") // 換詞長
    }
    if let items = snapshot.candidates?.items, let at = items.firstIndex(where: { $0.text == "麻婆" }) {
        for _ in 0..<max(0, at - (snapshot.candidates?.highlight ?? 0)) { snapshot = try key("Down") }
    }
    check(snapshot.candidates.map { $0.items[$0.highlight].text } == "麻婆",
          "highlight the added word: \(snapshot.candidates?.items.map(\.text) ?? [])")
    snapshot = try key("ShiftBackspace")
    check(snapshot.visibleNotices.first?.hasPrefix("已從詞庫刪除：麻婆") == true, "undo key removes the custom word: \(snapshot.visibleNotices)")
    check(snapshot.text == "麻婆", "undo key keeps the composition")
    snapshot = try key("Escape")
    check(snapshot.candidates == nil && snapshot.text == "麻婆", "Escape closes the reopened candidates")
    // 候選窗沒開：Shift＋⌫ 等同 ⌫，不會把按鍵漏給 App。
    snapshot = try key("ShiftBackspace")
    check(snapshot.handled && snapshot.text.count == 1, "ShiftBackspace without candidates is Backspace: \(snapshot.text)")
    _ = try key("Escape")
    session.reset()
}

// 鍵名對應：只有按著 Shift（沒有 Command／Control／Option）而且引擎支援時才送新鍵名（#123）。
func vocabMappingChecks() {
    let cases: [(UInt16, String?, String, String)] = [
        (123, nil, "ShiftLeft", "Left"), (124, nil, "ShiftRight", "Right"),
        (51, "\u{7F}", "ShiftBackspace", "Backspace"), (117, "\u{F728}", "ShiftDelete", "Delete")]
    for (code, characters, shifted, plain) in cases {
        check(KeyMapping.key(code: code, characters: characters, modifiers: [.shift], vocabKeys: true) == shifted, "Shift+\(plain) is \(shifted)")
        check(KeyMapping.key(code: code, characters: characters, modifiers: [.shift, .function], vocabKeys: true) == shifted, "Fn flag keeps \(shifted)")
        check(KeyMapping.key(code: code, characters: characters, modifiers: [.shift], vocabKeys: false) == plain, "old engine: Shift+\(plain) stays \(plain)")
        check(KeyMapping.key(code: code, characters: characters, modifiers: [], vocabKeys: true) == plain, "\(plain) without Shift is unchanged")
        for flags: NSEvent.ModifierFlags in [[.shift, .command], [.shift, .control], [.shift, .option]] {
            check(KeyMapping.key(code: code, characters: characters, modifiers: flags, vocabKeys: true) == nil, "\(flags) + \(plain) goes to the app")
        }
        check(KeyMapping.text(for: shifted) == nil, "\(shifted) has no text to hand back")
    }
    check(KeyMapping.key(code: 115, characters: "\u{F729}", modifiers: [.shift, .function], vocabKeys: true) == "Home", "Shift+Home stays Home")
    check(KeyMapping.key(code: 125, characters: nil, modifiers: [.shift], vocabKeys: true) == "Down", "Shift+Down stays Down")
    check(KeyMapping.key(code: 48, characters: "\u{19}", modifiers: [.shift], vocabKeys: true) == "BackTab", "Shift+Tab stays BackTab")
    check(KeyMapping.key(code: 0, characters: "A", modifiers: [.shift], vocabKeys: true) == "A", "Shift+letter stays a letter")
}

// 提示（notices）解析：舊引擎缺欄位、空字串略過。
func noticeChecks() {
    func decode(_ json: String) -> Snapshot { try! JSONDecoder().decode(Snapshot.self, from: Data(json.utf8)) }
    check(decode(#"{"handled":true,"cursor":{"utf16":0},"mode":"zhuyin"}"#).visibleNotices.isEmpty, "old engines send no notices")
    check(decode(#"{"handled":true,"cursor":{"utf16":2},"mode":"zhuyin","notices":["已加入詞庫：世傑",""]}"#).visibleNotices == ["已加入詞庫：世傑"],
          "notices are decoded and blank ones dropped")
    let marking = decode(#"{"handled":true,"cursor":{"utf16":1},"mode":"zhuyin","preedit":[{"text":"我和","style":"normal"},{"text":"世","style":"marked"},{"text":"傑","style":"marked"}]}"#)
    check(marking.markedText == "世傑", "marked text joins the marked segments")
    check(decode(#"{"handled":true,"cursor":{"utf16":1},"mode":"zhuyin","preedit":[{"text":"我","style":"locked"}]}"#).markedText == nil, "no mark, no hint")
}

do { try run() } catch { fatalError("Integration test failed: \(error)") }
