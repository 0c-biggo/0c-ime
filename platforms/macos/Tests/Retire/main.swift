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

// HandoverPolicy（#195）：執行檔被更新換掉後，使用者離開時請排程立即交接；同一段離開只喚起一次。
let a = HandoverPolicy.FileID(device: 1, inode: 10), b = HandoverPolicy.FileID(device: 1, inode: 11)
check(!HandoverPolicy.replaced(launched: a, current: a), "same executable: nothing to hand over")
check(HandoverPolicy.replaced(launched: a, current: b), "executable replaced by an update: hand over")
check(!HandoverPolicy.replaced(launched: a, current: nil), "app removed: nothing to start instead")
check(!HandoverPolicy.replaced(launched: nil, current: b), "unknown launch file: do not guess")
// #205：被更新移入備份後（啟動路徑上換成新版或已移除）不再碰 SMAppService，免得背景項目的登記被拉到備份。
check(HandoverPolicy.atLaunchPath(launched: a, current: a), "still the launched executable: query the schedule")
check(!HandoverPolicy.atLaunchPath(launched: a, current: b), "moved into a backup, new version at the launch path: hands off")
check(!HandoverPolicy.atLaunchPath(launched: a, current: nil), "uninstalled while running: hands off")
check(!HandoverPolicy.atLaunchPath(launched: nil, current: b), "unknown launch file: do not claim the location")
let t0 = Date()
check(HandoverPolicy.due(last: nil, now: t0), "first time away: kick the schedule")
check(!HandoverPolicy.due(last: t0, now: t0.addingTimeInterval(5)), "lock then display sleep: kick once")
check(HandoverPolicy.due(last: t0, now: t0.addingTimeInterval(HandoverPolicy.minGap)), "away again later: kick again")
check(HandoverPolicy.due(last: t0, now: t0.addingTimeInterval(-10)), "clock moved back: do not get stuck")
let exe = CommandLine.arguments[0]
check(HandoverPolicy.fileID(exe) != nil && HandoverPolicy.fileID(exe) == HandoverPolicy.fileID(exe), "reads the running executable's file identity")
check(HandoverPolicy.fileID(exe + ".missing") == nil, "missing file has no identity")
print("retire policy: \(checks) checks passed")
