import Carbon
import Foundation

// 安裝與回復時由 0c-update 呼叫（0c --activate-input-source <App 路徑>）：登記該 App 的輸入來源並啟用，
// 停用靈犀注音其他版本留下的來源。0.1.0-dev.21 以前的 App 是 org.zuin.inputmethod.Zuin，之後是 com.biggo.inputmethod.0c；
// 0.1.0-dev.59 之後的 App 另宣告輸入模式 com.biggo.inputmethod.0c.Bopomofo（#87），要啟用父項與模式；
// 模式的預設狀態是未啟用，必須在這裡明確啟用，系統才會記進啟用清單（見 Info.plist 的說明）。
enum InputSourceSetup {
    static func sources(includeAll: Bool) -> [TISInputSource] {
        guard let list = TISCreateInputSourceList(nil, includeAll)?.takeRetainedValue() as? [TISInputSource] else { return [] }
        return list
    }

    static func string(_ source: TISInputSource, _ key: CFString) -> String? {
        guard let raw = TISGetInputSourceProperty(source, key) else { return nil }
        return Unmanaged<CFString>.fromOpaque(raw).takeUnretainedValue() as String
    }

    static func bool(_ source: TISInputSource, _ key: CFString) -> Bool {
        guard let raw = TISGetInputSourceProperty(source, key) else { return false }
        return CFBooleanGetValue(Unmanaged<CFBoolean>.fromOpaque(raw).takeUnretainedValue())
    }

    static func plan(_ appPath: String) -> InputSourcePlan? {
        guard let bundle = Bundle(url: URL(fileURLWithPath: appPath)), let id = bundle.bundleIdentifier,
              let info = bundle.infoDictionary else { return nil }
        return InputSourcePlan(bundle: id, info: info)
    }

    static func error(_ message: String) {
        FileHandle.standardError.write("\(message)\n".data(using: .utf8)!)
    }

    // 回傳行程結束碼：0 成功，1 參數或 App 錯誤，2 登記或啟用失敗。
    // force：簽章身分改變（例如 ad-hoc 換成 Developer ID）時，系統會默默撤銷原本的核准但仍顯示已啟用；
    // 必須再呼叫一次啟用，系統才會跳出核准詢問。
    static func activate(appPath: String, force: Bool = false) -> Int32 {
        let url = URL(fileURLWithPath: appPath)
        guard let plan = plan(appPath) else {
            error("不是靈犀注音的 App：\(appPath)")
            return 1
        }
        var failure: String?
        // 第一輪依本行程讀到的狀態啟用；之後只補啟用子行程回報沒啟用的來源，
        // 避免對已核准的來源重新啟用而再跳一次核准詢問。
        var pending: Set<String>?
        let round = InputSourceLookup.confirm(
            enable: { round in
                failure = enable(url, plan, only: pending, force: force && round == 1)
                return failure == nil
            },
            confirmed: {
                pending = confirm(appPath)
                return pending?.isEmpty == true
            },
            wait: { Thread.sleep(forTimeInterval: $0) }
        )
        guard round != nil else {
            if let failure {
                error(failure)
            } else {
                error("已要求啟用 \(plan.selectable)，但系統尚未啟用（可能在等「允許」，或要重新登入後才登記新的輸入模式）；登入後會再試。")
            }
            return 2
        }
        // 新來源可用後才停用舊來源；登記、查詢或啟用失敗時保留使用者原有的輸入方式。
        for old in sources(includeAll: true) {
            guard let id = string(old, kTISPropertyInputSourceID),
                  plan.stale(id: id, bundle: string(old, kTISPropertyBundleID)) else { continue }
            _ = TISDisableInputSource(old)
        }
        print("已啟用 \(plan.selectable)")
        return 0
    }

    // 登記 App，依序啟用父項與模式；回傳錯誤訊息，成功為 nil。only 為 nil 時啟用所有尚未啟用的來源。
    static func enable(_ url: URL, _ plan: InputSourcePlan, only: Set<String>?, force: Bool) -> String? {
        let registered = TISRegisterInputSource(url as CFURL)
        if registered != noErr {
            return "無法登記輸入來源（\(registered)）"
        }
        for id in plan.enabled {
            guard let source = InputSourceLookup.find(
                list: { sources(includeAll: true) },
                matches: { string($0, kTISPropertyInputSourceID) == id },
                wait: { Thread.sleep(forTimeInterval: $0) },
                missing: { attempt in error("輸入來源登記回傳成功，但清單尚未包含 \(id)（第 \(attempt)/3 次查詢）") }
            ) else {
                return "重查兩次後仍找不到輸入來源 \(id)；請登出再登入後確認。"
            }
            // 已啟用就不再呼叫：對已核准的輸入來源重新啟用，會讓系統再跳一次核准詢問。
            let needed = only.map { $0.contains(id) } ?? !bool(source, kTISPropertyInputSourceIsEnabled)
            if (force && id == plan.parent) || needed {
                let status = TISEnableInputSource(source)
                if status != noErr {
                    return "無法啟用 \(id)（\(status)）；請在系統設定確認是否允許。"
                }
            }
        }
        return nil
    }

    // TIS 在行程內快取輸入來源的狀態，本行程重查清單仍是舊值；在新的子行程回讀，回傳尚未啟用的可選取來源。
    // 只看可選取的來源：有模式時父項要等使用者在系統詢問中允許後才顯示為啟用（macOS 26.6.2 實測），模式則立即生效。
    // 子行程讀不到結果時當成未啟用。
    static func confirm(_ appPath: String) -> Set<String> {
        let process = Process()
        process.executableURL = Bundle.main.executableURL ?? URL(fileURLWithPath: CommandLine.arguments[0])
        process.arguments = ["--confirm-input-source", appPath]
        let output = Pipe()
        process.standardOutput = output
        guard let plan = plan(appPath) else { return [] }
        guard (try? process.run()) != nil else { return [plan.selectable] }
        let data = output.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        var pending = Set<String>()
        for line in String(decoding: data, as: UTF8.self).split(separator: "\n") {
            let fields = line.split(separator: " ")
            if fields.count == 2, fields[0] == plan.selectable, fields[1] != "enabled", fields[1] != "selected" { pending.insert(plan.selectable) }
        }
        return process.terminationStatus == 0 ? pending : [plan.selectable]
    }

    // 子行程（0c --confirm-input-source <App 路徑>）：逐行輸出「<ID> enabled|disabled|missing」。
    // 可選取的來源已啟用時，若目前選取的是靈犀注音的其他來源（例如升級前的父項），改選這一版的來源，再輸出「<ID> selected」。
    static func reportState(appPath: String) -> Int32 {
        guard let plan = plan(appPath) else { return 1 }
        let list = sources(includeAll: true)
        var ready = false
        for id in plan.enabled {
            let source = list.first { string($0, kTISPropertyInputSourceID) == id }
            let enabled = source.map { bool($0, kTISPropertyInputSourceIsEnabled) } ?? false
            if id == plan.selectable { ready = enabled }
            print("\(id) \(source == nil ? "missing" : enabled ? "enabled" : "disabled")")
        }
        guard ready else { return 0 }
        let current = TISCopyCurrentKeyboardInputSource().takeRetainedValue()
        if plan.shouldSelect(currentID: string(current, kTISPropertyInputSourceID), currentBundle: string(current, kTISPropertyBundleID)),
           let source = list.first(where: { string($0, kTISPropertyInputSourceID) == plan.selectable }),
           TISSelectInputSource(source) == noErr {
            print("\(plan.selectable) selected")
        }
        return 0
    }
}
