import Foundation
import Darwin
import Carbon

final class EngineConnection {
    private var fd: Int32 = -1
    private let path: String
    private let start: () throws -> Void
    private let timeout: TimeInterval
    private let maxFrame = 1 << 20

    init(path: String, timeout: TimeInterval = 1, start: @escaping () throws -> Void = {}) {
        self.path = path
        self.timeout = timeout
        self.start = start
    }
    deinit { disconnect() }
    func disconnect() {
        if fd >= 0 { Darwin.close(fd); fd = -1 }
    }

    /// A fresh controller has no connection; an existing one uses a nonblocking
    /// EOF probe. Do not consume or close the old session here:
    /// handle() must still report disconnection so composition recovery runs.
    private var needsCapabilitiesProbe: Bool {
        guard fd >= 0 else { return true }
        var byte: UInt8 = 0
        let count = Darwin.recv(fd, &byte, 1, MSG_PEEK | MSG_DONTWAIT)
        if count < 0 && [EAGAIN, EWOULDBLOCK, EINTR].contains(errno) { return false }
        return count <= 0
    }

    /// A healthy session keeps its cached capabilities. A fresh controller or EOF
    /// ask the replacement engine over a separate, short-lived connection.
    /// This never retries an uncertain input request or changes its session ID.
    func replacementInfo() -> [String: String]? {
        guard needsCapabilitiesProbe else { return nil }
        let probe = EngineConnection(path: path, timeout: 0.05, start: start)
        defer { probe.disconnect() }
        return try? probe.call(Envelope(op: "health")).info
    }

    private func connectOnce() throws -> Bool {
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        let bytes = Array(path.utf8) + [0]
        guard bytes.count <= MemoryLayout.size(ofValue: address.sun_path) else {
            throw EngineError.rejected("Socket path is too long")
        }
        withUnsafeMutableBytes(of: &address.sun_path) { $0.copyBytes(from: bytes) }
        let socket = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard socket >= 0 else { throw EngineError.unavailable }
        _ = fcntl(socket, F_SETFD, FD_CLOEXEC)
        _ = fcntl(socket, F_SETFL, O_NONBLOCK)
        var yes: Int32 = 1
        _ = setsockopt(socket, SOL_SOCKET, SO_NOSIGPIPE, &yes, socklen_t(MemoryLayout.size(ofValue: yes)))
        let result = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(socket, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard result == 0 else { Darwin.close(socket); return false }
        fd = socket
        return true
    }

    // 按鍵路徑上最多等這麼久；引擎冷啟動（載入字典約 0.3 秒以上）時回報 starting，由外殼暫存按鍵，不卡住 App。
    private let startWait: TimeInterval = 0.15

    func connect() throws {
        if fd >= 0 { return }
        if try connectOnce() { return }
        try start()
        let deadline = ProcessInfo.processInfo.systemUptime + startWait
        repeat {
            if try connectOnce() { return }
            Thread.sleep(forTimeInterval: 0.02)
        } while ProcessInfo.processInfo.systemUptime < deadline
        throw EngineError.starting
    }

    /// 不啟動引擎，只試著連線一次；給暫存按鍵的重試計時器用。
    func ready() -> Bool {
        if fd >= 0 { return true }
        return (try? connectOnce()) == true
    }

    private func wait(_ events: Int16, until deadline: TimeInterval) throws {
        while true {
            let remaining = deadline - ProcessInfo.processInfo.systemUptime
            guard remaining > 0 else { throw EngineError.unavailable }
            var p = pollfd(fd: fd, events: events, revents: 0)
            let result = Darwin.poll(&p, 1, Int32(ceil(remaining * 1000)))
            if result < 0 && errno == EINTR { continue }
            if result > 0, p.revents & events == 0, p.revents & Int16(POLLHUP | POLLERR) != 0 { throw EngineError.disconnected }
            guard result > 0, p.revents & events != 0 else { throw EngineError.unavailable }
            return
        }
    }

    private func write(_ data: Data, until deadline: TimeInterval) throws {
        try data.withUnsafeBytes { buffer in
            var offset = 0
            while offset < buffer.count {
                try wait(Int16(POLLOUT), until: deadline)
                let count = Darwin.send(fd, buffer.baseAddress!.advanced(by: offset), buffer.count - offset, 0)
                if count < 0 && (errno == EINTR || errno == EAGAIN) { continue }
                if count < 0 && [EPIPE, ECONNRESET, ENOTCONN].contains(errno) { throw EngineError.disconnected }
                guard count > 0 else { throw EngineError.unavailable }
                offset += count
            }
        }
    }

    private func read(_ size: Int, until deadline: TimeInterval) throws -> Data {
        var data = Data(count: size)
        try data.withUnsafeMutableBytes { buffer in
            var offset = 0
            while offset < size {
                try wait(Int16(POLLIN), until: deadline)
                let count = Darwin.recv(fd, buffer.baseAddress!.advanced(by: offset), size - offset, 0)
                if count < 0 && (errno == EINTR || errno == EAGAIN) { continue }
                if count == 0 || count < 0 && errno == ECONNRESET { throw EngineError.disconnected }
                guard count > 0 else { throw EngineError.unavailable }
                offset += count
            }
        }
        return data
    }

    /// 已經連著的連線被引擎關掉（更新交接、套用設定重啟、當掉）時丟出 `disconnected`：這次請求沒有新引擎處理過，
    /// 由呼叫端決定要不要開新連線重送（#119）。剛建立的連線就斷，照舊是 `unavailable`。
    func call(_ envelope: Envelope) throws -> Reply {
        let reused = fd >= 0
        do {
            try connect()
            let body = try JSONEncoder().encode(envelope)
            guard body.count <= maxFrame else { throw EngineError.invalidFrame }
            let deadline = ProcessInfo.processInfo.systemUptime + timeout
            let n = UInt32(body.count)
            let header = Data((0..<4).map { UInt8(truncatingIfNeeded: n >> ($0 * 8)) })
            try write(header + body, until: deadline)
            let responseHeader = try read(4, until: deadline)
            let size = responseHeader.enumerated().reduce(0) { $0 | (Int($1.element) << ($1.offset * 8)) }
            guard size > 0, size <= maxFrame else { throw EngineError.invalidFrame }
            let reply = try JSONDecoder().decode(Reply.self, from: read(size, until: deadline))
            guard reply.ok else { throw EngineError.rejected(reply.error ?? "Unknown server error") }
            return reply
        } catch EngineError.disconnected where !reused {
            disconnect()
            throw EngineError.unavailable
        } catch {
            disconnect()
            throw error
        }
    }
}

// 核心 info 裡的中英切換鍵（#117）。舊核心沒有 right_shift 時維持原行為：
// 右 Shift 永遠切換、左 Shift 看 left_shift。
struct ShiftToggleSetting: Equatable {
    var left = false
    var right = true
    var tapLimit = RightShiftTap.defaultLimit

    init(info: [String: String]) {
        left = info["left_shift"] == "true"
        if let value = info["right_shift"] { right = value == "true" }
        if let text = info["shift_tap_ms"], let ms = Double(text), ms > 0 { tapLimit = ms / 1000 }
    }
}

// One connection per input controller isolates fields. No retry of an uncertain
// request: a lost commit reply must never insert the same text twice.
final class EngineSession {
    private let connection: EngineConnection
    private let inputAllowed: () -> Bool
    private var id: UInt64?
    // 已確認前文：預設開啟；ZUIN_DOCUMENT_CONTEXT=0 時外殼完全不追蹤（使用者設定在引擎端）。
    let contextEnabled = ProcessInfo.processInfo.environment["ZUIN_DOCUMENT_CONTEXT"] != "0"
    init(connection: EngineConnection, inputAllowed: @escaping () -> Bool = { !IsSecureEventInputEnabled() }) {
        self.connection = connection
        self.inputAllowed = inputAllowed
    }
    func reset() { id = nil; connection.disconnect() }
    func ready() -> Bool { inputAllowed() && connection.ready() }
    /// A new engine session does not know earlier commit tokens.
    var isOpen: Bool { id != nil }

    // 引擎最近一次回報的中英切換鍵設定（#117：toggle-key 右／左右／左／不用 Shift），所有欄位共用。
    // 引擎閒置結束後冷啟動中問不到時沿用它；從沒問到過時左右 Shift 都不切換，和 macOS 的預設相同。
    private static var leftShiftSetting = false
    private static var rightShiftSetting = false
    // 單按時限（info 的 shift_tap_ms）；沒給時 0.3 秒。
    private static var shiftTapLimitSetting = RightShiftTap.defaultLimit
    var shiftTapLimit: TimeInterval { Self.shiftTapLimitSetting }

    // 引擎是否認得 0.2.0 的 Shift＋←／→、Shift＋⌫ 等鍵名（#123，info 的 vocab_keys）。只從 open 與 health 的回覆更新，
    // 不為按鍵另外查詢；設定套用會重啟引擎，下一次 open 就拿到新值。舊引擎沒有這個欄位，視為 false。
    private static var vocabKeysSetting = false
    var vocabKeys: Bool { Self.vocabKeysSetting }
    private static var styleShiftSpaceSetting = false
    var styleShiftSpace: Bool { refreshCapabilities(); return Self.styleShiftSpaceSetting }
    private static var styleCapsLowerSetting = false
    var styleCapsLower: Bool { refreshCapabilities(); return Self.styleCapsLowerSetting }
    // 引擎是否認得 Ctrl＋標點的鍵名而且設定開著（#168，info 的 ctrl_punctuation）；更新方式同 vocabKeys。
    // 舊引擎沒有這個欄位，視為 false，Ctrl＋標點照舊交給 App。
    private static var ctrlPunctSetting = false
    var ctrlPunct: Bool { Self.ctrlPunctSetting }
    // 「輸出簡體字」（#169）：引擎能即時切換（info 的 output_simplified_live）與目前的值（info 的 output_simplified，
    // 之後每個快照的 output_simplified）。選單打勾看它；舊引擎沒有 output_simplified_live，選單改看 0c-update 的狀態。
    // 「用快捷鍵切換簡體字」開著（#192，info 的 simplified_hotkey）；更新方式同 vocabKeys，加上切回 App 時的背景 health，
    // 在設定程式打開後回到 App 就是新值。舊引擎沒有這一項，視為 false，⌃⇧F11 照舊交給 App。
    private static var simplifiedHotkeySetting = false
    var simplifiedHotkey: Bool { Self.simplifiedHotkeySetting && Self.simplifiedLive }
    private(set) static var simplifiedLive = false
    private(set) static var outputSimplified = false
    /// 快照帶回的「輸出簡體字」：設定程式或別的 App 切換後，下一個快照就跟上。只信回報即時切換的引擎。
    static func note(_ snapshot: Snapshot) {
        if simplifiedLive { outputSimplified = snapshot.outputSimplified ?? false } // 引擎在關閉時省略這一項
    }
    // 這個行程是否已從引擎拿到過上面的設定（open 或 health 的回覆）。
    private static var settingsKnown = false

    /// 只在主執行緒呼叫：open 的回覆，以及選單的背景 health（ModelStatusMonitor）都經過這裡。
    static func update(from info: [String: String]) {
        let toggle = ShiftToggleSetting(info: info)
        leftShiftSetting = toggle.left
        rightShiftSetting = toggle.right
        shiftTapLimitSetting = toggle.tapLimit
        vocabKeysSetting = info["vocab_keys"] == "true"
        let shiftSpace = info["input_style_shift_space"] ?? "disabled"
        styleShiftSpaceSetting = !shiftSpace.isEmpty && shiftSpace != "disabled"
        styleCapsLowerSetting = info["input_style_caps_letter"] == "lower"
        ctrlPunctSetting = info["ctrl_punctuation"] == "true"
        simplifiedLive = info["output_simplified_live"] == "true"
        simplifiedHotkeySetting = info["simplified_hotkey"] == "true"
        outputSimplified = simplifiedLive && info["output_simplified"] == "true"
        settingsKnown = true
    }

    /// 引擎目前載入的設定是否讓單按這一側 Shift 切換中英。
    /// 按鍵路徑上不等引擎（#148）：主執行緒卡在 IPC 時，系統把按鍵原樣交給 App、選單裡靈犀的項目只剩「……」。
    /// 設定由 open 的回覆與背景 health 更新（換到 App 時、每 5 秒，設定套用後回到 App 就是新值）；
    /// 已有連線只做非阻塞 EOF 探測；確認交接後才短暫查新引擎，避免第一個 modifier 用到舊風格。
    /// 從未拿過設定時仍同步問一次。探測不關閉舊 session，保留原有斷線／組字復原路徑。
    private func refreshCapabilities() {
        guard Self.settingsKnown, inputAllowed(), let info = connection.replacementInfo() else { return }
        Self.update(from: info)
    }

    func shiftToggles(left: Bool) -> Bool {
        refreshCapabilities()
        if !Self.settingsKnown, inputAllowed(), let info = try? connection.call(Envelope(op: "health")).info {
            Self.update(from: info)
        }
        return left ? Self.leftShiftSetting : Self.rightShiftSetting
    }

    func handle(_ request: Request) throws -> Snapshot {
        do {
            try requireOrdinaryInput()
            if id == nil {
                let reply = try connection.call(Envelope(op: "open", version: 0))
                let opened = reply.session
                if let info = reply.info { Self.update(from: info) }
                id = opened
                if contextEnabled, let opened {
                    try requireOrdinaryInput()
                    // The engine decides from the user's settings whether to use it.
                    _ = try? connection.call(Envelope(op: "handle", session: opened,
                                                      req: Request(type: "context", contextEnabled: true)))
                }
            }
            try requireOrdinaryInput()
            guard let id,
                  let snapshot = try connection.call(Envelope(op: "handle", session: id, req: request)).snapshot else {
                throw EngineError.invalidFrame
            }
            return snapshot
        } catch { reset(); throw error }
    }

    private func requireOrdinaryInput() throws {
        guard inputAllowed() else { throw EngineError.secureInput }
    }
}

// InputMethodKit invokes controllers on the main thread. Keep one launcher for
// all controllers so simultaneous fields never spawn competing writers.
final class EngineHost {
    static let shared = EngineHost()
    private var process: Process?
    private let home = FileManager.default.homeDirectoryForCurrentUser
    private var support: URL { home.appendingPathComponent("Library/Application Support/0c") }
    private var runtime: URL { home.appendingPathComponent("Library/Caches/0c") }

    func session(inputAllowed: @escaping () -> Bool) -> EngineSession {
        EngineSession(connection: EngineConnection(path: runtime.appendingPathComponent("engine.sock").path) {
            try self.launch()
        }, inputAllowed: inputAllowed)
    }

    private func launch() throws {
        if process?.isRunning == true { return }
        let fm = FileManager.default
        for directory in [support, runtime] {
            try fm.createDirectory(at: directory, withIntermediateDirectories: true,
                                   attributes: [.posixPermissions: 0o700])
        }
        guard let resources = Bundle.main.resourceURL,
              let executable = Bundle.main.executableURL?.deletingLastPathComponent().appendingPathComponent("0c-server") else {
            throw EngineError.unavailable
        }
        let p = Process()
        p.executableURL = executable
        p.arguments = ["-data", resources.appendingPathComponent("data").path,
                       "-user", support.appendingPathComponent("user.json").path,
                       "-socket", runtime.appendingPathComponent("engine.sock").path,
                       "-idle-exit", "300s"]
        p.standardInput = FileHandle.nullDevice
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.standardError
        NSLog("0c: starting engine")
        try p.run()
        process = p
    }
}
