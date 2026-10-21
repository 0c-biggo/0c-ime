import AppKit

// Run in a logged-in graphical macOS session; never activate an input source.
let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let panel = CandidatePanel()
defer { panel.orderOut(nil) }
func descendants<T: NSView>(_ root: NSView, of type: T.Type) -> [T] {
    (root as? T).map { [$0] } ?? root.subviews.flatMap { descendants($0, of: type) }
}
let snapshot = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":2},"mode":"zhuyin","preedit":[{"text":"麻婆","style":"normal"}],"candidates":{"items":[{"id":17,"text":"麻婆"},{"id":23,"text":"媽婆"}],"page":0,"pages":3,"highlight":1,"title":"修改「麻婆」 · Tab 切換詞長"},"suggestion":"豆腐"}"#.utf8))
let bounds = NSScreen.main?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
if NSScreen.screens.isEmpty { print("No display available; checking panel layout with fallback screen bounds") }
panel.show(snapshot, at: NSRect(x: bounds.maxX - 1, y: bounds.minY + 1, width: 1, height: 20))
panel.contentView!.layoutSubtreeIfNeeded()
precondition(panel.frame.width > 100 && panel.frame.height > 70, "panel must fit its rows")
precondition(bounds.contains(panel.frame), "panel must remain on screen at bottom-right edge")
precondition(!panel.canBecomeKey && !panel.canBecomeMain, "candidate window must not steal focus")
precondition(descendants(panel.contentView!, of: NSTextField.self).contains { $0.stringValue == "修改「麻婆」 · Tab 切換詞長" }, "ordinary candidate header must show the actual replacement and shortcut")
let buttons = descendants(panel.contentView!, of: NSButton.self)
precondition(buttons.count == 2 && buttons.allSatisfy { $0.frame.height > 0 }, "candidate rows must be visible")
var selected: Int?
panel.choose = { selected = $0 }
buttons[1].performClick(nil)
precondition(selected == 23, "click must send the engine ID, not the row index")
// 舊引擎沒有 key：依位置畫 1、2（#112）。Tab 補全提示跟候選同字級（#118）。
precondition(buttons[0].title == "1  麻婆" && buttons[1].title == "2  媽婆", "legacy payload numbers rows by position")
let suggestionField = descendants(panel.contentView!, of: NSTextField.self).first { $0.accessibilityIdentifier() == "suggestion" }
precondition(suggestionField?.stringValue == "⇥ 豆腐", "suggestion line shows the completion")
let suggestionFont = suggestionField?.attributedStringValue.attribute(.font, at: 2, effectiveRange: nil) as? NSFont
precondition(suggestionFont?.pointSize == 18, "suggestion uses the candidate font size, not the 13pt label default")
if CommandLine.arguments.count > 1, let view = panel.contentView, let bitmap = view.bitmapImageRepForCachingDisplay(in: view.bounds) {
    view.cacheDisplay(in: view.bounds, to: bitmap)
    if let png = bitmap.representation(using: .png, properties: [:]) {
        try png.write(to: URL(fileURLWithPath: CommandLine.arguments[1]).appendingPathComponent("suggestion.png"))
    }
}
// #112：引擎給的選字鍵照畫（1234qweras 的第 5 項是 q）；英文模式的補字候選不畫鍵標。
let keyed = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":1},"mode":"zhuyin","candidates":{"items":[{"id":1,"text":"是","key":"1"},{"id":2,"text":"事","key":"2"},{"id":3,"text":"市","key":"3"},{"id":4,"text":"試","key":"4"},{"id":5,"text":"式","key":"q"},{"id":6,"text":"世","key":"w"},{"id":7,"text":"室","key":"e"},{"id":8,"text":"士","key":"r"},{"id":9,"text":"視","key":"a"},{"id":10,"text":"示","key":"s"}],"page":0,"pages":2,"highlight":0}}"#.utf8))
panel.show(keyed, at: NSRect(x: bounds.minX + 40, y: bounds.minY + 40, width: 1, height: 20))
panel.contentView!.layoutSubtreeIfNeeded()
let keyedRows = descendants(panel.contentView!, of: NSButton.self)
precondition(keyedRows.map { String($0.title.prefix(1)) } == ["1", "2", "3", "4", "q", "w", "e", "r", "a", "s"], "rows show the engine's selection keys")
if CommandLine.arguments.count > 1, let view = panel.contentView, let bitmap = view.bitmapImageRepForCachingDisplay(in: view.bounds) {
    view.cacheDisplay(in: view.bounds, to: bitmap)
    if let png = bitmap.representation(using: .png, properties: [:]) {
        try png.write(to: URL(fileURLWithPath: CommandLine.arguments[1]).appendingPathComponent("selection-keys.png"))
    }
}
let tenth = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":1},"mode":"zhuyin","candidates":{"items":[{"id":1,"text":"一"},{"id":2,"text":"二"},{"id":3,"text":"三"},{"id":4,"text":"四"},{"id":5,"text":"五"},{"id":6,"text":"六"},{"id":7,"text":"七"},{"id":8,"text":"八"},{"id":9,"text":"九"},{"id":10,"text":"十"}],"page":0,"pages":1,"highlight":0}}"#.utf8))
precondition(tenth.candidateKey(9) == "0", "legacy tenth row is 0, the key that selects it")
let completion = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":3},"mode":"english","preedit":[{"text":"hel","style":"normal"}],"candidates":{"items":[{"id":0,"text":"hello"},{"id":1,"text":"help"}],"page":0,"pages":1,"highlight":0,"title":"按 Tab 補全或修正拼字 · Esc 關閉"}}"#.utf8))
panel.show(completion, at: NSRect(x: bounds.minX + 40, y: bounds.minY + 40, width: 1, height: 20))
panel.contentView!.layoutSubtreeIfNeeded()
precondition(descendants(panel.contentView!, of: NSButton.self).map(\.title) == ["hello", "help"], "English completions have no key labels")
// #193：核心說 j/k 會換字（step_keys）時頁尾才寫 J K 換字；混打按鍵、注音打到一半、英文補字、Emoji、
// 選字鍵 asdfghjkl; 等核心不送，外殼不自己從 length、kind、選字鍵猜（以前猜錯時頁尾寫了、按 j/k 沒反應）。
func footerHasJK(_ candidates: String) -> Bool {
    let json = #"{"handled":true,"cursor":{"utf16":1},"mode":"zhuyin","candidates":"# + candidates + "}"
    let snap = try! JSONDecoder().decode(Snapshot.self, from: Data(json.utf8))
    panel.show(snap, at: NSRect(x: bounds.minX + 40, y: bounds.minY + 40, width: 1, height: 20))
    panel.contentView!.layoutSubtreeIfNeeded()
    return descendants(panel.contentView!, of: NSTextField.self).contains { $0.stringValue.hasSuffix("J\u{00A0}K\u{00A0}換\u{2060}字") }
}
precondition(footerHasJK(#"{"items":[{"id":1,"text":"是","key":"1"}],"page":0,"pages":1,"highlight":0,"start":0,"length":1,"step_keys":true}"#), "ordinary candidates show the J/K hint")
precondition(footerHasJK(#"{"items":[{"id":1,"text":"是","key":"1"}],"page":0,"pages":1,"highlight":0,"start":0,"length":2,"columns":5,"step_keys":true}"#), "ordinary grid shows the J/K hint")
precondition(!footerHasJK(#"{"items":[{"id":1,"text":"是","key":"1"}],"page":0,"pages":1,"highlight":0,"start":0,"length":1}"#), "no step_keys (mixed keys, pending zhuyin, asdfghjkl;) has no J/K hint even with length > 0")
precondition(!footerHasJK(#"{"items":[{"id":1,"text":"是","key":"1"}],"page":0,"pages":1,"highlight":0,"start":0,"length":1,"step_keys":false}"#), "step_keys false has no J/K hint")
precondition(!footerHasJK(#"{"items":[{"id":1,"text":"是"}],"page":0,"pages":1,"highlight":0}"#), "legacy payload without step_keys has no J/K hint")

// Grids retain stable engine IDs and actual row/column geometry. Cache the
// rendered NSView only when an explicit output directory is supplied.
for (columns, count, kind) in [(5, 10, "emoji"), (5, 7, "ordinary"), (2, 10, "kaomoji")] {
    let values: [[String: Any]] = (0..<count).map { index in
        ["id": 101 + index * 3, "text": kind == "emoji" ? ["😀", "🇹🇼", "👨‍👩‍👧‍👦", "👍🏽", "❤️"][index % 5]
            : kind == "kaomoji" ? "(づ｡◕‿‿◕｡)づ" : "一般候選文字"]
    }
    let data = try JSONSerialization.data(withJSONObject: [
        "handled": true, "cursor": ["utf16": 0], "mode": "zhuyin",
        "candidates": ["items": values, "page": 0, "pages": 3, "highlight": 6,
            "columns": columns, "kind": kind, "title": "自選選單 · Tab 切換"]
    ])
    let grid = try JSONDecoder().decode(Snapshot.self, from: data)
    panel.show(grid, at: NSRect(x: bounds.maxX - 1, y: bounds.minY + 1, width: 1, height: 20))
    panel.contentView!.layoutSubtreeIfNeeded()
    let cells = descendants(panel.contentView!, of: NSButton.self)
    precondition(cells.count == count, "grid must preserve all visible candidates")
    let first = cells[0].convert(NSPoint.zero, to: panel.contentView)
    let second = cells[1].convert(NSPoint.zero, to: panel.contentView)
    let nextRow = cells[columns].convert(NSPoint.zero, to: panel.contentView)
    precondition(abs(first.y - second.y) < 1 && second.x > first.x, "row must have multiple columns")
    precondition(abs(first.x - nextRow.x) < 1 && abs(first.y - nextRow.y) > 20, "next row must align")
    precondition(bounds.contains(panel.frame), "grid must remain inside screen")
    for cell in cells {
        selected = nil
        cell.performClick(nil)
        precondition(selected == cell.tag, "grid click must preserve engine ID")
        precondition(cell.frame.width > 20 && cell.frame.height > 20, "grid cell must be visible")
    }
    if CommandLine.arguments.count > 1,
       let bitmap = panel.contentView!.bitmapImageRepForCachingDisplay(in: panel.contentView!.bounds) {
        panel.contentView!.cacheDisplay(in: panel.contentView!.bounds, to: bitmap)
        if let png = bitmap.representation(using: .png, properties: [:]) {
            try png.write(to: URL(fileURLWithPath: CommandLine.arguments[1]).appendingPathComponent("\(kind)-grid.png"))
        }
    }
}
// 模糊音近音候選：附註接在候選文字之後，點擊仍送引擎 ID；沒有附註的候選不變。
let near = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":1},"mode":"zhuyin","preedit":[{"text":"ㄘ","style":"normal"}],"candidates":{"items":[{"id":3,"text":"吃","note":"近音 ㄔ"},{"id":4,"text":"ㄘ"}],"page":0,"pages":1,"highlight":0}}"#.utf8))
panel.show(near, at: NSRect(x: bounds.minX + 40, y: bounds.minY + 40, width: 1, height: 20))
panel.contentView!.layoutSubtreeIfNeeded()
let nearButtons = descendants(panel.contentView!, of: NSButton.self)
precondition(nearButtons.count == 2, "near-reading candidates must be listed")
precondition(nearButtons[0].attributedTitle.string.contains("吃") && nearButtons[0].attributedTitle.string.contains("近音 ㄔ"), "near-reading note must follow the candidate")
precondition(!nearButtons[1].title.contains("近音"), "exact candidates must have no note")
selected = nil
nearButtons[0].performClick(nil)
precondition(selected == 3, "near-reading click must send the engine ID")
// 標籤分欄靠右、整列可點，不影響舊引擎 payload、未知值或近音提示。
let labeled = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":1},"mode":"zhuyin","candidates":{"items":[{"id":20,"text":"景","label":"common"},{"id":21,"text":"井","label":"related"},{"id":22,"text":"丼","label":"personal"},{"id":23,"text":"警","label":"future"},{"id":24,"text":"頸","label":"personal","note":"近音 ㄐㄧㄥˇ"}],"page":0,"pages":1,"highlight":2}}"#.utf8))
for appearanceName in [NSAppearance.Name.aqua, .darkAqua] {
    panel.appearance = NSAppearance(named: appearanceName)
    panel.show(labeled, at: NSRect(x: bounds.maxX - 1, y: bounds.minY + 1, width: 1, height: 20))
    panel.contentView!.layoutSubtreeIfNeeded()
    precondition(bounds.contains(panel.frame), "labeled panel must remain within screen bounds")
    precondition(!panel.isOpaque && panel.backgroundColor == .clear && panel.alphaValue == 1,
                 "only background should be translucent; text must retain opacity")
    let effects = descendants(panel.contentView!, of: NSVisualEffectView.self)
    precondition(effects.count == 1 && effects[0].blendingMode == .behindWindow && effects[0].state == .active,
                 "non-key candidate window must blend the actual background")
    let rows = descendants(panel.contentView!, of: NSButton.self)
    let expected = ["常用", "關聯", "個人常用", "", ""]
    var labelRight: CGFloat?
    for (i, row) in rows.enumerated() {
        let fields = descendants(row, of: NSTextField.self)
        let badge = fields.last!
        precondition(badge.stringValue == expected[i], "unknown labels or near notes must not create extra badges")
        precondition(fields[0].frame.maxX + 12 <= badge.frame.minX, "badge must not overlap candidate text: \(fields[0].frame) vs \(badge.frame), row \(row.frame)")
        precondition(abs(row.frame.width - rows[0].frame.width) < 0.5, "selection must cover the entire row")
        let right = row.convert(badge.frame, to: panel.contentView).maxX
        if let previous = labelRight { precondition(abs(previous - right) < 0.5, "badges must align on the right") }
        labelRight = right
        selected = nil
        row.performClick(nil)
        precondition(selected == 20 + i, "labeled row must retain its engine ID")
        if !expected[i].isEmpty {
            let point = row.convert(NSPoint(x: badge.frame.midX, y: badge.frame.midY), to: panel.contentView)
            precondition(panel.contentView!.hitTest(point) === row, "clicking badge must hit the candidate row")
        }
        let highlighted = i == 2
        precondition(badge.textColor == (highlighted ? .selectedMenuItemTextColor : .secondaryLabelColor),
                     "badge colors must follow highlight and system appearance")
        row.effectiveAppearance.performAsCurrentDrawingAppearance {
            precondition(row.layer?.backgroundColor == (highlighted ? NSColor.selectedContentBackgroundColor.cgColor : nil),
                         "selection background must resolve in the current appearance")
        }
    }
}
// 開著面板切換外觀，不靠重新建構列更新反白色。
panel.appearance = NSAppearance(named: .aqua)
panel.contentView!.layoutSubtreeIfNeeded()
let highlightedRow = descendants(panel.contentView!, of: NSButton.self)[2]
highlightedRow.effectiveAppearance.performAsCurrentDrawingAppearance {
    precondition(highlightedRow.layer?.backgroundColor == NSColor.selectedContentBackgroundColor.cgColor,
                 "open panel must update selection on appearance change")
}
// 注入輔助使用設定的讀取值；不更動使用者的系統偏好。
var reduceTransparency = false
let accessibleSurface = CandidateSurface(NSStackView(views: [NSTextField(labelWithString: "中")]),
                                         reduceTransparency: { reduceTransparency })
let accessibleEffect = descendants(accessibleSurface, of: NSVisualEffectView.self)[0]
precondition(!accessibleEffect.isHidden && accessibleSurface.layer?.backgroundColor == nil, "normal backdrop must remain translucent")
reduceTransparency = true
NSWorkspace.shared.notificationCenter.post(name: NSWorkspace.accessibilityDisplayOptionsDidChangeNotification, object: nil)
precondition(accessibleEffect.isHidden && accessibleSurface.layer?.backgroundColor != nil, "reduce transparency must switch to a solid background")
reduceTransparency = false
NSWorkspace.shared.notificationCenter.post(name: NSWorkspace.accessibilityDisplayOptionsDidChangeNotification, object: nil)
precondition(!accessibleEffect.isHidden && accessibleSurface.layer?.backgroundColor == nil, "restoring transparency must restore the effect")
panel.showHint("中", at: NSRect(x: bounds.minX + 40, y: bounds.minY + 40, width: 1, height: 20))
precondition(descendants(panel.contentView!, of: NSVisualEffectView.self).count == 1 && !panel.canBecomeKey,
             "mode hints must share the translucent surface without stealing focus")
// 提示（#123）：候選窗開著時排在最上面、候選照常可點；只有提示時幾秒後自己收起；下一個快照沒有提示就消失。
let noticed = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":2},"mode":"zhuyin","notices":["已從詞庫刪除：世傑（可在設定程式復原）"],"candidates":{"items":[{"id":5,"text":"世界"},{"id":6,"text":"視界"}],"page":0,"pages":1,"highlight":0}}"#.utf8))
panel.show(noticed, at: NSRect(x: bounds.minX + 40, y: bounds.minY + 40, width: 1, height: 20))
panel.contentView!.layoutSubtreeIfNeeded()
let noticeFields = descendants(panel.contentView!, of: NSTextField.self).filter { $0.accessibilityIdentifier() == "notice" }
precondition(noticeFields.count == 1 && noticeFields[0].stringValue == "已從詞庫刪除：世傑（可在設定程式復原）", "notice must show with the candidates")
let noticeButtons = descendants(panel.contentView!, of: NSButton.self)
precondition(noticeButtons.count == 2, "candidates stay listed under the notice")
let noticeTop = noticeFields[0].convert(NSRect(origin: .zero, size: noticeFields[0].bounds.size), to: nil).midY
let firstRow = noticeButtons[0].convert(NSRect(origin: .zero, size: noticeButtons[0].bounds.size), to: nil).midY
precondition(noticeTop > firstRow, "notice sits above the candidates")
precondition(!panel.hidesAutomatically, "a panel with candidates stays until the next key")
selected = nil
noticeButtons[1].performClick(nil)
precondition(selected == 6, "candidate under a notice keeps its engine ID")
let onlyNotice = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":2},"mode":"zhuyin","preedit":[{"text":"世傑","style":"locked"}],"notices":["已加入詞庫：世傑"]}"#.utf8))
panel.show(onlyNotice, at: NSRect(x: bounds.minX + 40, y: bounds.minY + 40, width: 1, height: 20))
precondition(panel.isVisible && panel.hidesAutomatically, "a notice alone shows and then hides by itself")
precondition(descendants(panel.contentView!, of: NSTextField.self).contains { $0.stringValue == "已加入詞庫：世傑" }, "notice text is shown")
if CommandLine.arguments.count > 1,
   let bitmap = panel.contentView!.bitmapImageRepForCachingDisplay(in: panel.contentView!.bounds) {
    panel.contentView!.cacheDisplay(in: panel.contentView!.bounds, to: bitmap)
    if let png = bitmap.representation(using: .png, properties: [:]) {
        try png.write(to: URL(fileURLWithPath: CommandLine.arguments[1]).appendingPathComponent("notice.png"))
    }
}
// 標示中：面板說明 Enter 會加入哪幾個字（VS Code 等看不出底線樣式的 App 也看得到）；補全照常顯示，不自動收起。
let marking = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":2},"mode":"zhuyin","preedit":[{"text":"我和","style":"normal"},{"text":"世傑","style":"marked"}],"suggestion":"們"}"#.utf8))
panel.show(marking, at: NSRect(x: bounds.minX + 40, y: bounds.minY + 40, width: 1, height: 20))
let markFields = descendants(panel.contentView!, of: NSTextField.self).filter { $0.accessibilityIdentifier() == "mark" }
precondition(panel.isVisible && markFields.count == 1 && markFields[0].stringValue == "Enter 加入詞庫：世傑　Esc 取消", "marking hint names the marked text")
precondition(descendants(panel.contentView!, of: NSTextField.self).contains { $0.stringValue == "⇥ 們" }, "suggestion stays visible while marking")
precondition(!panel.hidesAutomatically, "marking hint stays until the mark ends")
// 新引擎以提示送出說明時，不再重複顯示外殼自己的說明列。
let engineHint = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":2},"mode":"zhuyin","preedit":[{"text":"我和","style":"normal"},{"text":"世傑","style":"marked"}],"notices":["Enter 加入詞庫：世傑（ㄕˋ ㄐㄧㄝˊ）　Esc 取消"]}"#.utf8))
panel.show(engineHint, at: NSRect(x: bounds.minX + 40, y: bounds.minY + 40, width: 1, height: 20))
precondition(descendants(panel.contentView!, of: NSTextField.self).filter { $0.accessibilityIdentifier() == "mark" }.isEmpty
             && descendants(panel.contentView!, of: NSTextField.self).contains { $0.stringValue == "Enter 加入詞庫：世傑（ㄕˋ ㄐㄧㄝˊ）　Esc 取消" },
             "the engine hint replaces the shell's own marking line")
precondition(!panel.hidesAutomatically, "the engine's marking hint stays until the mark ends")
let afterNotice = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":2},"mode":"zhuyin","preedit":[{"text":"世傑","style":"locked"}]}"#.utf8))
panel.show(afterNotice, at: .zero)
precondition(!panel.isVisible, "the next snapshot without notices hides it")
panel.noticeSeconds = 0.05
panel.show(onlyNotice, at: NSRect(x: bounds.minX + 40, y: bounds.minY + 40, width: 1, height: 20))
RunLoop.main.run(until: Date().addingTimeInterval(0.3))
precondition(!panel.isVisible, "a notice alone hides after its timeout")
// #162：中英混打打 20 時面板只顯示讀音 ㄉㄢ（按空白出「單」、Enter 留 20），不自動收起；數字以虛線底線加灰字交給 App。
let digits = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":2},"mode":"zhuyin","preedit":[{"text":"20","style":"reading"}],"reading_hint":{"start":0,"length":2,"text":"ㄉㄢ"}}"#.utf8))
precondition(digits.visibleReading == "ㄉㄢ", "reading hint decodes")
panel.show(digits, at: NSRect(x: bounds.minX + 40, y: bounds.minY + 40, width: 1, height: 20))
let readingFields = descendants(panel.contentView!, of: NSTextField.self).filter { $0.accessibilityIdentifier() == "reading" }
precondition(panel.isVisible && readingFields.count == 1 && readingFields[0].stringValue == "ㄉㄢ" && !panel.hidesAutomatically,
             "reading hint shows the syllable under the caret and stays while composing")
precondition(descendants(panel.contentView!, of: NSButton.self).isEmpty, "reading hint alone has no candidate rows")
if CommandLine.arguments.count > 1, let view = panel.contentView, let bitmap = view.bitmapImageRepForCachingDisplay(in: view.bounds) {
    view.cacheDisplay(in: view.bounds, to: bitmap)
    if let png = bitmap.representation(using: .png, properties: [:]) {
        try png.write(to: URL(fileURLWithPath: CommandLine.arguments[1]).appendingPathComponent("reading.png"))
    }
}
let readingMarked = MarkedText.attributed(digits.preedit ?? [])
let readingUnderline = readingMarked.attribute(.underlineStyle, at: 0, effectiveRange: nil) as? Int ?? 0
precondition(readingUnderline & NSUnderlineStyle.patternDash.rawValue != 0
             && readingMarked.attribute(.foregroundColor, at: 0, effectiveRange: nil) as? NSColor == MarkedText.readingColor,
             "reading digits are dashed and gray")
let plainMarked = MarkedText.attributed([Snapshot.Segment(text: "20", style: "pending")])
precondition(plainMarked.attribute(.foregroundColor, at: 0, effectiveRange: nil) == nil, "ordinary composition keeps the app's color")
let legacy = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":true,"cursor":{"utf16":2},"mode":"zhuyin","preedit":[{"text":"20","style":"pending"}]}"#.utf8))
precondition(legacy.visibleReading == nil, "old engines without reading_hint show nothing new")
let empty = try JSONDecoder().decode(Snapshot.self, from: Data(#"{"handled":false,"cursor":{"utf16":0},"mode":"zhuyin"}"#.utf8))
panel.show(empty, at: .zero)
precondition(!panel.isVisible, "empty snapshot must hide the panel")
// #194：App 是隱藏狀態時解除隱藏、不搶前景。以 open -j 隱藏啟動的情況這裡模擬不出來（行程內 hide 之後
// orderFrontRegardless 自己會解除），真的隱藏啟動要在真機驗收（platforms/macos/README.md）。
app.hide(nil)
precondition(app.isHidden, "test setup: app must be hidden")
CandidatePanel.revealApp(app)
precondition(!app.isHidden && !app.isActive, "revealApp unhides without activating")
print("Passed native candidate panel checks: legacy payload, near notes, labels, light/dark layout, hit testing, focus, accessibility fallback, hints and notices, hidden app")
