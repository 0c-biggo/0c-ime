import AppKit
import InputMethodKit
import Carbon

@objc(LingxiInputController)
final class LingxiInputController: IMKInputController {
    private lazy var session = EngineHost.shared.session { [weak self] in
        !SecureInputPolicy.bypass(secure: IsSecureEventInputEnabled(), client: self?.client()?.bundleIdentifier())
    }
    private lazy var panel: CandidatePanel = {
        let panel = CandidatePanel()
        panel.choose = { [weak self] id in
            guard let self, let client = self.client() else { return }
            _ = self.send(Request(type: "select", index: id), to: client)
        }
        return panel
    }()
    private var snapshot: Snapshot? {
        didSet { if snapshot == nil { shownMarked = nil } }
    }
    // 上一次交給 App 的組字；內容相同時不重送（#108）。snapshot 清空（重設、失焦、改插入文字）時一併忘記。
    private var shownMarked: MarkedText.Shown?
    private var rendering = false
    // 最近一次提交的代號與文字：下一段組字前確認游標仍緊接其後，才請引擎以它作為前文。
    private var pendingContext: (id: UInt64, text: String)?
    private let replacement = NSRange(location: NSNotFound, length: 0)
    // 引擎冷啟動期間暫存的請求（含原始字元，引擎不處理時補送給 App），連上後依序重送。
    private let queued = RequestQueue()
    private var retry: Timer?

    // 更新交接（#170，Retire.swift）：任一個輸入控制器還在組字時，收到 SIGTERM 也先不結束。
    private static let live = NSHashTable<LingxiInputController>.weakObjects()
    static func anyComposing() -> Bool { live.allObjects.contains { $0.snapshot?.composing == true || !$0.queued.isEmpty } }

    // Retain InputMethodKit's default mouse handling: clicking outside marked
    // text commits it. System shortcuts switch input sources (no Shift hook).
    override func recognizedEvents(_ sender: Any!) -> Int {
        Int(NSEvent.EventTypeMask.keyDown.rawValue | NSEvent.EventTypeMask.flagsChanged.rawValue)
    }

    // 單按 Shift（按下到放開之間沒有其他按鍵、也沒有 Command／Control／Option，按住不超過 0.3 秒）切換注音／英文，
    // 與 Windows、Linux 相同；保留已組好的字，英數插入游標處，切到英文時清掉未打完的注音（#90）。
    // 哪一側切換看設定「中英切換」（#117 toggle-key，預設右 Shift）；不切換的一側照舊交給系統（Shift 加字母仍是大寫）。
    // Caps Lock 另見 capsMode。左右各用一個 RightShiftTap（名稱沿用右 Shift，判斷規則兩側相同，也一樣忽略 Chromium／Electron 重複送的按下）。
    private var rightShift = RightShiftTap()
    private var leftShift = RightShiftTap()
    // 上一個操作是成功的切換：再按一次 Shift 時請核心放回切到英文時清掉的未打完注音（#90 防誤觸）。
    // 任何按鍵、其他請求、點選或失焦都清除。
    private var toggleArmed = false
    private lazy var hint = CandidatePanel()

    // Caps Lock 是英文大寫模式（#84，與 Windows 同樣輸出大寫）。Info.plist 宣告 TICapsLockLanguageSwitchCapable（#67），
    // 系統的「用大寫鎖定鍵切換 ABC」開著時短按由系統切換輸入法，按住才開大寫鎖定、走到這裡：開啟時先送出組字、切到英文並提示「英」，字母輸出大寫
    // （Shift＋字母也是大寫，同系統行為）；要中英混在同一段組字裡用右 Shift。
    // 關閉時回到開啟前的模式（#219）：由核心記住（caps_lock 請求，與 Windows、Linux 的 #173 同一套），Shift 切到英文再開關
    // Caps Lock 仍是英文；開著期間單按 Shift 切換過就照切換後的模式。提示照核心回覆的模式（#146）。
    // 以每個按鍵當下的 Caps Lock 狀態為準。
    private var capsMode = false
    // 這次開啟送的是 mode（舊引擎沒有 caps_lock_request，或剛登入引擎還沒起來問不到）：關閉也送 mode，照舊回注音／共用的模式。
    private var capsViaMode = false

    private func syncCaps(_ on: Bool, client: IMKTextInput, showHint: Bool) {
        // 沒變化時不碰引擎能力（剛登入還不知道時會同步問一次引擎）。核心忘了 Caps Lock（mode 請求、重開 session）時重新告訴它。
        guard on != capsMode || !capsViaMode && (snapshot?.capsLock ?? false) != on else { return }
        let tried = !capsViaMode && session.capsLockRequests
        if tried {
            if send(Request(type: "caps_lock", on: on), to: client) {
                capsMode = on
                if showHint { showModeHint(client) }
                return
            }
            // 引擎回覆沒處理（交接中換成不認得 caps_lock 的引擎）才改送 mode；送不出去（失敗、安全輸入）就等下一個按鍵。
            guard snapshot?.handled == false, on != capsMode else { return }
        } else if on == capsMode { return }
        if on, snapshot?.composing == true || !queued.isEmpty {
            _ = send(Request(type: "commit"), to: client)
        }
        guard send(Request(type: "mode", on: on), to: client) else { return }
        // 冷啟動的 open 可能剛在上面取得能力，補同步 Caps 的來源（小寫風格要靠它打小寫）；關閉照舊送 mode。
        if on, !tried, session.capsLockRequests { _ = send(Request(type: "caps_lock", on: true), to: client) }
        capsMode = on
        capsViaMode = on
        if showHint { showModeHint(client) }
    }

    private func showModeHint(_ client: IMKTextInput) {
        // #146：Caps 關閉不一定回注音；只顯示核心已確認的模式。
        // 冷啟動／排隊時快照仍可能是舊的，與 Shift 一樣不先猜測提示。
        guard !queued.isProcessing, queued.isEmpty, let mode = snapshot?.mode else { return }
        hint.level = panel.level
        hint.showHint(mode == "english" ? "英" : "中", at: caretRect(client))
    }

    private func handleFlags(_ event: NSEvent, client: IMKTextInput) -> Bool {
        let capsLockKey: UInt16 = 57, leftShiftKey: UInt16 = 56, rightShiftKey: UInt16 = 60
        if event.keyCode == capsLockKey {
            syncCaps(event.modifierFlags.contains(.capsLock), client: client, showHint: true)
            return false
        }
        guard event.keyCode == rightShiftKey || event.keyCode == leftShiftKey else {
            rightShift.reset()
            leftShift.reset()
            return false
        }
        // 另一側 Shift 的變化讓這一側不再是單按。
        let left = event.keyCode == leftShiftKey
        if left { rightShift.reset() } else { leftShift.reset() }
        let modified = !event.modifierFlags.intersection([.command, .control, .option]).isEmpty
        let pressed = event.modifierFlags.contains(.shift)
        let limit = session.shiftTapLimit
        // 這一側 Shift 此刻實際上是否按著；Chromium 晚到的重複按下送來時鍵早已放開（#148）。
        let held = !pressed || CGEventSource.keyState(.combinedSessionState, key: CGKeyCode(event.keyCode))
        let at = RightShiftTap.eventTime(timestamp: event.timestamp, now: ProcessInfo.processInfo.systemUptime,
                                         sinceLastModifierChange: CGEventSource.secondsSinceLastEventType(.combinedSessionState, eventType: .flagsChanged))
        let toggle = left ? leftShift.flags(pressed: pressed, modified: modified, at: at, limit: limit, held: held)
                          : rightShift.flags(pressed: pressed, modified: modified, at: at, limit: limit, held: held)
        // 確定是單按時才看設定（快取，不等引擎）；這一側沒開時照舊交給系統。
        guard toggle, session.shiftToggles(left: left) else { return false }
        guard send(Request(type: "toggle", restore: toggleArmed ? true : nil), to: client) else { return false }
        toggleArmed = true
        showModeHint(client)
        return true
    }

    override func handle(_ event: NSEvent!, client sender: Any!) -> Bool {
        guard let event, let client = sender as? IMKTextInput else { return false }
        RetireMonitor.shared.noteKey()
        Self.live.add(self)
        guard !bypassSecureInput(client) else { return false }
        if event.type == .flagsChanged { return handleFlags(event, client: client) }
        guard event.type == .keyDown else { return false }
        rightShift.otherKey()
        leftShift.otherKey()
        toggleArmed = false // 交還 App 的快捷鍵與未對應的按鍵也算切換之後的操作
        // 用快捷鍵切換簡體字（#192）：設定開著（引擎回報 simplified_hotkey）時吃掉 ⌃⇧F11，跟選單的「輸出簡體字」同一條路：
        // 組字不動、游標旁提示「已切換為輸出簡體字／繁體字」，選單打勾跟著變。設定關閉或舊引擎時照一般快捷鍵交給 App。
        if KeyMapping.simplifiedHotkey(code: event.keyCode, modifiers: event.modifierFlags), session.simplifiedHotkey {
            simplifiedMenuAction(nil)
            return true
        }
        syncCaps(event.modifierFlags.contains(.capsLock), client: client, showHint: false)
        // Ctrl＋標點打全形（#168）：注音模式、引擎回報 ctrl_punctuation 時交給引擎插進組字區，沒在組字時也一樣。
        // 引擎沒處理（設定剛關掉、模式不同步）就照下面的一般快捷鍵先送出組字再交給 App。
        if let ctrl = KeyMapping.ctrlPunct(code: event.keyCode, modifiers: event.modifierFlags),
           session.ctrlPunct(key: ctrl), !capsMode, snapshot?.mode != "english" {
            confirmDocumentContext(client)
            if send(Request(type: "key", key: ctrl), to: client, characters: KeyMapping.ctrlPunctText[ctrl]) { return true }
            if !flushQueuedEnglish(to: client) { commitComposition(sender) }
            return false
        }
        guard let key = KeyMapping.key(code: event.keyCode, characters: event.characters,
                                       modifiers: event.modifierFlags, vocabKeys: session.vocabKeys,
                                       styleShiftSpace: session.styleShiftSpace, capsLower: session.styleCapsLower) else {
            if !flushQueuedEnglish(to: client) { commitComposition(sender) }
            return false
        }
        confirmDocumentContext(client)
        // 英文模式下沒有組字時引擎交還字母，App 照 Caps Lock 收到大寫，與右 Shift 的英文模式同一條路。
        return send(Request(type: "key", key: key), to: client, characters: KeyMapping.text(for: key))
    }

    // 輸入法選單（選單列的輸入來源選單）：背景執行被關閉時固定顯示回去的入口，不只靠通知。
    // 系統每次組字更新後都會要一次選單（一個按鍵一次），這裡只讀快取、不做同步查詢，查詢都丟到背景並節流（#148）：
    // 主執行緒卡住時系統把按鍵原樣交給 App（變英文），選單裡靈犀的項目只剩「……」。
    override func menu() -> NSMenu! {
        let menu = NSMenu()
        menu.autoenablesItems = false
        UpdateMenuMonitor.shared.refreshForMenu()
        UpdateMenuMonitor.shared.append(to: menu, target: self, action: #selector(updateMenuAction(_:)))
        // 「輸出簡體字」（#169）：可以打勾，放在「我的詞彙…」下面（append 的第一個分隔線之前）。引擎能即時切換時照引擎
        // 目前的值；舊引擎照 0c-update 回報的設定檔。都不知道時（0.2.1 以前的 0c-update、引擎沒在執行）不顯示。
        if let on = Self.simplifiedState() {
            let item = NSMenuItem(title: "輸出簡體字", action: #selector(simplifiedMenuAction(_:)), keyEquivalent: "")
            item.target = self
            item.state = on ? .on : .off
            item.isEnabled = true
            let at = menu.items.firstIndex(where: \.isSeparatorItem) ?? menu.items.count
            menu.insertItem(item, at: at)
        }
        menu.addItem(.separator())
        ModelStatusMonitor.shared.refreshForMenu()
        for title in ModelStatusMonitor.shared.labels {
            let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
            item.isEnabled = false
            menu.addItem(item)
        }
        UpdateAgentMonitor.shared.refreshStatus(minimumInterval: UpdateMenuMonitor.menuRefreshInterval)
        guard let status = UpdateAgentMonitor.shared.status else { return menu } // 啟動後第一次查詢還沒回來
        menu.addItem(.separator())
        switch status {
        case .requiresApproval:
            menu.addItem(NSMenuItem(title: "自動更新已關閉 — 開啟背景執行…", action: #selector(openLoginItems(_:)), keyEquivalent: ""))
        case .enabled:
            let item = NSMenuItem(title: "自動更新：已開啟", action: nil, keyEquivalent: "")
            item.isEnabled = false
            menu.addItem(item)
        default:
            let item = NSMenuItem(title: "自動更新：未設定", action: nil, keyEquivalent: "")
            item.isEnabled = false
            menu.addItem(item)
        }
        return menu
    }

    @objc func openLoginItems(_ sender: Any?) { UpdateAgentMonitor.openSettings() }

    static func simplifiedState() -> Bool? {
        if EngineSession.simplifiedLive { return EngineSession.outputSimplified }
        return UpdateMenuMonitor.shared.status?.outputSimplified
    }

    /// 選單的「輸出簡體字」（#169）：引擎能即時切換時直接在這個欄位的 session 送請求（組字不動、之後送出的字立刻照新設定，
    /// 引擎寫設定檔），在游標旁提示「已切換為輸出簡體字／繁體字」。IMKit 的 sender 是 infoDictionary，這裡不需要它。
    /// 舊引擎或引擎還沒起來時退回 0c-update set output-simplified … -apply -notify。
    @objc func simplifiedMenuAction(_ sender: Any?) {
        let on = !(Self.simplifiedState() ?? false)
        if EngineSession.simplifiedLive, let client = client(), !bypassSecureInput(client), !queued.isProcessing, queued.isEmpty,
           let next = try? session.handle(Request(type: "output_simplified", on: on)), next.handled {
            var shown = next
            shown.notices = nil // 提示用游標旁的小面板，不留在候選窗
            render(shown, to: client)
            EngineSession.note(next)
            if let notice = next.visibleNotices.first {
                hint.level = panel.level
                hint.showHint(notice, at: caretRect(client), for: 1.6)
            }
            ModelStatusMonitor.shared.refresh()
            return
        }
        UpdateMenuMonitor.shared.setSimplified(on)
    }

    @objc func updateMenuAction(_ sender: Any?) {
        if let item = UpdateMenuMonitor.selectedItem(from: sender) { UpdateMenuMonitor.shared.perform(item) }
    }

    override func activateServer(_ sender: Any!) {
        dropQueued()
        capsMode = false
        capsViaMode = false
        toggleArmed = false
        rightShift.reset()
        leftShift.reset()
        hint.orderOut(nil)
        snapshot = nil
        pendingContext = nil
        lastCaret = nil
        session.reset()
        panel.orderOut(nil)
        // 從設定程式改完切換鍵回到 App 時，在背景先把新設定讀進快取；單按 Shift 不在主執行緒上問引擎（#148）。
        ModelStatusMonitor.shared.refresh()
        (sender as? IMKTextInput)?.overrideKeyboard(withKeyboardNamed: "com.apple.keylayout.US")
    }

    override func deactivateServer(_ sender: Any!) {
        dropQueued()
        capsMode = false
        capsViaMode = false
        toggleArmed = false
        rightShift.reset()
        leftShift.reset()
        hint.orderOut(nil)
        commitComposition(sender)
        dropQueued() // callbacks from the old client must not survive focus loss
        pendingContext = nil
        lastCaret = nil
        session.reset()
        snapshot = nil
        panel.orderOut(nil)
    }

    override func commitComposition(_ sender: Any!) {
        toggleArmed = false // 點到別處等由系統結束組字的時機；沒有組字時也不再放回注音
        guard let client = sender as? IMKTextInput,
              !bypassSecureInput(client), snapshot?.composing == true || !queued.isEmpty else { return }
        _ = send(Request(type: "commit"), to: client)
    }

    override func hidePalettes() { panel.orderOut(nil) }
    override func inputControllerWillClose() {
        dropQueued()
        pendingContext = nil
        panel.orderOut(nil)
        session.reset()
        snapshot = nil
        super.inputControllerWillClose()
    }

    /// 只比對游標（無選取）前的文字是否正好是剛提交的內容；讀到的文字不送出、不記錄。
    private func confirmDocumentContext(_ client: IMKTextInput) {
        guard !queued.isProcessing, queued.isEmpty, !rendering,
              snapshot?.composing != true, let pending = pendingContext else { return }
        pendingContext = nil
        let length = (pending.text as NSString).length
        let selection = client.selectedRange()
        guard session.isOpen, length > 0, selection.location != NSNotFound, selection.length == 0,
              selection.location >= length,
              client.attributedSubstring(from: NSRange(location: selection.location - length, length: length))?.string == pending.text
        else { return }
        _ = try? session.handle(Request(type: "context", on: true, contextID: pending.id))
    }

    private func dropQueued() {
        retry?.invalidate()
        retry = nil
        if !queued.isEmpty { capsMode = false; capsViaMode = false }
        queued.clear()
    }

    // Terminal 的安全鍵盤輸入仍可能把事件交給 IMK。必須在解析按鍵、讀取前文及
    // 重播前攔下；只保留已顯示的舊組字，不請引擎提交（避免評分／學習）。
    // 只看客戶端是不是終端機：別的 App 留著的安全輸入不能讓這裡停擺（見 SecureInputPolicy）。
    private func bypassSecureInput(_ client: IMKTextInput) -> Bool {
        guard SecureInputPolicy.bypass(secure: IsSecureEventInputEnabled(), client: client.bundleIdentifier()) else { return false }
        dropQueued()
        rightShift.reset()
        leftShift.reset()
        capsMode = false
        capsViaMode = false
        pendingContext = nil
        lastCaret = nil
        panel.orderOut(nil)
        hint.orderOut(nil)
        let text = snapshot?.text ?? ""
        snapshot = nil
        session.reset()
        if !rendering, !text.isEmpty {
            rendering = true
            client.insertText(text, replacementRange: replacement)
            rendering = false
        }
        return true
    }

    // 引擎啟動中：每 50ms 檢查一次，連上後依序重送；5 秒內都沒起來就放棄（按鍵遺失，記錄原因）。
    private func scheduleReplay(_ client: IMKTextInput) {
        guard retry == nil else { return }
        let started = ProcessInfo.processInfo.systemUptime
        retry = Timer.scheduledTimer(withTimeInterval: 0.05, repeats: true) { [weak self] _ in
            guard let self else { return }
            guard !self.bypassSecureInput(client), !self.queued.isProcessing, !self.rendering else { return }
            if self.session.ready() {
                self.retry?.invalidate()
                self.retry = nil
                self.replay(client)
            } else if ProcessInfo.processInfo.systemUptime - started > 5 {
                NSLog("0c: engine did not start; dropped %d queued keys", self.queued.count)
                self.dropQueued()
            }
        }
    }

    private func replay(_ client: IMKTextInput) {
        guard !queued.isProcessing, !bypassSecureInput(client) else { return }
        NSLog("0c: replay started (%d requests)", queued.count)
        defer { NSLog("0c: replay ended (%d pending requests)", queued.count) }
        do {
            try queued.drain { item in
                guard !bypassSecureInput(client) else { return }
                let generation = queued.cancellationGeneration
                let next = try session.handle(item.request)
                render(next, to: client)
                guard queued.cancellationGeneration == generation, !queued.isEmpty, !bypassSecureInput(client) else { return }
                if !next.handled, let characters = item.characters, !characters.isEmpty {
                    // Keep this callback inside the queue's processing guard too.
                    queued.present { client.insertText(characters, replacementRange: replacement) }
                }
            }
        } catch EngineError.starting {
            scheduleReplay(client)
        } catch {
            NSLog("0c: replay failed (%@)", String(describing: error))
            dropQueued()
        }
    }

    /// IMKTextInput can insert text but cannot replay a host command. If the
    /// engine is still starting or presenting a known synchronous reply, deliver
    /// the known English text and queued letters, then release the native key.
    @discardableResult
    private func flushQueuedEnglish(to client: IMKTextInput) -> Bool {
        guard (!rendering || queued.isPresenting), !bypassSecureInput(client),
              snapshot?.text.isEmpty != false || snapshot?.englishPending == true,
              let text = queued.englishLiteral(initial: snapshot?.text ?? "",
                                               english: snapshot?.mode == "english",
                                               pending: snapshot?.englishPending == true) else { return false }
        let caps = capsMode, viaMode = capsViaMode
        // Caps Lock 帶來的英文（#219）在新 session 用 caps_lock 還原，關掉 Caps Lock 才會回到開啟前的模式。
        let restore = snapshot?.capsEnglish == true ? Request(type: "caps_lock", on: true) : Request(type: "mode", on: true)
        dropQueued()
        capsMode = caps
        capsViaMode = viaMode
        let generation = queued.cancellationGeneration
        session.reset()
        snapshot = nil
        pendingContext = nil
        panel.orderOut(nil)
        queued.append(restore)
        let wasRendering = rendering
        rendering = true
        if !text.isEmpty {
            let deliver = { client.insertText(text, replacementRange: self.replacement) }
            if queued.isProcessing { queued.present(deliver) }
            else { queued.perform { queued.present(deliver) } }
        }
        rendering = wasRendering
        // A focus/security callback may have invalidated this controller.
        guard queued.cancellationGeneration == generation, !bypassSecureInput(client), !queued.isEmpty else { return true }
        scheduleReplay(client)
        return true
    }

    private func enqueue(_ request: Request, characters: String?, client: IMKTextInput) {
        let wasEmpty = queued.isEmpty
        queued.append(request, characters: characters)
        if wasEmpty { NSLog("0c: requests queued") }
        if !queued.isProcessing && !rendering { scheduleReplay(client) }
    }

    @discardableResult
    private func send(_ request: Request, to client: IMKTextInput, characters: String? = nil) -> Bool {
        if request.type != "toggle" { toggleArmed = false }
        guard !bypassSecureInput(client) else { return false }
        // Enter 只送出英文單字、不交給 App（#235）：排隊中也照順序交給引擎，不先送出再放行。
        let bufferConsumesNamedKey = snapshot?.englishPending == true &&
            (["Backspace", "ShiftBackspace", "Escape", "Enter"].contains(request.key ?? "") ||
             snapshot?.candidates != nil && ["Tab", "Up", "Down"].contains(request.key ?? ""))
        if request.type == "key", characters == nil, !bufferConsumesNamedKey,
           flushQueuedEnglish(to: client) { return false }
        if queued.isProcessing || rendering || !queued.isEmpty {
            // Mode-only startup and a completed commit cannot consume idle
            // host commands. Named keys cannot be restored with insertText.
            if request.type == "key", characters == nil,
               queued.canPassNamedKey(composing: snapshot?.composing == true) { return false }
            // 前面還有暫存的按鍵：排在後面，保持順序。
            enqueue(request, characters: characters, client: client)
            return true
        }
        defer {
            if !queued.isEmpty { scheduleReplay(client) }
        }
        func perform(_ request: Request) throws -> Bool {
            try queued.perform {
                let next = try session.handle(request)
                render(next, to: client)
                return next.handled
            }
        }
        // 引擎在這個欄位還連著時結束了（更新交接、套用設定重啟、當掉，#119）：沒有組字時開新連線重送這個鍵一次，
        // 引擎還沒起來就照冷啟動暫存。舊 session 裡沒有畫面上的東西，這個鍵也沒被處理過，不會重複提交；
        // 英文模式在新 session 先還原。沒這樣做時這個鍵原樣交給 App：句首的「5」加空白變成「5 」，↓ 也開不了候選。
        // 組字中斷線照下面的通則：畫面上的組字保留成文字、按鍵交還 App。
        var restoreMode: Request?
        var reopened = false
        while true {
            do {
                if let mode = restoreMode {
                    _ = try perform(mode)
                    restoreMode = nil
                }
                return try perform(request)
            } catch EngineError.disconnected where !reopened && snapshot?.composing != true {
                reopened = true
                // Caps Lock 帶來的英文（#219）用 caps_lock 還原，關掉 Caps Lock 才會回到開啟前的模式。
                if snapshot?.capsEnglish == true { restoreMode = Request(type: "caps_lock", on: true) }
                else if snapshot?.mode == "english" { restoreMode = Request(type: "mode", on: true) }
                NSLog("0c: engine restarted; reconnecting")
            } catch EngineError.starting {
                if let mode = restoreMode { enqueue(mode, characters: nil, client: client) }
                // 沒有組字、也沒有排隊中的按鍵時，Enter、Backspace、方向鍵這類具名鍵引擎一定不處理：
                // 直接交還 App 照常執行（例如選取文字後按刪除），不暫存、不吞掉。
                if request.type == "key", characters == nil, snapshot?.composing != true { return false }
                enqueue(request, characters: characters, client: client)
                return true
            } catch {
                return fail(error, client: client)
            }
        }
    }

    /// 引擎無法使用：畫面上的組字照原樣留成文字，按鍵交還 App；下一個按鍵開新的 session。
    private func fail(_ error: Error, client: IMKTextInput) -> Bool {
        dropQueued()
        // EngineSession discarded its session. Reapply held Caps Lock to the
        // next one even when there were no queued requests to drop.
        capsMode = false
        capsViaMode = false
        // Preserve the visible composition as literal text on failure, then
        // release the original key. The next key starts a fresh session.
        rendering = true
        if let text = snapshot?.text, !text.isEmpty {
            client.insertText(text, replacementRange: replacement)
        }
        snapshot = nil
        pendingContext = nil
        panel.orderOut(nil)
        rendering = false
        // A callback during the fallback must not resurrect old requests.
        dropQueued()
        NSLog("0c: engine unavailable (%@)", String(describing: error))
        return false
    }

    // 同一個有效矩形提供 x、y。混用 firstRect 的 x 與快取行矩形的 y，會在
    // TextEdit 移動視窗後只移動候選的水平位置；零高度矩形也不能覆蓋有效的行錨點。
    private var lastCaret: NSRect?

    private func caretRect(_ client: IMKTextInput) -> NSRect {
        var line = NSRect.zero
        let index = max(0, (snapshot?.cursor.utf16 ?? 0) - 1)
        _ = client.attributes(forCharacterIndex: index, lineHeightRectangle: &line)
        if !CaretGeometry.valid(line), index != 0 {
            _ = client.attributes(forCharacterIndex: 0, lineHeightRectangle: &line)
        }
        var cursor = NSRect.zero
        let selected = client.selectedRange()
        if selected.location != NSNotFound {
            cursor = client.firstRect(forCharacterRange: NSRange(location: selected.location, length: 0), actualRange: nil)
        }
        guard let caret = CaretGeometry.resolve(cursor: cursor, line: line, screens: NSScreen.screens.map(\.frame))
        else { return lastCaret ?? .zero }
        lastCaret = caret
        return caret
    }

    private func render(_ next: Snapshot, to client: IMKTextInput) {
        guard !bypassSecureInput(client) else { return }
        rendering = true
        defer { rendering = false }
        let generation = queued.cancellationGeneration
        let hadMarkedText = !(snapshot?.text.isEmpty ?? true)
        snapshot = next
        EngineSession.note(next) // 「輸出簡體字」在別處切換後跟上（#169）
        if let commit = next.commit, !commit.isEmpty {
            shownMarked = nil // insertText 取代了 App 裡的組字
            queued.present { client.insertText(commit, replacementRange: replacement) }
            guard queued.cancellationGeneration == generation else { return }
            pendingContext = session.contextEnabled ? next.contextID.map { ($0, commit) } : nil
        }
        let marked = MarkedText.attributed(next.preedit ?? [])
        let shown = MarkedText.Shown(text: marked, cursor: min(max(0, next.cursor.utf16), marked.length))
        if marked.length > 0 || (hadMarkedText && (next.commit ?? "").isEmpty),
           !MarkedText.unchanged(shown, last: shownMarked, clientMarkedRange: { client.markedRange() }) {
            shownMarked = marked.length > 0 ? shown : nil // 先記：送出途中的回呼若重設狀態會清掉它
            queued.present {
                client.setMarkedText(marked, selectionRange: NSRange(location: shown.cursor, length: 0),
                                     replacementRange: replacement)
            }
        }
        guard queued.cancellationGeneration == generation else { return }
        // 提示（加入詞庫、反悔鍵的結果，#123）與標示中的說明跟候選一起畫在同一個面板；只有提示時面板幾秒後自己收起。
        // 讀音提示（#162）也畫在這個面板。
        if next.candidates != nil || next.suggestion != nil || !next.visibleNotices.isEmpty || next.markedText != nil || next.visibleReading != nil {
            let caret = caretRect(client)
            panel.level = NSWindow.Level(rawValue: max(NSWindow.Level.popUpMenu.rawValue, Int(client.windowLevel()) + 1))
            panel.show(next, at: caret)
        } else { panel.orderOut(nil) }
    }
}
