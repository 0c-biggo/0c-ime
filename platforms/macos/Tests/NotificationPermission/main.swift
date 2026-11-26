import Foundation

// #350：什麼時候正式詢問通知權限（NotificationPermission.shouldAsk）與紀錄檔。不呼叫通知中心、不碰使用者資料。
typealias P = NotificationPermission
let now = Date(timeIntervalSince1970: 1_800_000_000)
func ask(_ status: P.Status, record: P.Record? = nil, version: String = "0.2.15", composing: Bool = false,
         sinceKey: TimeInterval? = 10, locked: Bool = false) -> Bool {
    P.shouldAsk(status: status, record: record, version: version, composing: composing, sinceKey: sinceKey, locked: locked, now: now)
}

// 還沒決定與暫時授權（0.2.14 以前的安裝）都問；已允許、已拒絕不問（系統也不會再詢問）。
precondition(ask(.notDetermined) && ask(.provisional))
for status in [P.Status.authorized, .denied, .ephemeral, .unknown] {
    precondition(!ask(status), "\(status) must not prompt")
}
// 使用者正在打字、組字中、從沒用靈犀打過字、停手太久（可能已離開）、螢幕鎖著時都不問。
precondition(!ask(.provisional, composing: true), "composing")
precondition(!ask(.provisional, sinceKey: 1), "just typed")
precondition(!ask(.provisional, sinceKey: nil), "no key yet in this process")
precondition(!ask(.provisional, sinceKey: P.recent + 1), "stopped long ago")
precondition(!ask(.provisional, locked: true), "locked")
precondition(ask(.provisional, sinceKey: P.quiet) && ask(.provisional, sinceKey: P.recent))

// 問過一次就不再問；同一版不重問；沒回答的（狀態仍是暫時授權）換版而且隔 30 天才再問。
var asked = P.Record(status: .provisional, checked_at: now)
precondition(ask(.provisional, record: asked), "status snapshot alone is not an ask")
asked.asked_at = now.addingTimeInterval(-3600)
asked.asked_version = "0.2.15"
precondition(!ask(.provisional, record: asked), "asked an hour ago")
precondition(!ask(.provisional, record: asked, version: "0.2.16"), "new version but within 30 days")
asked.asked_at = now.addingTimeInterval(-P.reaskAfter)
precondition(!ask(.provisional, record: asked), "same version never re-asks")
precondition(ask(.provisional, record: asked, version: "0.2.16"), "new version after 30 days")

// 紀錄檔：0c-update 匯出狀態摘要時讀這個檔案（shell.notifications），欄位名稱要固定。
let dir = FileManager.default.temporaryDirectory.appendingPathComponent("notification-permission-\(getpid())")
defer { try? FileManager.default.removeItem(at: dir) }
let url = dir.appendingPathComponent("notification-permission.json")
precondition(P.load(url) == nil)
var record = P.Record(status: .authorized, alert_style: "banner", checked_at: now, asked_at: now, asked_version: "0.2.15",
                      asked_from: .provisional, answer: "granted")
P.save(record, to: url)
precondition(P.load(url) == record)
let json = try! JSONSerialization.jsonObject(with: Data(contentsOf: url)) as! [String: Any]
precondition(json["status"] as? String == "authorized" && json["asked_from"] as? String == "provisional"
             && json["alert_style"] as? String == "banner" && json["answer"] as? String == "granted"
             && json["checked_at"] as? String == "2027-01-15T08:00:00Z", "\(json)")
record.status = .notDetermined
P.save(record, to: url)
precondition((try! JSONSerialization.jsonObject(with: Data(contentsOf: url)) as! [String: Any])["status"] as? String == "not-determined")
print("Passed notification permission timing/record checks (no Notification Center calls)")
