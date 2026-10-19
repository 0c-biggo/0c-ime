import AppKit
import UserNotifications

// Go sends only a bounded notification payload on stdin. The App owns both the
// notification identity and its action delegate; no command/path is in userInfo.
enum UpdateNotification {
    static let category = "update-ready"
    struct Payload: Decodable {
        var title: String
        var body: String
        var action: String?
        var target_version: String?
        var target_sha256: String?
    }

    static func categories() -> Set<UNNotificationCategory> {
        let install = UNNotificationAction(identifier: "finish", title: "立即安裝", options: [])
        let later = UNNotificationAction(identifier: "later", title: "稍後 1 小時", options: [])
        let open = UNNotificationAction(identifier: "open", title: "開啟登入項目", options: [.foreground])
        return [UNNotificationCategory(identifier: category, actions: [install, later], intentIdentifiers: [], options: []),
                UNNotificationCategory(identifier: "update-agent-disabled", actions: [open], intentIdentifiers: [], options: [])]
    }

    static func content(_ payload: Payload) -> UNMutableNotificationContent? {
        guard !payload.title.isEmpty, payload.title.utf8.count <= 1024, payload.body.utf8.count <= 8192,
              payload.action == nil || payload.action == "" || payload.action == "finish" else { return nil }
        let result = UNMutableNotificationContent()
        result.title = payload.title
        result.body = payload.body
        if payload.action == "finish" {
            guard let version = payload.target_version, let hash = payload.target_sha256,
                  UpdateMenuMonitor.finishCommand(version: version, sha256: hash) != nil else { return nil }
            result.categoryIdentifier = category
            result.userInfo = ["target_version": version, "target_sha256": hash]
        }
        return result
    }

    static func command(category: String, action: String, userInfo: [AnyHashable: Any]) -> [String]? {
        // Opening/closing does not grant consent; "later" only persists a
        // reminder for the same asset, without invoking an update.
        guard category == Self.category, action == "finish" || action == "later",
              let version = userInfo["target_version"] as? String,
              let hash = userInfo["target_sha256"] as? String else { return nil }
        return action == "finish" ? UpdateMenuMonitor.finishCommand(version: version, sha256: hash) : UpdateMenuMonitor.snoozeCommand(version: version, sha256: hash)
    }

    static func handle(category: String, action: String, userInfo: [AnyHashable: Any], monitor: UpdateMenuMonitor = .shared,
                       preview: @escaping ([String], UpdateMenuMonitor) -> Void = { UpdateNotificationPreview.shared.show(command: $0, monitor: $1) }) {
        if action == UNNotificationDefaultActionIdentifier {
            // Opening the notification is not installation consent. Show the
            // original target with an explicit button; that button uses the
            // same bounded finish command/revalidation as the menu.
            guard let args = command(category: category, action: "finish", userInfo: userInfo) else { return }
            DispatchQueue.main.async { preview(args, monitor) }
            return
        }
        guard let args = command(category: category, action: action, userInfo: userInfo) else { return }
        DispatchQueue.main.async {
            let item = NSMenuItem()
            item.representedObject = args
            monitor.perform(item)
        }
    }

    // A short-lived invocation of this App posts a notification under its own
    // bundle ID. A later action launches/addresses the normal App delegate.
    // Provisional authorization never prompts or steals the typing focus.
    static func run() -> Int32 {
        var data = Data()
        do {
            while let chunk = try FileHandle.standardInput.read(upToCount: 16385 - data.count), !chunk.isEmpty {
                data.append(chunk)
                if data.count > 16384 { return 2 }
            }
        } catch { return 2 }
        guard data.count <= 16384,
              let payload = try? JSONDecoder().decode(Payload.self, from: data),
              let content = content(payload) else { return 2 }
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        let center = UNUserNotificationCenter.current()
        center.setNotificationCategories(categories())
        var result: Int32?
        center.requestAuthorization(options: [.alert, .provisional]) { granted, error in
            guard granted, error == nil else { DispatchQueue.main.async { result = 3 }; return }
            let id = content.categoryIdentifier.isEmpty ? "update-status" : "update-ready-" + (payload.target_version ?? "")
            center.add(UNNotificationRequest(identifier: id, content: content, trigger: nil)) { error in
                DispatchQueue.main.async { result = error == nil ? 0 : 1 }
            }
        }
        let deadline = Date().addingTimeInterval(15)
        while result == nil && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.05)) }
        return result ?? 1
    }
}

// Only a user-initiated body click opens this panel. Posting a notification and
// background button actions never create a window or activate the App.
private final class UpdateNotificationPreview: NSObject {
    static let shared = UpdateNotificationPreview()
    private var panel: NSPanel?
    private var command: [String]?
    private var monitor: UpdateMenuMonitor?

    func show(command: [String], monitor: UpdateMenuMonitor) {
        guard UpdateMenuMonitor.allowed(command), command.first == "finish" else { return }
        self.command = command
        self.monitor = monitor
        let panel = self.panel ?? NSPanel(contentRect: NSRect(x: 0, y: 0, width: 480, height: 230),
                                         styleMask: [.titled, .closable], backing: .buffered, defer: false)
        panel.title = "靈犀注音更新"
        panel.isReleasedWhenClosed = false
        let view = NSView(frame: NSRect(x: 0, y: 0, width: 480, height: 230))
        let text = NSTextField(wrappingLabelWithString: "通知中的更新版本：\(command[3])\n\n按下『立即安裝』後會重新確認這個版本是否仍可用。請先完成目前組字；更新會等到安全時才執行。")
        text.frame = NSRect(x: 24, y: 82, width: 432, height: 125)
        view.addSubview(text)
        let install = NSButton(title: "立即安裝 \(command[3])", target: self, action: #selector(install))
        install.bezelStyle = .rounded
        install.frame = NSRect(x: 220, y: 24, width: 236, height: 36)
        install.isEnabled = !monitor.busy
        view.addSubview(install)
        let later = NSButton(title: "稍後 1 小時", target: self, action: #selector(snooze))
        later.bezelStyle = .rounded
        later.frame = NSRect(x: 80, y: 24, width: 124, height: 36)
        view.addSubview(later)
        panel.contentView = view
        if self.panel == nil { panel.center() }
        self.panel = panel
        panel.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    @objc private func install() {
        guard let command = command, let monitor = monitor, !monitor.busy else { return }
        let item = NSMenuItem()
        item.representedObject = command
        monitor.perform(item)
        close()
    }

    @objc private func close() {
        panel?.orderOut(nil)
        command = nil
        monitor = nil
    }

    @objc private func snooze() {
        guard let command = command, let monitor = monitor, !monitor.busy,
              let args = UpdateMenuMonitor.snoozeCommand(version: command[3], sha256: command[5]) else { return }
        let item = NSMenuItem()
        item.representedObject = args
        monitor.perform(item)
        close()
    }
}
