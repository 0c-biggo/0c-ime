import AppKit

// 背景更新裝好新版後，執行中的舊輸入法程式（就是這個行程）由 0c-update 在使用者離開電腦時交接（#178）。排程每 15 分鐘
// 才喚起一次，鎖一下就解開碰不到喚起的那一刻，就不會換（#195）。這裡在螢幕鎖定、螢幕保護程式啟動、螢幕關閉或切換到別的
// 使用者時，若磁碟上的執行檔已經被更新換掉，就請 launchd 立即執行一次排程（0c-update auto）。交接本身照舊由 0c-update
// 判斷：使用者已離開、沒有組字、停手，才請這個行程結束並啟動新版。
enum HandoverPolicy {
    struct FileID: Equatable {
        let device: UInt64
        let inode: UInt64
    }

    /// 等系統把工作階段標成已鎖定（ioreg 的 CGSSessionScreenIsLocked）再喚起排程。
    static let delay: TimeInterval = 3
    /// 同一段離開只喚起一次（鎖定與螢幕關閉常連著發生）。
    static let minGap: TimeInterval = 60

    static func fileID(_ path: String) -> FileID? {
        var st = stat()
        guard stat(path, &st) == 0 else { return nil }
        return FileID(device: UInt64(st.st_dev), inode: UInt64(st.st_ino))
    }

    /// 啟動時的執行檔已經被換掉。現在讀不到（解除安裝、搬移中）時不喚起：沒有新版可換。
    static func replaced(launched: FileID?, current: FileID?) -> Bool {
        guard let launched, let current else { return false }
        return launched != current
    }

    /// 這個行程仍在啟動時的位置執行：啟動路徑上的執行檔就是啟動時那一個。更新把 App 移入備份後不再成立（路徑上換成新版，
    /// 或解除安裝後不存在）；讀不到啟動時的檔案時也當作不成立。
    static func atLaunchPath(launched: FileID?, current: FileID?) -> Bool {
        guard let launched, let current else { return false }
        return launched == current
    }

    static func due(last: Date?, now: Date) -> Bool {
        guard let last else { return true }
        return now.timeIntervalSince(last) >= minGap || now < last
    }
}

final class HandoverMonitor {
    static let shared = HandoverMonitor()
    static let agentLabel = "com.biggo.0c.update"

    private var executable: String?
    private var launched: HandoverPolicy.FileID?
    private var lastKick: Date?
    private var tokens: [NSObjectProtocol] = []

    /// 在主執行緒呼叫一次。
    func start() {
        guard tokens.isEmpty, let path = Bundle.main.executablePath else { return }
        executable = path
        launched = HandoverPolicy.fileID(path)
        let distributed = DistributedNotificationCenter.default()
        for name in ["com.apple.screenIsLocked", "com.apple.screensaver.didstart"] {
            tokens.append(distributed.addObserver(forName: Notification.Name(name), object: nil, queue: .main) { [weak self] _ in self?.away() })
        }
        let workspace = NSWorkspace.shared.notificationCenter
        for name in [NSWorkspace.screensDidSleepNotification, NSWorkspace.sessionDidResignActiveNotification] {
            tokens.append(workspace.addObserver(forName: name, object: nil, queue: .main) { [weak self] _ in self?.away() })
        }
    }

    /// 還在 Input Methods 裡啟動時的位置（沒有被更新移入備份）。start() 之前、啟動時讀不到執行檔時一律為 true（照舊）。
    var atLaunchPath: Bool {
        guard let executable, launched != nil else { return true }
        return HandoverPolicy.atLaunchPath(launched: launched, current: HandoverPolicy.fileID(executable))
    }

    private func away() {
        guard let executable, HandoverPolicy.replaced(launched: launched, current: HandoverPolicy.fileID(executable)),
              HandoverPolicy.due(last: lastKick, now: Date()) else { return }
        lastKick = Date()
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + HandoverPolicy.delay) {
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/bin/launchctl")
            process.arguments = ["kickstart", "gui/\(getuid())/\(Self.agentLabel)"]
            process.standardOutput = FileHandle.nullDevice
            process.standardError = FileHandle.nullDevice
            // 排程已在執行時 kickstart 不做事；使用者在系統設定關閉了背景執行時失敗，照舊等重新登入。
            if (try? process.run()) != nil { process.waitUntilExit() }
        }
    }
}
