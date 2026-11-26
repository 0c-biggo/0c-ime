import AppKit
import Carbon
import IOKit

/// 安全輸入被背景程式卡住時，在螢幕上方跳一個不搶焦點的小視窗說明怎麼恢復（#349，判斷與文字見 SecureInputWatch）。
/// 開啟時的前景 App 只當線索寫出（「開啟時在用的是 X」），不說是它卡住的。
///
/// 放在輸入法外殼：安全輸入開著時靈犀只是被系統停用，這個行程照常在跑（#312、#342 的匯出狀態裡 PID 不變、
/// 仍有 activateServer），可以自己畫視窗，不需要通知權限，也不用等更新排程（15 分鐘一次、短命的行程）。
/// 用面板不用系統通知：通知要授權，provisional 授權只會安靜地進通知中心，卡住當下看不到。
/// 面板不啟用 App、不拿鍵盤焦點（使用者可能正在別的地方打字），放掉之後自己收起。
final class SecureInputMonitor: NSObject {
    static let shared = SecureInputMonitor()
    static let interval: TimeInterval = 30

    private var watch = SecureInputWatch()
    private var timer: Timer?
    private var panel: NSPanel?
    private var sleeping = false
    private var screenSaver = false
    private var switchedOut = false

    func start() {
        guard timer == nil else { return }
        let workspace = NSWorkspace.shared.notificationCenter
        let distributed = DistributedNotificationCenter.default()
        let flags: [(NotificationCenter, String, ReferenceWritableKeyPath<SecureInputMonitor, Bool>, Bool)] = [
            (workspace, NSWorkspace.willSleepNotification.rawValue, \.sleeping, true),
            (workspace, NSWorkspace.didWakeNotification.rawValue, \.sleeping, false),
            (workspace, NSWorkspace.sessionDidResignActiveNotification.rawValue, \.switchedOut, true),
            (workspace, NSWorkspace.sessionDidBecomeActiveNotification.rawValue, \.switchedOut, false),
            (distributed, "com.apple.screensaver.didstart", \.screenSaver, true),
            (distributed, "com.apple.screensaver.didstop", \.screenSaver, false),
        ]
        for (center, name, flag, value) in flags {
            center.addObserver(forName: Notification.Name(name), object: nil, queue: .main) { [weak self] _ in
                self?[keyPath: flag] = value
            }
        }
        let timer = Timer(timeInterval: Self.interval, repeats: true) { [weak self] _ in self?.check() }
        timer.tolerance = 10
        RunLoop.main.add(timer, forMode: .common)
        self.timer = timer
    }

    func check() {
        let secure = IsSecureEventInputEnabled()
        var observation = SecureInputWatch.Observation(secure: secure, paused: paused())
        if secure, !observation.paused, let pid = Self.holderPID() {
            observation.holder = pid
            observation.holderBundle = NSRunningApplication(processIdentifier: pid)?.bundleIdentifier
            let front = NSWorkspace.shared.frontmostApplication
            observation.front = front?.processIdentifier
            observation.frontBundle = front?.bundleIdentifier
        }
        if !secure { close() }
        switch watch.check(observation, at: Date()) {
        case .stuck(let warn):
            NSLog("0c: secure input stays on in the background%@", warn ? "; showing how to recover" : "")
            ShellStats.shared.noteSecureInputStuck(warned: warn)
            if warn {
                let app = observation.holder.flatMap { NSRunningApplication(processIdentifier: $0) }
                show(SecureInputWatch.message(SecureInputWatch.clue(bundle: app?.bundleIdentifier, name: app?.localizedName)))
            }
        case .released:
            NSLog("0c: secure input released")
            ShellStats.shared.noteSecureInputReleased()
        case .none:
            break
        }
    }

    /// 鎖定每次直接問 CGSession（CGSSessionScreenIsLocked）：不靠通知，外殼在鎖定中才啟動、或漏了解鎖通知時都不會卡在暫停。
    /// 切換使用者也再問一次 kCGSSessionOnConsoleKey。
    private func paused() -> Bool {
        if sleeping || screenSaver || switchedOut { return true }
        guard let session = CGSessionCopyCurrentDictionary() as? [String: Any] else { return false }
        if session["CGSSessionScreenIsLocked"] as? Bool == true { return true }
        return session["kCGSSessionOnConsoleKey"] as? Bool == false
    }

    /// IORegistry 根節點 IOConsoleUsers 裡這個使用者的 kCGSSessionSecureInputPID（同 0c-update 匯出狀態的 ioreg）。
    static func holderPID() -> Int32? {
        let root = IORegistryGetRootEntry(kIOMainPortDefault)
        guard root != 0 else { return nil }
        defer { IOObjectRelease(root) }
        guard let users = IORegistryEntryCreateCFProperty(root, "IOConsoleUsers" as CFString, kCFAllocatorDefault, 0)?
            .takeRetainedValue() as? [[String: Any]] else { return nil }
        let uid = Int(getuid())
        for user in users where (user["kCGSSessionUserIDKey"] as? Int) == uid {
            if let pid = user["kCGSSessionSecureInputPID"] as? Int, pid > 0 { return Int32(pid) }
        }
        return nil
    }

    private func show(_ message: String) {
        panel?.orderOut(nil)
        let panel = makePanel(message)
        self.panel = panel
        let screen = NSScreen.main ?? NSScreen.screens.first
        // 上方置中：右上角是通知橫幅的位置，會被蓋住（.15 實測）。
        if let area = screen?.visibleFrame {
            panel.setFrameOrigin(NSPoint(x: area.midX - panel.frame.width / 2, y: area.maxY - panel.frame.height - 24))
        }
        CandidatePanel.revealApp()
        panel.orderFrontRegardless()
    }

    @objc private func close() {
        panel?.orderOut(nil)
    }

    private func makePanel(_ message: String) -> NSPanel {
        let width: CGFloat = 380
        let panel = NSPanel(contentRect: NSRect(x: 0, y: 0, width: width, height: 200),
                            styleMask: [.titled, .closable, .nonactivatingPanel], backing: .buffered, defer: true)
        panel.title = "靈犀注音"
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.becomesKeyOnlyIfNeeded = true
        panel.isReleasedWhenClosed = false
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]

        let heading = NSTextField(labelWithString: SecureInputWatch.title)
        heading.font = .boldSystemFont(ofSize: NSFont.systemFontSize + 1)
        let body = NSTextField(wrappingLabelWithString: message)
        body.preferredMaxLayoutWidth = width - 40
        body.isSelectable = false
        let button = NSButton(title: "知道了", target: self, action: #selector(close))
        button.bezelStyle = .rounded
        button.keyEquivalent = ""
        let row = NSStackView(views: [NSView(), button])
        row.orientation = .horizontal
        let stack = NSStackView(views: [heading, body, row])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 10
        stack.edgeInsets = NSEdgeInsets(top: 16, left: 20, bottom: 16, right: 20)
        stack.translatesAutoresizingMaskIntoConstraints = false
        row.translatesAutoresizingMaskIntoConstraints = false
        let content = NSView()
        content.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            stack.topAnchor.constraint(equalTo: content.topAnchor),
            stack.bottomAnchor.constraint(equalTo: content.bottomAnchor),
            stack.widthAnchor.constraint(equalToConstant: width),
            row.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -40),
        ])
        panel.contentView = content
        panel.setContentSize(content.fittingSize)
        return panel
    }
}
