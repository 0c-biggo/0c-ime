import Foundation

// #349：安全輸入被背景程式卡住時的判斷（SecureInputWatch）。純邏輯，不讀系統狀態。
var checks = 0
func check(_ condition: Bool, _ message: String) {
    checks += 1
    if !condition {
        print("FAIL: \(message)")
        exit(1)
    }
}

let start = Date(timeIntervalSince1970: 1_800_000_000)
func at(_ seconds: TimeInterval) -> Date { start.addingTimeInterval(seconds) }
let chrome: Int32 = 100, notes: Int32 = 200, terminal: Int32 = 300, agent: Int32 = 400, login: Int32 = 500
func on(holder: Int32?, front: Int32?, holderBundle: String? = nil, frontBundle: String? = nil, paused: Bool = false) -> SecureInputWatch.Observation {
    SecureInputWatch.Observation(secure: true, holder: holder, holderBundle: holderBundle, front: front, frontBundle: frontBundle, paused: paused)
}
let off = SecureInputWatch.Observation(secure: false)

// 前景 App 自己的密碼欄：記錄的 PID 就是前景 App，一直不提醒。
do {
    var watch = SecureInputWatch()
    for t in stride(from: 0.0, through: 600, by: 30) {
        check(watch.check(on(holder: chrome, front: chrome), at: at(t)) == .none, "password field in the front app at \(t)")
    }
    check(watch.check(off, at: at(630)) == .none, "released without ever being stuck")
}

// 終端機的「安全鍵盤輸入」：前景是終端機就不提醒（記錄的 PID 也可能是開啟當下的別的 App）。
do {
    var watch = SecureInputWatch()
    for t in stride(from: 0.0, through: 300, by: 30) {
        check(watch.check(on(holder: notes, front: terminal, frontBundle: "com.googlecode.iterm2"), at: at(t)) == .none,
              "secure keyboard entry in a terminal at \(t)")
    }
}

// 背景卡住：記錄的 PID 不在前景，撐過 grace 秒還開著才提醒，同一個 PID 只提醒一次；放掉後記一次。
do {
    var watch = SecureInputWatch()
    check(watch.check(on(holder: chrome, front: notes), at: at(0)) == .none, "first sight is not enough")
    check(watch.check(on(holder: chrome, front: notes), at: at(SecureInputWatch.grace - 1)) == .none, "still within grace")
    check(watch.check(on(holder: chrome, front: notes), at: at(30)) == .stuck(warn: true), "stuck after grace")
    check(watch.check(on(holder: chrome, front: notes), at: at(60)) == .none, "one notice per stuck period")
    check(watch.check(off, at: at(90)) == .released, "release is recorded once")
    check(watch.check(off, at: at(120)) == .none, "nothing more after release")
    // 同一個 PID 又卡住：記一次卡住，但不再跳提醒。
    check(watch.check(on(holder: chrome, front: notes), at: at(150)) == .none, "second period, first sight")
    check(watch.check(on(holder: chrome, front: notes), at: at(180)) == .stuck(warn: false), "same PID is not shown again")
    check(watch.check(off, at: at(210)) == .released, "second release")
    // 換一個 PID 卡住：再提醒。
    _ = watch.check(on(holder: notes, front: chrome), at: at(240))
    check(watch.check(on(holder: notes, front: chrome), at: at(270)) == .stuck(warn: true), "another PID is shown")
}

// 記錄的是開啟當下的前景 App（1Password 在 Edge 前景時開的，記成 Edge）：使用者一直在 Edge 時分不出來；
// 切到別的 App 看到還開著就起疑，之後切回 Edge 不提醒、但也不忘記，再切走就提醒。
do {
    var watch = SecureInputWatch()
    check(watch.check(on(holder: chrome, front: chrome), at: at(0)) == .none, "front app holds it")
    check(watch.check(on(holder: chrome, front: notes), at: at(30)) == .none, "switched away and it is still on")
    check(watch.check(on(holder: chrome, front: chrome), at: at(60)) == .none, "back in that app: no notice")
    check(watch.check(on(holder: chrome, front: notes), at: at(90)) == .stuck(warn: true), "away again: notice")
}

// 系統的 SecurityAgent（授權、登入對話框）不算；查不到 PID 不判斷。
do {
    var watch = SecureInputWatch()
    for t in stride(from: 0.0, through: 120, by: 30) {
        check(watch.check(on(holder: agent, front: notes, holderBundle: "com.apple.SecurityAgent"), at: at(t)) == .none, "SecurityAgent at \(t)")
        check(watch.check(on(holder: nil, front: notes), at: at(t)) == .none, "no PID at \(t)")
    }
}

// 睡眠、鎖定期間不判斷；解鎖後 loginwindow 還留著（用 Touch ID 解鎖後卡住）照樣要等 grace 才提醒。
do {
    var watch = SecureInputWatch()
    for t in stride(from: 0.0, through: 300, by: 30) {
        check(watch.check(on(holder: login, front: notes, holderBundle: "com.apple.loginwindow", paused: true), at: at(t)) == .none,
              "locked at \(t)")
    }
    check(watch.check(on(holder: login, front: notes, holderBundle: "com.apple.loginwindow"), at: at(330)) == .none, "just unlocked")
    check(watch.check(on(holder: login, front: notes, holderBundle: "com.apple.loginwindow"), at: at(360)) == .stuck(warn: true),
          "loginwindow keeps it after unlock")
    // 鎖定期間放掉：解鎖後的第一次檢查記成放掉。
    check(watch.check(SecureInputWatch.Observation(secure: false, paused: true), at: at(390)) == .none, "paused hides the release")
    check(watch.check(off, at: at(420)) == .released, "release seen after unlock")
}

// 另一個程式重新開啟（PID 換了）：從頭觀察，不沿用前一個的時間。
do {
    var watch = SecureInputWatch()
    _ = watch.check(on(holder: chrome, front: notes), at: at(0))
    check(watch.check(on(holder: notes, front: chrome), at: at(30)) == .none, "new holder restarts the grace period")
    check(watch.check(on(holder: notes, front: chrome), at: at(60)) == .stuck(warn: true), "new holder after grace")
}

// 提醒的文字（負責人 2026-11-24）：開啟時的前景 App 只寫成線索，第一步是結束它再開；瀏覽器多提自動填密碼的擴充功能。
// loginwindow、SecurityAgent、對不到 App 時不點名，只列步驟。
do {
    let edge = SecureInputWatch.clue(bundle: "com.microsoft.edgemac", name: "Microsoft Edge")
    check(edge == SecureInputWatch.Clue(name: "Microsoft Edge", browser: true), "Edge is a browser clue")
    check(SecureInputWatch.clue(bundle: "com.google.Chrome.canary", name: "Google Chrome Canary")?.browser == true, "Chrome channels")
    check(SecureInputWatch.clue(bundle: "com.apple.WebKit.WebContent", name: "Safari Web Content")?.browser == true, "Safari web content")
    check(SecureInputWatch.clue(bundle: "com.apple.Notes", name: "備忘錄") == SecureInputWatch.Clue(name: "備忘錄", browser: false), "ordinary app")
    check(SecureInputWatch.clue(bundle: "com.apple.loginwindow", name: "loginwindow") == nil, "loginwindow is not named")
    check(SecureInputWatch.clue(bundle: "com.apple.SecurityAgent", name: "SecurityAgent") == nil, "SecurityAgent is not named")
    check(SecureInputWatch.clue(bundle: nil, name: nil) == nil && SecureInputWatch.clue(bundle: "x", name: "  ") == nil, "no app")

    let named = SecureInputWatch.message(edge)
    check(named.contains("（開啟時在用的是 Microsoft Edge）"), "clue wording")
    check(named.contains("1. 先結束 Microsoft Edge 再開，或停用它會自動填密碼的擴充功能"), "first step names the app, browser extension")
    check(named.contains("2. 沒用的話，結束密碼管理程式（例如 1Password）"), "password managers second")
    check(named.contains("3. 還是不行就鎖定螢幕") && named.contains("用密碼解鎖") && named.contains("登出"), "lock, password unlock, log out")
    check(!named.contains("卡住了") && !named.contains("造成"), "the clue is not called the holder")
    let notes = SecureInputWatch.message(SecureInputWatch.clue(bundle: "com.apple.Notes", name: "備忘錄"))
    check(notes.contains("1. 先結束 備忘錄 再開，通常就會恢復。") && !notes.contains("停用它"), "non-browser first step")
    let plain = SecureInputWatch.message(nil)
    check(!plain.contains("開啟時在用的是") && plain.contains("1. 結束密碼管理程式") && plain.contains("2. 結束剛才輸入過密碼的 App"),
          "steps only when nothing is named")
}

print("secure input checks passed: \(checks)")
