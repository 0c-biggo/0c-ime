import AppKit
import UserNotifications

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let hash = String(repeating: "a", count: 64)
let payload = UpdateNotification.Payload(title: "人工更新", body: "人工資料已準備", action: "finish",
                                          target_version: "0.1.0-dev.43", target_sha256: hash)
let content = UpdateNotification.content(payload)!
precondition(content.categoryIdentifier == "update-ready" && content.userInfo["target_version"] as? String == "0.1.0-dev.43")
let categories = UpdateNotification.categories()
precondition(Set(categories.map { $0.identifier }) == ["update-ready", "update-agent-disabled"], "do not remove the disabled-agent category")
let ready = categories.first { $0.identifier == "update-ready" }!
precondition(ready.actions.map { $0.identifier } == ["finish", "later"] && ready.actions.map { $0.title } == ["立即安裝", "稍後 1 小時"])
precondition(ready.actions.allSatisfy { !$0.options.contains(.foreground) }, "background action must not steal focus")
let expected = UpdateMenuMonitor.finishCommand(version: "0.1.0-dev.43", sha256: hash)!
precondition(UpdateNotification.command(category: "update-ready", action: "finish", userInfo: content.userInfo) == expected)
for action in [UNNotificationDefaultActionIdentifier, UNNotificationDismissActionIdentifier, "unknown"] {
    precondition(UpdateNotification.command(category: "update-ready", action: action, userInfo: content.userInfo) == nil)
}
precondition(UpdateNotification.command(category: "other", action: "finish", userInfo: content.userInfo) == nil)
precondition(UpdateNotification.command(category: "update-ready", action: "finish", userInfo: ["target_version":"0.1.0-dev.43"]) == nil)
for bad in ["", "../unsafe", String(repeating: "a", count: 63)] {
    precondition(UpdateMenuMonitor.finishCommand(version: "0.1.0-dev.43", sha256: bad) == nil)
}
precondition(!UpdateMenuMonitor.allowed(["finish", "-notify"]), "presenter cannot grant unbound consent")
var calls: [[String]] = []
var complete: ((Result<Data, Error>) -> Void)?
let monitor = UpdateMenuMonitor { args, done in calls.append(args); complete = done }
var previews: [[String]] = []
UpdateNotification.handle(category: "update-ready", action: UNNotificationDefaultActionIdentifier,
                          userInfo: content.userInfo, monitor: monitor, preview: { args, _ in previews.append(args) })
RunLoop.current.run(until: Date().addingTimeInterval(0.03))
precondition(previews == [expected] && calls.isEmpty && !monitor.busy, "body click opens the original target without consent")
UpdateNotification.handle(category: "update-ready", action: UNNotificationDefaultActionIdentifier,
                          userInfo: ["target_version": "0.1.0-dev.43"], monitor: monitor, preview: { args, _ in previews.append(args) })
UpdateNotification.handle(category: "other", action: UNNotificationDefaultActionIdentifier,
                          userInfo: content.userInfo, monitor: monitor, preview: { args, _ in previews.append(args) })
RunLoop.current.run(until: Date().addingTimeInterval(0.03))
precondition(previews == [expected] && calls.isEmpty, "invalid/unrelated body click cannot expose an install action")
UpdateNotification.handle(category: "update-ready", action: "later", userInfo: content.userInfo, monitor: monitor)
RunLoop.current.run(until: Date().addingTimeInterval(0.03))
let snooze = UpdateMenuMonitor.snoozeCommand(version: "0.1.0-dev.43", sha256: hash)!
precondition(calls == [snooze] && !calls.contains { $0.first == "finish" }, "later persists the original asset reminder without consent")
complete?(.failure(NSError(domain: "artificial.snooze", code: 1)))
RunLoop.current.run(until: Date().addingTimeInterval(0.03))
calls.removeAll()
UpdateNotification.handle(category: "update-ready", action: "finish", userInfo: content.userInfo, monitor: monitor)
RunLoop.current.run(until: Date().addingTimeInterval(0.03))
precondition(calls == [expected] && monitor.busy, "delegate route must invoke bounded updater command")
UpdateNotification.handle(category: "update-ready", action: "finish", userInfo: content.userInfo, monitor: monitor)
RunLoop.current.run(until: Date().addingTimeInterval(0.03))
precondition(calls.count == 1, "repeat response may not start a second update")
precondition(UpdateNotification.command(category: "update-ready", action: "later", userInfo: ["target_version": "0.1.0-dev.43"]) == nil)
var invalid = payload
invalid.target_sha256 = nil
precondition(UpdateNotification.content(invalid) == nil)
invalid = payload; invalid.action = "apply"
precondition(UpdateNotification.content(invalid) == nil)
print("Passed native notification payload/category/action checks (injected runner; OS delivery not tested)")
