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
precondition(Set(categories.map { $0.identifier }) == ["update-ready", "update-found", "update-agent-disabled"], "do not remove the disabled-agent category")
// #340 手動更新查到新版：「立即下載」＋「稍後 1 小時」，都在背景執行、不搶焦點。
let found = categories.first { $0.identifier == "update-found" }!
precondition(found.actions.map { $0.identifier } == ["download", "later"] && found.actions.map { $0.title } == ["立即下載", "稍後 1 小時"])
precondition(found.actions.allSatisfy { !$0.options.contains(.foreground) }, "download action must not steal focus")
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
// #340：download 通知綁定版本，按「立即下載」只執行 download-now；點內文開設定程式的更新頁，不下載。
do {
    var downloadPayload = payload
    downloadPayload.action = "download"
    let downloadContent = UpdateNotification.content(downloadPayload)!
    precondition(downloadContent.categoryIdentifier == "update-found" && downloadContent.userInfo["target_sha256"] as? String == hash)
    let bound = UpdateMenuMonitor.downloadCommand(version: "0.1.0-dev.43", sha256: hash)!
    precondition(bound == ["download-now", "-notify", "-target", "0.1.0-dev.43", "-target-sha256", hash] && UpdateMenuMonitor.allowed(bound))
    precondition(UpdateNotification.command(category: "update-found", action: "download", userInfo: downloadContent.userInfo) == bound)
    precondition(UpdateNotification.command(category: "update-found", action: "finish", userInfo: downloadContent.userInfo) == nil, "a download notice never installs")
    precondition(UpdateNotification.command(category: "update-ready", action: "download", userInfo: content.userInfo) == nil)
    precondition(UpdateNotification.command(category: "update-found", action: "later", userInfo: downloadContent.userInfo) == UpdateMenuMonitor.snoozeCommand(version: "0.1.0-dev.43", sha256: hash))
    var foundCalls: [[String]] = []
    var foundDone: ((Result<Data, Error>) -> Void)?
    let foundMonitor = UpdateMenuMonitor { args, done in foundCalls.append(args); foundDone = done }
    var foundPreviews: [[String]] = []
    UpdateNotification.handle(category: "update-found", action: UNNotificationDefaultActionIdentifier, userInfo: downloadContent.userInfo,
                              monitor: foundMonitor, preview: { args, _ in foundPreviews.append(args) })
    RunLoop.current.run(until: Date().addingTimeInterval(0.03))
    precondition(foundCalls == [["open-settings", "update"]] && foundPreviews.isEmpty && !foundMonitor.busy, "body click opens the update page, does not download")
    foundDone?(.success(Data())); RunLoop.current.run(until: Date().addingTimeInterval(0.03))
    UpdateNotification.handle(category: "update-found", action: "download", userInfo: downloadContent.userInfo, monitor: foundMonitor)
    RunLoop.current.run(until: Date().addingTimeInterval(0.03))
    precondition(foundCalls.last == bound && foundMonitor.busy, "download button runs the bound download")
}
print("Passed native notification payload/category/action checks (injected runner; OS delivery not tested)")
