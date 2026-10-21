import Foundation

// 更新時的交接（#170）：0c-update 安裝新版後送 SIGTERM 請舊的輸入法程式結束，再啟動 ~/Library/Input Methods
// 裡新的 App（鼠鬚管等安裝時也是結束舊程式、交給系統重新啟動）。0c-update 只在引擎沒有組字、使用者停手之後才送，
// 背景安裝更只在使用者離開電腦（螢幕鎖定或關閉）時送（#178）；這裡再守一次，避免剛好在送出之前又開始打字：收到 SIGTERM 時若有組字、
// 或剛按過鍵，就等到沒有組字並停手 quiet 秒之後才結束。登出時系統送的 SIGTERM 也走這裡，那時不會在打字。
enum RetirePolicy {
    static let quiet: TimeInterval = 1.5

    static func ready(busy: Bool, sinceKey: TimeInterval) -> Bool { !busy && sinceKey >= quiet }
}

final class RetireMonitor {
    static let shared = RetireMonitor()

    private var source: DispatchSourceSignal?
    private var timer: Timer?
    private var lastKey = Date.distantPast
    private var busy: [() -> Bool] = []

    /// 在主執行緒呼叫一次。SIGTERM 改由主佇列處理，不再立即結束行程。
    func start(busy: @escaping () -> Bool) {
        self.busy.append(busy)
        guard source == nil else { return }
        signal(SIGTERM, SIG_IGN)
        let source = DispatchSource.makeSignalSource(signal: SIGTERM, queue: .main)
        source.setEventHandler { [weak self] in self?.requested() }
        source.resume()
        self.source = source
    }

    /// 每個按鍵（含 Shift 等修飾鍵）呼叫。
    func noteKey() { lastKey = Date() }

    private func requested() {
        guard timer == nil else { return }
        if check() { return }
        timer = Timer.scheduledTimer(withTimeInterval: 0.25, repeats: true) { [weak self] _ in _ = self?.check() }
    }

    private func check() -> Bool {
        guard RetirePolicy.ready(busy: busy.contains { $0() }, sinceKey: Date().timeIntervalSince(lastKey)) else { return false }
        exit(0)
    }
}
