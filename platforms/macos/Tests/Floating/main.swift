import Foundation

// 浮動組字的判斷（#84）與診斷計數檔（#266）。時間由測試給，不碰 IMK、不讀使用者資料。
var checks = 0
func check(_ condition: Bool, _ message: String) {
    checks += 1
    if !condition { fatalError(message) }
}

let line = CGRect(x: 100, y: 500, width: 1, height: 20)

// 一般欄位：組字在 App 裡撐到下一個按鍵；使用者在按鍵後一段時間才點到別處，照常送出。
do {
    var f = FloatingComposition()
    f.compositionStarts(line: nil)
    check(!f.floating, "ordinary clients compose inline")
    f.markedTextShown(at: 10)
    f.keyArrives(composing: true)
    f.markedTextShown(at: 10.2)
    check(f.appEndsComposition(at: 11.0, composing: true, shown: "ㄕ", location: 0) == .commit, "a click long after the last key commits")
    check(!f.floating && f.strikes == 0 && f.leftover == nil, "an ordinary end is not a strike")
    check(f.appEndsComposition(at: 11.01, composing: false, shown: nil, location: nil) == .commit, "nothing composing")
}

// Chrome 在 input 事件把輸入框 blur／focus：setMarkedText 後 10 ms 就結束組字。
do {
    var f = FloatingComposition()
    f.compositionStarts(line: nil)
    f.markedTextShown(at: 1)
    check(f.appEndsComposition(at: 1.01, composing: true, shown: "ㄕ", location: 3) == .rescue, "first early end rescues the composition")
    check(f.floating && !f.sticky && f.strikes == 1, "the rest of this composition floats without marked text; not sticky yet")
    check(f.leftover == .init(text: "ㄕ", location: 3), "the text the app kept is remembered with its position")
    check(f.appEndsComposition(at: 1.02, composing: true, shown: nil, location: nil) == .ignore, "duplicate end right after the rescue is ignored")
    f.keyArrives(composing: true)
    check(f.strikes == 1, "a floating key is no evidence the field works")
    check(f.takeLeftover() == .init(text: "ㄕ", location: 3) && f.takeLeftover() == nil, "the leftover is taken once")
    f.textInserted(at: 3)
    check(f.appEndsComposition(at: 3.01, composing: true, shown: nil, location: nil) == .ignore, "the page reacting to inserted text is ignored")
    f.compositionEnded()
    check(!f.floating, "floating ends with the composition")
    // 下一段組字一開始仍在 App 裡試；又被結束 → 這個欄位改成浮動
    f.compositionStarts(line: nil)
    check(!f.floating, "a single strike does not make the field sticky")
    f.keyArrives(composing: false)
    f.markedTextShown(at: 5)
    check(f.appEndsComposition(at: 5.02, composing: true, shown: "ㄕ", location: 0) == .rescue, "second early end rescues")
    check(f.sticky && f.strikes == 2, "two consecutive early ends make the field sticky")
    f.compositionEnded()
    f.compositionStarts(line: line)
    check(f.floating, "a sticky field starts floating")
    check(f.needsField, "a sticky field asks for the caret line")
    f.compositionEnded()
    f.compositionStarts(line: line.offsetBy(dx: 200, dy: 2))
    check(f.floating, "same line keeps floating")
    // 浮動中，按鍵後很久才結束組字（點到別處）：照常送出
    f.textInserted(at: 6)
    check(f.appEndsComposition(at: 9, composing: true, shown: nil, location: nil) == .commit, "a click while floating commits")
    f.compositionEnded()
    // 游標換到別的行（例如回到主輸入框）：回到一般組字重新試，同一個欄位再被結束一次就又固定
    f.compositionStarts(line: line.offsetBy(dx: 0, dy: -400))
    check(!f.floating && !f.sticky && f.strikes == FloatingComposition.strikesToStick - 1, "another field starts inline again")
    f.markedTextShown(at: 20)
    _ = f.appEndsComposition(at: 20.01, composing: true, shown: "ㄕ", location: nil)
    check(f.sticky, "one more early end makes it sticky again")
    f.compositionEnded()
    f.reset()
    check(!f.floating && !f.sticky && f.strikes == 0 && f.leftover == nil, "reset forgets everything")
}

// Chrome 沒有組字時給不出位置：以游標是否接在上次送出的文字後面判斷是不是同一個輸入框。
do {
    var f = FloatingComposition()
    for t in [1.0, 2.0] {
        f.compositionStarts(line: nil)
        f.markedTextShown(at: t)
        _ = f.appEndsComposition(at: t + 0.01, composing: true, shown: "ㄕ", location: nil)
        f.compositionEnded()
    }
    f.caretAfterInsert(4)
    f.compositionStarts(line: nil, caret: 4)
    check(f.floating && f.sticky, "caret right after the last commit: same field")
    f.caretAfterInsert(6)
    f.compositionEnded()
    f.compositionStarts(line: nil, caret: 0)
    check(!f.floating && !f.sticky, "caret elsewhere (main chat input): try inline again")
    f.compositionEnded()
    // 查不到游標位置時照舊
    var g = FloatingComposition()
    for t in [1.0, 2.0] {
        g.compositionStarts(line: nil)
        g.markedTextShown(at: t)
        _ = g.appEndsComposition(at: t + 0.01, composing: true, shown: "ㄕ", location: nil)
        g.compositionEnded()
    }
    g.caretAfterInsert(nil)
    g.compositionStarts(line: nil, caret: 9)
    check(g.floating && g.sticky, "unknown expected caret keeps the field sticky")
    // 欄位的行在被結束的那段組字開頭就記下（Chrome 這時還給得出位置），之後換到別的行就重試
    var h = FloatingComposition()
    for t in [1.0, 2.0] {
        h.compositionStarts(line: nil)
        h.markedTextShown(at: t)
        _ = h.appEndsComposition(at: t + 0.01, composing: true, shown: "ㄕ", location: nil, line: line)
        h.compositionEnded()
    }
    h.compositionStarts(line: nil)
    check(h.floating, "Chrome gives no line in the sticky field itself")
    h.compositionEnded()
    h.compositionStarts(line: line.offsetBy(dx: 0, dy: 40), caret: 4)
    check(!h.floating && !h.sticky, "a line from another input (main chat box) drops sticky even when the caret looks the same")
}

// 中間有一段組字撐到下一個按鍵：不算連續。
do {
    var f = FloatingComposition()
    f.compositionStarts(line: nil)
    f.markedTextShown(at: 1)
    _ = f.appEndsComposition(at: 1.01, composing: true, shown: "ㄕ", location: nil)
    f.compositionEnded()
    f.compositionStarts(line: nil)
    f.markedTextShown(at: 2)
    f.keyArrives(composing: true)
    check(f.strikes == 0, "a composition that survives to the next key resets the strikes")
    f.compositionEnded()
    f.compositionStarts(line: nil)
    f.markedTextShown(at: 3)
    _ = f.appEndsComposition(at: 3.01, composing: true, shown: "ㄕ", location: nil)
    check(!f.sticky && f.strikes == 1, "non-consecutive early ends do not make the field sticky")
}

// 使用者按鍵後超過門檻才點到別處：不算。空字串不是殘字。
do {
    var f = FloatingComposition()
    f.compositionStarts(line: nil)
    f.markedTextShown(at: 1)
    check(f.appEndsComposition(at: 1 + FloatingComposition.earlyEnd + 0.05, composing: true, shown: "ㄕ", location: nil) == .commit, "slow end commits")
    f.compositionStarts(line: nil)
    f.markedTextShown(at: 2)
    _ = f.appEndsComposition(at: 2.01, composing: true, shown: "", location: nil)
    check(f.leftover == nil, "empty residue is ignored")
}

// 查不到游標行時 sticky 照舊
do {
    var f = FloatingComposition()
    for t in [1.0, 2.0] {
        f.compositionStarts(line: nil)
        f.markedTextShown(at: t)
        _ = f.appEndsComposition(at: t + 0.01, composing: true, shown: "ㄕ", location: nil)
        f.compositionEnded()
    }
    f.compositionStarts(line: nil)
    check(f.floating, "unknown caret keeps the sticky field floating")
    f.compositionEnded()
    f.compositionStarts(line: line)
    check(f.floating, "the first known line is remembered")
    f.compositionEnded()
    f.compositionStarts(line: nil)
    check(f.floating, "unknown later still floats")
}

// 殘字核對：游標要緊接在殘字後面；讀得到文字就比文字，讀不到（Chrome）就比 App 回報的位置。
do {
    let leftover = FloatingComposition.Leftover(text: "ㄕ", location: 3)
    let caret = NSRange(location: 4, length: 0)
    check(FloatingComposition.leftoverRange(leftover, selection: caret) { _ in nil } == NSRange(location: 3, length: 1), "Chrome: position matches")
    check(FloatingComposition.leftoverRange(leftover, selection: caret) { _ in "ㄕ" } == NSRange(location: 3, length: 1), "text matches")
    check(FloatingComposition.leftoverRange(leftover, selection: caret) { _ in "a" } == nil, "different text is left alone")
    check(FloatingComposition.leftoverRange(leftover, selection: NSRange(location: 9, length: 0)) { _ in nil } == nil, "caret moved")
    check(FloatingComposition.leftoverRange(leftover, selection: NSRange(location: 4, length: 2)) { _ in "ㄕ" } == nil, "a selection is left alone")
    check(FloatingComposition.leftoverRange(leftover, selection: NSRange(location: NSNotFound, length: 0)) { _ in "ㄕ" } == nil, "unknown caret")
    let unplaced = FloatingComposition.Leftover(text: "ㄕ", location: nil)
    check(FloatingComposition.leftoverRange(unplaced, selection: caret) { _ in "ㄕ" } == NSRange(location: 3, length: 1), "no position: text decides")
    check(FloatingComposition.leftoverRange(unplaced, selection: caret) { _ in nil } == nil, "no position and no text: leave it")
}

check(FloatingComposition.sameLine(line, line.offsetBy(dx: 300, dy: 10)), "within a line and a half")
check(!FloatingComposition.sameLine(line, line.offsetBy(dx: 0, dy: 40)), "two lines down is another field")

// 診斷計數檔（#266）
do {
    let directory = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("0c-stats-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: directory) }
    let path = directory.appendingPathComponent(ShellStats.fileName)
    var clock = Date(timeIntervalSince1970: 1_800_000_000)
    let stats = ShellStats(path: path, now: { clock }, writeDelay: 60)
    stats.noteActivate()
    stats.noteKey()
    stats.noteKey()
    check(!FileManager.default.fileExists(atPath: path.path), "ordinary counts are coalesced, not written per key")
    clock.addTimeInterval(30)
    stats.noteSleep()
    check(FileManager.default.fileExists(atPath: path.path), "sleep writes right away")
    clock.addTimeInterval(3600)
    stats.noteWake()
    stats.noteActivate()
    stats.noteDeactivate()
    for i in 0..<(ShellStats.maxApps + 5) { stats.noteFloating(bundle: "app.\(i)", field: false) }
    stats.noteFloating(bundle: "app.0", field: true)
    stats.noteFloating(bundle: nil, field: false)
    let object = try JSONSerialization.jsonObject(with: Data(contentsOf: path)) as! [String: Any]
    check(object["schema"] as? Int == 1, "schema")
    check(object["activations"] as? Int == 2 && object["deactivations"] as? Int == 1 && object["keys"] as? Int == 2, "totals")
    check(object["activations_since_wake"] as? Int == 1 && object["keys_since_wake"] as? Int == 0, "since-wake counters restart at wake")
    check(object["last_wake_at"] as? String == "2027-01-15T09:00:30Z", "wake time \(object["last_wake_at"] ?? "")")
    check(object["last_key_at"] as? String == "2027-01-15T08:00:00Z", "last key time")
    let floating = object["floating"] as! [String: Any]
    let apps = floating["apps"] as! [String: Int]
    check(floating["rescues"] as? Int == ShellStats.maxApps + 6 && floating["fields"] as? Int == 1, "floating counts")
    check(apps.count == ShellStats.maxApps && apps["app.0"] == 2 && apps["unknown"] == nil, "at most \(ShellStats.maxApps) apps are kept")
}

print("floating checks passed: \(checks)")
