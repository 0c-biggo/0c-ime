import AppKit

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
func pump() { RunLoop.current.run(until: Date().addingTimeInterval(0.03)) }
var calls: [[String]] = []
var completion: ((Result<Data, Error>) -> Void)?
let monitor = UpdateMenuMonitor { args, done in calls.append(args); completion = done }
final class Target: NSObject {
    let monitor: UpdateMenuMonitor
    init(_ monitor: UpdateMenuMonitor) { self.monitor = monitor }
    @objc func invoke(_ item: NSMenuItem) { monitor.perform(item) }
}
let target = Target(monitor)
func menu() -> NSMenu {
    let result = NSMenu()
    result.autoenablesItems = false
    monitor.append(to: result, target: target, action: #selector(Target.invoke(_:)))
    return result
}
func item(_ title: String) -> NSMenuItem { menu().items.first { $0.title == title }! }
func statusLine(_ menu: NSMenu) -> String? { menu.items.first { $0.title != "設定…" && !$0.isSeparatorItem }?.title }
func receive(_ json: String) { completion?(.success(Data(json.utf8))); pump() }
monitor.refresh()
let loading = menu()
precondition(loading.items.first?.title == "設定…" && loading.items[1].isSeparatorItem && statusLine(loading) == "更新狀態待確認" && calls == [["status", "-json"]], "menu must return while status query is unfinished")
monitor.refresh()
precondition(calls.count == 1, "status queries must not overlap")
// #148：系統每次組字更新後都會要選單（一個按鍵一次）；選單觸發的查詢要節流，不能每個按鍵都開一次 0c-update。
do {
    var throttledCalls = 0
    var finish: ((Result<Data, Error>) -> Void)?
    let throttled = UpdateMenuMonitor { _, done in throttledCalls += 1; finish = done }
    throttled.refreshForMenu(now: 100)
    finish?(.success(Data("{}".utf8))); pump()
    throttled.refreshForMenu(now: 100.5)
    precondition(throttledCalls == 1, "a menu request right after the last query must reuse the cached status")
    throttled.refreshForMenu(now: 100 + UpdateMenuMonitor.menuRefreshInterval)
    precondition(throttledCalls == 2, "the menu refreshes again once the cached status is old enough")
}
receive(#"{"installed":"0.1.0-dev.40","mode":"ask","stage":"prepared","target":{"version":"0.1.0-dev.41","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","kind":"shell"},"blocked":{"reason":"needs_consent"},"action":"finish"}"#)
precondition(item("下載後詢問").state == .on && item("自動更新").state == .off, "show actual persisted mode")
let install = item("立即安裝 0.1.0-dev.41")
precondition(app.sendAction(install.action!, to: install.target, from: install), "NSMenu action must reach its target")
precondition(calls.last == UpdateMenuMonitor.finishCommand(version: "0.1.0-dev.41", sha256: String(repeating: "a", count: 64)) && monitor.busy, "finish must invoke updater and mark busy")
precondition(menu().items.filter { $0.action != nil && $0.title != "設定…" }.allSatisfy { !$0.isEnabled } && item("設定…").isEnabled, "disable update commands while update runs, but keep the settings entry")
let updateDone = completion
let settingsDuringUpdate = calls.count
let settingsItem = item("設定…")
precondition(app.sendAction(settingsItem.action!, to: settingsItem.target, from: settingsItem), "settings entry must reach its target")
precondition(calls.count == settingsDuringUpdate + 1 && calls.last == ["open-settings"], "settings entry runs only open-settings")
monitor.perform(settingsItem)
precondition(calls.count == settingsDuringUpdate + 1, "a second click must not start another open while one is pending")
completion?(.failure(NSError(domain: "test", code: 1))); pump()
precondition(!menu().items.contains { $0.title == "更新操作未完成，請稍後再試" }, "a settings failure is not an update failure")
monitor.perform(settingsItem)
precondition(calls.count == settingsDuringUpdate + 2, "settings can be opened again after the first finished")
completion?(.success(Data())); pump()
precondition(monitor.busy, "opening settings must not clear or set the update busy state")
let count = calls.count
monitor.perform(install)
monitor.refresh()
precondition(calls.count == count, "neither another click nor refresh may overlap an update")
updateDone?(.success(Data())); pump()
precondition(calls.last == ["status", "-json"], "reload status after command completes")
receive(#"{"installed":"0.1.0-dev.41","mode":"ask","stage":"pending","blocked":{"reason":"needs_relogin"},"paused_until":"2026-10-10T00:00:00Z"}"#)
precondition(statusLine(menu())!.contains("重新登入後生效") && !menu().items.contains { $0.title.hasPrefix("立即安裝") }, "installed pending runtime must not be presented as active or finishable")
// #77：正在執行的外殼已是目標版本時不再要求重新登入；其他版本或開發建置照舊提示。
let relogin = try! JSONDecoder().decode(UpdateMenuStatus.self, from: Data(#"{"installed":"0.1.0-dev.59","stage":"pending","target":{"version":"0.1.0-dev.59"},"blocked":{"reason":"needs_relogin"}}"#.utf8))
precondition(relogin.label(shell: "0.1.0-dev.59") == "靈犀注音 0.1.0-dev.59", "running target shell is already active")
precondition(relogin.label(shell: "0.1.0-dev.58").contains("重新登入後生效") && relogin.label(shell: nil).contains("重新登入後生效"), "older or unknown shell still asks for a new login")
let resume = item("恢復更新")
app.sendAction(resume.action!, to: resume.target, from: resume)
precondition(calls.last == ["resume"], "resume invokes only resume")
completion?(.success(Data())); pump()
receive(#"{"installed":"0.1.0-dev.41","mode":"manual","stage":"idle"}"#)
precondition(item("手動更新").state == .on && item("暫停更新 7 天").representedObject as? [String] == ["pause", "7"], "manual mode and pause remain accessible")
// IMKit 把選取的項目包在 infoDictionary 裡傳給動作，直接當 NSMenuItem 用會什麼都不做。
let picked = item("檢查更新")
precondition(UpdateMenuMonitor.selectedItem(from: ["IMKCommandMenuItem": picked, "IMKMenuTitle": "x"] as [String: Any]) === picked, "unwrap IMKit's infoDictionary")
precondition(UpdateMenuMonitor.selectedItem(from: picked) === picked && UpdateMenuMonitor.selectedItem(from: nil) == nil
             && UpdateMenuMonitor.selectedItem(from: ["IMKCommandMenuItem": "not an item"] as [String: Any]) == nil
             && UpdateMenuMonitor.selectedItem(from: ["other": picked] as [String: Any]) == nil, "only a real menu item is accepted")
let bad = NSMenuItem()
bad.representedObject = ["apply", "-archive", "/private/tmp/untrusted"]
let before = calls.count
monitor.perform(bad)
precondition(calls.count == before, "reject commands outside menu whitelist")
monitor.refresh()
receive("not-json")
precondition(monitor.failed && monitor.status == nil && !menu().items.contains { $0.title.hasPrefix("立即安裝") }, "invalid status clears stale install action")
// #125：「我的詞彙…」只在 features.vocab 為真時出現在「設定…」下面，執行 open-settings vocab。
for json in [#"{"installed":"0.1.0-dev.62","mode":"auto"}"#, #"{"installed":"x","features":{"vocab":false}}"#,
             #"{"installed":"x","features":{"vocab":"yes"}}"#, #"{"installed":"x","features":true}"#] {
    monitor.refresh(); receive(json)
    precondition(monitor.status?.installed != nil && !menu().items.contains { $0.title == "我的詞彙…" }, "no vocab entry without features.vocab: \(json)")
}
monitor.refresh()
receive(#"{"installed":"0.2.0-dev.1","mode":"auto","stage":"idle","features":{"vocab":true}}"#)
let withVocab = menu()
precondition(withVocab.items[0].title == "設定…" && withVocab.items[1].title == "我的詞彙…" && withVocab.items[2].isSeparatorItem, "vocab entry sits right below settings")
let vocabItem = item("我的詞彙…")
precondition(vocabItem.representedObject as? [String] == ["open-settings", "vocab"] && UpdateMenuMonitor.allowed(["open-settings", "vocab"]) && !UpdateMenuMonitor.allowed(["open-settings", "about"]), "vocab entry runs only open-settings vocab")
precondition(UpdateMenuMonitor.selectedItem(from: ["IMKCommandMenuItem": vocabItem, "IMKMenuTitle": "我的詞彙…"] as [String: Any]) === vocabItem, "IMKit infoDictionary carries the vocab item")
precondition(app.sendAction(vocabItem.action!, to: vocabItem.target, from: vocabItem) && calls.last == ["open-settings", "vocab"] && !monitor.busy, "vocab entry opens the vocab page without marking an update busy")
completion?(.success(Data())); pump()
// 更新進行中也能按。
let installReady = #"{"installed":"0.2.0-dev.1","mode":"ask","stage":"prepared","target":{"version":"0.2.0-dev.2","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"action":"finish","features":{"vocab":true}}"#
monitor.refresh(); receive(installReady)
let finishItem = item("立即安裝 0.2.0-dev.2")
monitor.perform(finishItem)
precondition(monitor.busy && item("我的詞彙…").isEnabled && !item("檢查更新").isEnabled, "vocab entry stays usable while an update runs")
let beforeVocab = calls.count
monitor.perform(item("我的詞彙…"))
precondition(calls.count == beforeVocab + 1 && calls.last == ["open-settings", "vocab"], "vocab opens during an update")
print("Passed 31 native update menu checks (injected runner; not full update E2E)")

// Optional explicit artificial artifact generated by the real Go status mapper.
// This covers the JSON boundary that hand-written presenter fixtures missed.
if CommandLine.arguments.count == 2 || (CommandLine.arguments.count == 3 && CommandLine.arguments[2] == "--expect-no-install") {
    let path = CommandLine.arguments[1]
    precondition(path.hasPrefix("/private/tmp/0c-"))
    let data = try! Data(contentsOf: URL(fileURLWithPath: path))
    let status = try! JSONDecoder().decode(UpdateMenuStatus.self, from: data)
    var bridgeCalls: [[String]] = []
    let bridge = UpdateMenuMonitor { args, done in bridgeCalls.append(args); if args == ["status", "-json"] { done(.success(data)) } }
    let bridgeTarget = Target(bridge)
    bridge.refresh(); pump()
    let native = NSMenu(); native.autoenablesItems = false
    bridge.append(to: native, target: bridgeTarget, action: #selector(Target.invoke(_:)))
    let entry = native.items.first { $0.title == "立即安裝 0.1.0-dev.41" }
    if CommandLine.arguments.count == 3 {
        precondition(entry == nil && bridgeCalls == [["status", "-json"]], "missing asset hash must reproduce the absent install entry")
        print("Confirmed old missing-SHA JSON suppresses the install entry (negative control)")
    } else {
        let command = entry?.representedObject as? [String]
        precondition(command == UpdateMenuMonitor.finishCommand(version: status.target!.version, sha256: status.target!.sha256!), "real Go JSON must yield the bounded native menu action")
        precondition(native.items.contains { $0.title.hasPrefix("更新提醒稍後至 ") }, "persisted reminder time must be visible")
        precondition(app.sendAction(entry!.action!, to: entry!.target, from: entry))
        precondition(bridgeCalls.last == command && bridge.busy, "real JSON entry must invoke the same original asset action")
        print("Passed real Go status JSON → native asset-bound menu/reminder/action integration (artificial state)")
    }
}
