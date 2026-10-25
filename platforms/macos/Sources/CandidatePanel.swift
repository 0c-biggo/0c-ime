import AppKit

final class CandidatePanel: NSPanel {
    var choose: ((Int) -> Void)?

    init() {
        super.init(contentRect: .zero, styleMask: [.borderless, .nonactivatingPanel],
                   backing: .buffered, defer: false)
        level = .popUpMenu
        isFloatingPanel = true
        hasShadow = true
        hidesOnDeactivate = false
        collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        isOpaque = false
        backgroundColor = .clear
        isReleasedWhenClosed = false
    }
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }

    /// App 是隱藏狀態時，面板排好版、orderFrontRegardless 也不會上螢幕：以 open -j（隱藏啟動）開起來的輸入法程式整個是隱藏的，
    /// 空白鍵、↓ 叫不出選字窗（#194，0.2.3 自動換版後以 open -g -j 啟動新版）。啟動時與每次顯示前解除隱藏，不搶前景。
    static func revealApp(_ app: NSApplication? = NSApp) {
        if let app, app.isHidden { app.unhideWithoutActivation() }
    }

    func show(_ snapshot: Snapshot, at caret: NSRect) {
        let stack = NSStackView()
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 2
        stack.edgeInsets = NSEdgeInsets(top: 8, left: 10, bottom: 8, right: 10)
        if let candidates = snapshot.candidates {
            let columns = max(1, min(candidates.columns ?? 1, 10))
            let screen = NSScreen.screens.first { $0.frame.intersects(caret) } ?? NSScreen.main
            let available = max(100, (screen?.visibleFrame.width ?? 1440) - 20)
            let cellWidth = min(candidates.kind == "kaomoji" ? 240 : 160,
                (available - CGFloat(columns - 1) * 6) / CGFloat(columns))
            if columns > 1 { stack.widthAnchor.constraint(equalToConstant: cellWidth * CGFloat(columns) + CGFloat(columns - 1) * 6 + 20).isActive = true }
            if let title = candidates.title, !title.isEmpty {
                let heading = NSTextField(wrappingLabelWithString: title)
                heading.font = .systemFont(ofSize: 12)
                heading.textColor = .secondaryLabelColor
                stack.addArrangedSubview(heading)
            }
            var row: NSStackView?
            var rowButtons: [NSButton] = []
            func finishRow() {
                let height = rowButtons.map { $0.intrinsicContentSize.height }.max() ?? 0
                for button in rowButtons { button.heightAnchor.constraint(equalToConstant: height).isActive = true }
                rowButtons = []
            }
            for (index, candidate) in candidates.items.enumerated() {
                let button = CandidateRow(candidate, key: snapshot.candidateKey(index),
                                          highlighted: index == candidates.highlight,
                                          gridWidth: columns > 1 ? cellWidth : nil,
                                          largeEmoji: candidates.kind == "emoji")
                button.target = self
                button.action = #selector(selectCandidate(_:))
                if columns > 1 {
                    if index % columns == 0 {
                        finishRow()
                        let next = NSStackView()
                        next.orientation = .horizontal
                        next.alignment = .top
                        next.spacing = 6
                        stack.addArrangedSubview(next)
                        row = next
                    }
                    row?.addArrangedSubview(button)
                    button.widthAnchor.constraint(equalToConstant: cellWidth).isActive = true
                    rowButtons.append(button)
                } else {
                    stack.addArrangedSubview(button)
                    button.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -20).isActive = true
                }
            }
            finishRow()
            var keys = columns > 1 ? "方向鍵移動 · PgUp/PgDn 翻頁" : "← → 翻頁"
            // 改字的候選按 j／k 換前後一個字（#193）；按了有沒有作用由核心判斷（step_keys），外殼不自己猜。
            if candidates.stepKeys == true {
                keys += " · J\u{00A0}K\u{00A0}換\u{2060}字" // 窄的候選窗換行時「J K 換字」整段移到下一行
            }
            let footer = NSTextField(wrappingLabelWithString: "\(candidates.page + 1) / \(candidates.pages)　\(keys)")
            footer.font = .systemFont(ofSize: 12)
            stack.addArrangedSubview(footer)
        }
        if let suggestion = snapshot.suggestion, !suggestion.isEmpty {
            // Tab 補全提示（長詞補全、近音修正、神經次佳、英文補全共用）跟候選同字級（#118），⇥ 用次要文字色。
            let hint = NSMutableAttributedString(string: "⇥ ", attributes: [
                .font: NSFont.systemFont(ofSize: 18), .foregroundColor: NSColor.secondaryLabelColor
            ])
            hint.append(NSAttributedString(string: suggestion, attributes: [
                .font: NSFont.systemFont(ofSize: 18), .foregroundColor: NSColor.labelColor
            ]))
            let label = NSTextField(labelWithAttributedString: hint)
            label.setAccessibilityIdentifier("suggestion")
            stack.addArrangedSubview(label)
        }
        // 讀音提示（#162）：中英混打打 20 時在游標下方顯示 ㄉㄢ，比照常見注音輸入法在數字下方附注音，事先看得出按空白會出中文
        // （Enter 留數字）。只有讀音本身，面板很小、不擋字；開候選時引擎不送。
        if let reading = snapshot.visibleReading {
            let label = NSTextField(labelWithString: reading)
            label.font = .systemFont(ofSize: 15)
            label.textColor = .labelColor
            label.setAccessibilityIdentifier("reading")
            stack.addArrangedSubview(label)
        }
        // 標示中（Shift＋←／→，#123）說明 Enter 會加入哪幾個字：VS Code 等自己畫組字的 App 看不出底線樣式，
        // 這行在每個 App 都看得到。新引擎會以提示送出說明（含讀音與不能加入的原因），這時不重複顯示。
        if let marked = snapshot.markedText, snapshot.visibleNotices.isEmpty {
            let label = NSTextField(wrappingLabelWithString: "Enter 加入詞庫：\(marked)　Esc 取消")
            label.font = .systemFont(ofSize: 14)
            label.textColor = .secondaryLabelColor
            label.setAccessibilityIdentifier("mark")
            stack.insertArrangedSubview(label, at: 0)
        }
        // 提示（例如「已加入詞庫：世傑」、反悔鍵的結果，#123）放在最上面，候選窗開著時也看得到；
        // 下一個按鍵的快照沒有提示就消失。
        let notices = snapshot.visibleNotices
        for (offset, notice) in notices.enumerated() {
            let label = NSTextField(wrappingLabelWithString: notice)
            label.font = .systemFont(ofSize: 14, weight: .medium)
            label.textColor = .labelColor
            label.setAccessibilityIdentifier("notice")
            stack.insertArrangedSubview(label, at: offset)
        }
        guard !stack.arrangedSubviews.isEmpty else { orderOut(nil); return }
        contentView = CandidateSurface(stack)
        hideTimer?.invalidate()
        place(stack.fittingSize, below: caret)
        Self.revealApp()
        orderFrontRegardless()
        // 只有提示、沒有候選與補全時，幾秒後自己收起（沒有下一個按鍵時不一直擋在游標下方）；
        // 標示中的說明（引擎每個快照都送）留到標示結束。
        if !notices.isEmpty, stack.arrangedSubviews.count == notices.count, snapshot.markedText == nil {
            hideTimer = Timer.scheduledTimer(withTimeInterval: noticeSeconds, repeats: false) { [weak self] _ in self?.orderOut(nil) }
        }
    }

    /// 只有提示時面板停留的秒數。
    var noticeSeconds: TimeInterval = 3
    /// 只有提示的面板是否排定自動收起（測試用）。
    var hidesAutomatically: Bool { hideTimer?.isValid == true }

    private var hideTimer: Timer?

    /// 在游標下方短暫顯示一段提示（例如切換中英時的「中」「英」），一段時間後自動隱藏。
    func showHint(_ text: String, at caret: NSRect, for seconds: TimeInterval = 0.8) {
        let label = NSTextField(labelWithString: text)
        label.font = .systemFont(ofSize: 18, weight: .medium)
        let stack = NSStackView(views: [label])
        stack.edgeInsets = NSEdgeInsets(top: 4, left: 10, bottom: 4, right: 10)
        contentView = CandidateSurface(stack)
        place(stack.fittingSize, below: caret)
        Self.revealApp()
        orderFrontRegardless()
        hideTimer?.invalidate()
        hideTimer = Timer.scheduledTimer(withTimeInterval: seconds, repeats: false) { [weak self] _ in self?.orderOut(nil) }
    }

    private func place(_ size: NSSize, below caret: NSRect) {
        let screen = NSScreen.screens.first { $0.frame.intersects(caret) } ?? NSScreen.main
        let bounds = screen?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        let x = min(max(caret.minX, bounds.minX), max(bounds.minX, bounds.maxX - size.width))
        var y = caret.minY - size.height - 4
        if y < bounds.minY { y = caret.maxY + 4 }
        y = min(max(y, bounds.minY), max(bounds.minY, bounds.maxY - size.height))
        setFrame(NSRect(origin: NSPoint(x: x, y: y), size: size), display: true)
    }

    @objc private func selectCandidate(_ sender: NSButton) { choose?(sender.tag) }
}

/// 背景使用系統毛玻璃；輸入法面板永不取得 key，因此效果明確設為 active。
/// 降低透明度時隱藏效果，改用系統實色；外觀與輔助使用設定可在面板開啟時變更。
final class CandidateSurface: NSView {
    private let effect = NSVisualEffectView()
    private var accessibilityObserver: NSObjectProtocol?
    private let reduceTransparency: () -> Bool

    init(_ stack: NSStackView, reduceTransparency: @escaping () -> Bool = {
        NSWorkspace.shared.accessibilityDisplayShouldReduceTransparency
    }) {
        self.reduceTransparency = reduceTransparency
        super.init(frame: .zero)
        wantsLayer = true
        layer?.cornerRadius = 10
        layer?.masksToBounds = true
        layer?.borderWidth = 0.5
        effect.material = .popover
        effect.blendingMode = .behindWindow
        effect.state = .active
        for view in [effect, stack] {
            view.translatesAutoresizingMaskIntoConstraints = false
            addSubview(view)
            NSLayoutConstraint.activate([
                view.leadingAnchor.constraint(equalTo: leadingAnchor),
                view.trailingAnchor.constraint(equalTo: trailingAnchor),
                view.topAnchor.constraint(equalTo: topAnchor),
                view.bottomAnchor.constraint(equalTo: bottomAnchor)
            ])
        }
        accessibilityObserver = NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.accessibilityDisplayOptionsDidChangeNotification, object: nil, queue: .main
        ) { [weak self] _ in self?.updateSurface() }
        updateSurface()
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }
    deinit {
        if let observer = accessibilityObserver { NSWorkspace.shared.notificationCenter.removeObserver(observer) }
    }
    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        updateSurface()
    }
    private func updateSurface() {
        let reduceTransparency = reduceTransparency()
        effect.isHidden = reduceTransparency
        effectiveAppearance.performAsCurrentDrawingAppearance {
            layer?.backgroundColor = reduceTransparency ? NSColor.windowBackgroundColor.cgColor : nil
            layer?.borderColor = NSColor.separatorColor.cgColor
        }
    }
}

/// 文字不攔截滑鼠，整列（包含右側標籤）都送同一個候選 ID。
private final class CandidateText: NSTextField {
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

private final class CandidateRow: NSButton {
    private let textField = CandidateText(labelWithString: "")
    private let labelField = CandidateText(labelWithString: "")
    private let candidateHighlighted: Bool
    private let gridWidth: CGFloat?

    /// key 是鍵標（#112，1、q、;）；空字串時不畫（英文模式的補字候選）。
    init(_ candidate: Snapshot.Candidate, key: String, highlighted: Bool, gridWidth: CGFloat? = nil, largeEmoji: Bool = false) {
        self.candidateHighlighted = highlighted
        self.gridWidth = gridWidth
        super.init(frame: .zero)
        tag = candidate.id
        isBordered = false
        alignment = .left
        font = .systemFont(ofSize: 18)
        let prefix = key.isEmpty ? "" : "\(key)  "
        title = prefix + candidate.text
        contentTintColor = highlighted ? .selectedMenuItemTextColor : .labelColor
        let color: NSColor = highlighted ? .selectedMenuItemTextColor : .labelColor
        let attributed = NSMutableAttributedString(string: title, attributes: [
            .font: NSFont.systemFont(ofSize: 18), .foregroundColor: color
        ])
        if largeEmoji {
            attributed.addAttribute(.font, value: NSFont.systemFont(ofSize: 32),
                range: NSRange(location: prefix.utf16.count, length: candidate.text.utf16.count))
        }
        if let note = candidate.note, !note.isEmpty {
            // 用動態次要文字色，避免背景輸入法行程切換外觀後附註變成白底白字。
            let noteColor: NSColor = highlighted ? .selectedMenuItemTextColor : .secondaryLabelColor
            attributed.append(NSAttributedString(string: "  \(note)", attributes: [
                .font: NSFont.systemFont(ofSize: 12), .foregroundColor: noteColor
            ]))
        }
        // NSButton 的 cell 不重複畫文字；attributedTitle 保留內容供輔助工具／既有檢查讀取。
        attributedTitle = attributed
        textField.attributedStringValue = attributed
        switch candidate.note?.isEmpty == false ? nil : candidate.label {
        case "common": labelField.stringValue = "常用"
        case "related": labelField.stringValue = "關聯"
        case "personal": labelField.stringValue = "個人常用"
        default: break // 舊引擎沒有標籤或新版未知值時安全略過
        }
        labelField.font = .systemFont(ofSize: 12)
        labelField.textColor = highlighted ? .selectedMenuItemTextColor : .secondaryLabelColor
        labelField.alignment = .right
        for field in [textField, labelField] {
            field.translatesAutoresizingMaskIntoConstraints = false
            field.setContentCompressionResistancePriority(gridWidth == nil ? .required : .defaultLow, for: .horizontal)
            addSubview(field)
            if gridWidth == nil { field.centerYAnchor.constraint(equalTo: centerYAnchor).isActive = true }
        }
        if let width = gridWidth {
            textField.maximumNumberOfLines = 0
            textField.lineBreakMode = .byWordWrapping
            textField.preferredMaxLayoutWidth = max(20, width - 16)
            NSLayoutConstraint.activate([
                textField.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
                textField.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
                textField.topAnchor.constraint(equalTo: topAnchor, constant: 6),
                labelField.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
                labelField.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
                labelField.topAnchor.constraint(greaterThanOrEqualTo: textField.bottomAnchor, constant: 2),
                labelField.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -6)
            ])
        } else { NSLayoutConstraint.activate([
            textField.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
            labelField.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
            labelField.leadingAnchor.constraint(greaterThanOrEqualTo: textField.trailingAnchor, constant: 16)
        ]) }
        wantsLayer = true
        layer?.cornerRadius = 6
        setAccessibilityLabel([title, candidate.note ?? "", labelField.stringValue].filter { !$0.isEmpty }.joined(separator: " "))
        updateHighlight()
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }
    override var intrinsicContentSize: NSSize {
        if let width = gridWidth {
            let rect = textField.attributedStringValue.boundingRect(with: NSSize(width: max(20, width - 16), height: .greatestFiniteMagnitude), options: [.usesLineFragmentOrigin, .usesFontLeading])
            return NSSize(width: width, height: ceil(rect.height) + labelField.intrinsicContentSize.height + 14)
        }
        return NSSize(width: textField.intrinsicContentSize.width + labelField.intrinsicContentSize.width + 32,
               height: max(20, textField.intrinsicContentSize.height) + 8)
    }
    override func draw(_ dirtyRect: NSRect) {} // 文字由子視圖繪製，整列仍是可點擊的 NSButton
    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        updateHighlight()
    }
    private func updateHighlight() {
        effectiveAppearance.performAsCurrentDrawingAppearance {
            layer?.backgroundColor = candidateHighlighted ? NSColor.selectedContentBackgroundColor.cgColor : nil
        }
    }
}
