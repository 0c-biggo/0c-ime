import AppKit

// 由安裝器執行已核對的內附啟動器登記 Applications 的副本；不開 Finder 或設定視窗。
if CommandLine.arguments.count == 3 && CommandLine.arguments[1] == "--register-entry" {
    exit(SettingsLauncher.register(app: URL(fileURLWithPath: CommandLine.arguments[2])) == 0 ? 0 : 1)
}

let app = NSApplication.shared
let delegate = SettingsLauncherDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
