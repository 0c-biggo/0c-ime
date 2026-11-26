import AppKit
import InputMethodKit

let arguments = CommandLine.arguments
if (arguments.count == 3 || (arguments.count == 4 && arguments[3] == "--force")), arguments[1] == "--activate-input-source" {
    exit(InputSourceSetup.activate(appPath: arguments[2], force: arguments.count == 4))
}
if arguments.count == 3, arguments[1] == "--confirm-input-source" {
    exit(InputSourceSetup.reportState(appPath: arguments[2]))
}
if arguments.count == 3, arguments[1] == "--query-input-source" {
    exit(InputSourceSetup.reportState(appPath: arguments[2], select: false))
}
if arguments.count == 3, arguments[1] == "--inspect-input-source" {
    exit(InputSourceSetup.inspect(appPath: arguments[2]))
}
if arguments.count == 3, arguments[1] == "--update-agent" {
    exit(UpdateAgent.run(arguments[2]))
}
if arguments.count == 2, arguments[1] == "--update-notice" {
    exit(UpdateNotification.run())
}

let application = NSApplication.shared
application.setActivationPolicy(.accessory)
let bundle = Bundle.main
guard let name = bundle.object(forInfoDictionaryKey: "InputMethodConnectionName") as? String,
      let identifier = bundle.bundleIdentifier,
      let server = IMKServer(name: name, bundleIdentifier: identifier) else {
    fatalError("Cannot initialize 0c InputMethodKit server")
}
// #194：以 open -j 隱藏啟動時整個 App 是隱藏的，候選窗不會上螢幕；啟動完成就解除隱藏（不搶前景）。
NotificationCenter.default.addObserver(forName: NSApplication.didFinishLaunchingNotification, object: application, queue: .main) { _ in
    CandidatePanel.revealApp(application)
}
// 診斷計數（#266）：睡眠、喚醒的時間，以及喚醒後有沒有按鍵進到輸入法。
let workspaceEvents = NSWorkspace.shared.notificationCenter
workspaceEvents.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) { _ in ShellStats.shared.noteWake() }
workspaceEvents.addObserver(forName: NSWorkspace.willSleepNotification, object: nil, queue: .main) { _ in ShellStats.shared.noteSleep() }
ShellStats.shared.write()
RetireMonitor.shared.start { LingxiInputController.anyComposing() }
HandoverMonitor.shared.start()
UpdateAgentMonitor.shared.start()
// #350：使用者在用靈犀、剛停手且沒有組字時正式詢問一次通知權限（之後更新通知以橫幅出現）。
NotificationPermissionMonitor.shared.start(composing: { LingxiInputController.anyComposing() },
                                           lastKey: { ShellStats.shared.counts.lastKey },
                                           servedHere: { HandoverMonitor.shared.atLaunchPath })
UpdateMenuMonitor.shared.start()
ModelStatusMonitor.shared.start()
SecureInputMonitor.shared.start() // #349：安全輸入被背景程式卡住時提醒怎麼恢復
withExtendedLifetime(server) { application.run() }
