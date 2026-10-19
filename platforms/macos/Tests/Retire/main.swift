import Foundation

// RetirePolicy（#170）：收到 SIGTERM 時，有組字或剛按過鍵就等，沒有組字並停手後才結束。
var checks = 0
func check(_ condition: Bool, _ message: String) {
    checks += 1
    if !condition { fatalError(message) }
}

check(!RetirePolicy.ready(busy: true, sinceKey: 3600), "composing: must not exit even after a long pause")
check(!RetirePolicy.ready(busy: false, sinceKey: 0.2), "just typed: wait for the user to stop")
check(!RetirePolicy.ready(busy: false, sinceKey: RetirePolicy.quiet - 0.01), "still within the quiet period")
check(RetirePolicy.ready(busy: false, sinceKey: RetirePolicy.quiet), "idle and not composing: exit")
check(RetirePolicy.ready(busy: false, sinceKey: Date().timeIntervalSince(.distantPast)), "never typed since launch: exit right away")
print("retire policy: \(checks) checks passed")
