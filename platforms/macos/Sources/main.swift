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
RetireMonitor.shared.start { LingxiInputController.anyComposing() }
HandoverMonitor.shared.start()
UpdateAgentMonitor.shared.start()
UpdateMenuMonitor.shared.start()
ModelStatusMonitor.shared.start()
withExtendedLifetime(server) { application.run() }
