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
        // 系統啟動「系統設定」或子行程卡住時也要結束，不讓安裝程式與排程一直等（#238）。
        DispatchQueue.global().asyncAfter(deadline: .now() + InputSourceApproval.deadline) {
            error("等待系統啟用 \(plan.selectable) 超過 \(Int(InputSourceApproval.deadline)) 秒，先結束；登入後會再試。")
            exit(2)
        }
        let registered = TISRegisterInputSource(url as CFURL)
        if registered != noErr {
            error("無法登記輸入來源（\(registered)）")
            return 2
        }
        let start = Date()
        let outcome = InputSourceApproval.run(
            ids: plan.enabled,
            forced: force ? plan.parent : nil,
            state: { readState(appPath) ?? cachedState(plan) },
            request: { request($0) },
            wait: { Thread.sleep(forTimeInterval: $0) },
            clock: { Date().timeIntervalSince(start) }
        )
        switch outcome {
        case .enabled:
            break
        case .failed(let message):
            error(message)
            return 2
        case .pending(let id):
            error("已要求啟用 \(id)，但 \(Int(InputSourceApproval.window)) 秒內系統尚未啟用（可能還在等「允許」）；登入後會再試。")
            return 2
        }
        _ = readState(appPath, select: true) // 正在用升級前的父項時改選這一版的模式
        // 新來源可用後才停用舊來源；登記、查詢或啟用失敗時保留使用者原有的輸入方式。
        for old in sources(includeAll: true) {
            guard let id = string(old, kTISPropertyInputSourceID),
                  plan.stale(id: id, bundle: string(old, kTISPropertyBundleID)) else { continue }
            _ = TISDisableInputSource(old)
        }
        print("已啟用 \(plan.selectable)")
        return 0
    }

    // 對一個來源送出啟用要求（系統會跳出「允許」詢問）；回傳錯誤訊息，成功為 nil。
    static func request(_ id: String) -> String? {
        guard let source = InputSourceLookup.find(
            list: { sources(includeAll: true) },
            matches: { string($0, kTISPropertyInputSourceID) == id },
            wait: { Thread.sleep(forTimeInterval: $0) },
            missing: { attempt in error("輸入來源登記回傳成功，但清單尚未包含 \(id)（第 \(attempt)/3 次查詢）") }
        ) else {
            return "重查兩次後仍找不到輸入來源 \(id)；請登出再登入後確認。"
        }
        let status = TISEnableInputSource(source)
        return status == noErr ? nil : "無法啟用 \(id)（\(status)）；請在系統設定確認是否允許。"
    }

    // 本行程快取的狀態：子行程讀不到時退而求其次，避免對已啟用（已核准）的來源再送要求而多跳一次詢問。
    static func cachedState(_ plan: InputSourcePlan) -> [String: Bool] {
        var state: [String: Bool] = [:]
        for source in sources(includeAll: true) {
            if let id = string(source, kTISPropertyInputSourceID), plan.enabled.contains(id) {
                state[id] = bool(source, kTISPropertyInputSourceIsEnabled)
            }
        }
        return state
    }

    // 子行程回讀最多等這麼久；卡住就結束它、當成讀不到。
    static let readTimeout: TimeInterval = 10

    // TIS 在行程內快取輸入來源的狀態，本行程重查清單仍是舊值；在新的子行程回讀各來源是否已啟用。
    // select 為 false 時用 --query-input-source（只查詢）；true 時用 --confirm-input-source（已啟用時改選這一版的模式）。
    // 子行程讀不到結果、逾時或失敗時回傳 nil（當成未啟用）。
    static func readState(_ appPath: String, select: Bool = false) -> [String: Bool]? {
        let process = Process()
        process.executableURL = Bundle.main.executableURL ?? URL(fileURLWithPath: CommandLine.arguments[0])
        process.arguments = [select ? "--confirm-input-source" : "--query-input-source", appPath]
        let output = Pipe()
        process.standardOutput = output
        let done = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in done.signal() }
        guard (try? process.run()) != nil else { return nil }
        var data = Data()
        let reader = DispatchQueue(label: "0c.input-source.read")
        reader.async { data = output.fileHandleForReading.readDataToEndOfFile() }
        if done.wait(timeout: .now() + readTimeout) == .timedOut {
            process.terminate()
            return nil
        }
        reader.sync {}
        return process.terminationStatus == 0 ? InputSourceApproval.parse(String(decoding: data, as: UTF8.self)) : nil
    }

    // 子行程（0c --confirm-input-source <App 路徑>）：逐行輸出「<ID> enabled|disabled|missing」。
    // 可選取的來源已啟用時，若目前選取的是靈犀注音的其他來源（例如升級前的父項），改選這一版的來源，再輸出「<ID> selected」。
    // select 為 false（0c --query-input-source，0.2.4 起；排程在背景確認使用者是不是自己加入了，#195）時只查詢，
    // 絕不改選：背景的喚起不能切換使用者正在用的輸入來源。
    static func reportState(appPath: String, select: Bool = true) -> Int32 {
        guard let plan = plan(appPath) else { return 1 }
        let list = sources(includeAll: true)
        var ready = false
        for id in plan.enabled {
            let source = list.first { string($0, kTISPropertyInputSourceID) == id }
            let enabled = source.map { bool($0, kTISPropertyInputSourceIsEnabled) } ?? false
            if id == plan.selectable { ready = enabled }
            print("\(id) \(source == nil ? "missing" : enabled ? "enabled" : "disabled")")
        }
        guard ready, select else { return 0 }
        let current = TISCopyCurrentKeyboardInputSource().takeRetainedValue()
        if plan.shouldSelect(currentID: string(current, kTISPropertyInputSourceID), currentBundle: string(current, kTISPropertyBundleID)),
           let source = list.first(where: { string($0, kTISPropertyInputSourceID) == plan.selectable }),
           TISSelectInputSource(source) == noErr {
            print("\(plan.selectable) selected")
        }
        return 0
    }
}
