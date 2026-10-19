import AppKit
import CoreServices

// Applications 裡的入口沒有設定資料或版本路徑，只呼叫穩定更新器。
enum SettingsLauncher {
    static func updater(home: URL) -> URL {
        home.appendingPathComponent("Library/Application Support/0c/bin/0c-update")
    }

    static func open(updater: URL) -> String? {
        guard FileManager.default.isExecutableFile(atPath: updater.path) else {
            return "找不到靈犀注音的設定入口。請重新安裝靈犀注音後再試一次。"
        }
        let process = Process()
        process.executableURL = updater
        process.arguments = ["open-settings"]
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = FileHandle.nullDevice
        let errors = Pipe()
        process.standardError = errors
        do { try process.run() } catch {
            return "設定程式無法開啟：\(error.localizedDescription)。請重新安裝靈犀注音。"
        }
        let data = errors.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        guard process.terminationReason == .exit && process.terminationStatus == 0 else {
            let detail = String(data: data.suffix(4096), encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            return detail.isEmpty ? "設定程式沒有啟動。請從輸入法選單開啟「設定…」，或重新安裝靈犀注音。" : detail
        }
        return nil
    }

    static func register(app: URL) -> OSStatus {
        LSRegisterURL(app as CFURL, true)
    }
}

final class SettingsLauncherDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        let updater = SettingsLauncher.updater(home: FileManager.default.homeDirectoryForCurrentUser)
        DispatchQueue.global(qos: .userInitiated).async {
            let error = SettingsLauncher.open(updater: updater)
            DispatchQueue.main.async {
                if let error = error {
                    NSApp.activate(ignoringOtherApps: true)
                    let alert = NSAlert()
                    alert.messageText = "靈犀注音設定無法開啟"
                    alert.informativeText = error
                    alert.addButton(withTitle: "好")
                    alert.runModal()
                }
                NSApp.terminate(nil)
            }
        }
    }
}
