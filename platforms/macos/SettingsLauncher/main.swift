import AppKit

// 由安裝器執行已核對的內附啟動器登記 Applications 的副本；不開 Finder 或設定視窗。
// 桌面掛著沒人回答的系統詢問時 LSRegisterURL 會一直等（#353）。新的 0c-update 呼叫時自己有時限；背景更新由舊版的
// 更新器安裝、呼叫時沒有時限，所以這裡也自己設上限，逾時以結束碼 3 結束，入口留給之後的排程補登記。
if CommandLine.arguments.count == 3 && CommandLine.arguments[1] == "--register-entry" {
    DispatchQueue.global().asyncAfter(deadline: .now() + SettingsLauncher.registerDeadline) {
        FileHandle.standardError.write(Data("登記設定程式入口超過 \(Int(SettingsLauncher.registerDeadline)) 秒沒有完成，先結束\n".utf8))
        exit(3)
    }
    exit(SettingsLauncher.register(app: URL(fileURLWithPath: CommandLine.arguments[2])) == 0 ? 0 : 1)
}

let app = NSApplication.shared
let delegate = SettingsLauncherDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
