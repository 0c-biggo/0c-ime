import Foundation

struct Source {
    let id: String
    let enabled: Bool
}

let target = Source(id: "com.biggo.inputmethod.0c", enabled: false)
let other = Source(id: "com.apple.keylayout.ABC", enabled: true)
let legacy = Source(id: "org.zuin.inputmethod.Zuin", enabled: true)
var passed = 0

func check(_ name: String, snapshots: [[Source]], id: String = target.id,
           queries: Int, delays: [TimeInterval], misses: [Int], expected: Source?) {
    var calls = 0
    var waits: [TimeInterval] = []
    var missing: [Int] = []
    let found = InputSourceLookup.find(
        list: {
            precondition(calls < snapshots.count, "\(name): 超出查詢上限")
            let snapshot = snapshots[calls]
            calls += 1
            return snapshot
        },
        matches: { $0.id == id },
        wait: { waits.append($0) },
        missing: { missing.append($0) }
    )
    precondition(calls == queries && waits == delays && missing == misses, "\(name): 重查或等待次數錯誤")
    precondition(found?.id == expected?.id && found?.enabled == expected?.enabled, "\(name): 來源錯誤")
    passed += 1
    print("PASS \(name)")
}

check("已登記的來源立即返回，不等待", snapshots: [[other, target]],
      queries: 1, delays: [], misses: [], expected: target)
check("第一次重查找到新來源", snapshots: [[other], [target]],
      queries: 2, delays: [0.5], misses: [1], expected: target)
check("第二次重查找到新來源", snapshots: [[], [other], [target]],
      queries: 3, delays: [0.5, 1.5], misses: [1, 2], expected: target)
check("兩次重查後仍不存在，結束等待", snapshots: [[], [], []],
      queries: 3, delays: [0.5, 1.5], misses: [1, 2, 3], expected: nil)
check("保留舊來源不代表新來源已登記", snapshots: [[legacy], [other, legacy], [legacy]],
      queries: 3, delays: [0.5, 1.5], misses: [1, 2, 3], expected: nil)
check("回復舊版只尋找指定舊來源", snapshots: [[target], [target, legacy]], id: legacy.id,
      queries: 2, delays: [0.5], misses: [1], expected: legacy)
let enabled = Source(id: target.id, enabled: true)
check("已啟用狀態完整保留給呼叫端", snapshots: [[other, enabled]],
      queries: 1, delays: [], misses: [], expected: enabled)

// #87：Info.plist 宣告輸入模式時，啟用父項與模式、選取模式；沒有模式的舊版照舊。
let mode = "com.biggo.inputmethod.0c.Bopomofo"
let modern: [String: Any] = ["TISInputSourceID": target.id,
                             "ComponentInputModeDict": ["tsInputModeListKey": [mode: [:]],
                                                        "tsVisibleInputModeOrderedArrayKey": [mode]]]
let dev59: [String: Any] = ["TISInputSourceID": target.id]
func expect(_ name: String, _ condition: Bool) {
    precondition(condition, name)
    passed += 1
    print("PASS \(name)")
}
let withMode = InputSourcePlan(bundle: target.id, info: modern)!
let withoutMode = InputSourcePlan(bundle: target.id, info: dev59)!
let zuin = InputSourcePlan(bundle: legacy.id, info: [:])!
expect("有模式時先啟用父項再啟用模式，選取模式", withMode.enabled == [target.id, mode] && withMode.selectable == mode)
expect("沒有模式的舊版只啟用頂層來源", withoutMode.enabled == [target.id] && withoutMode.selectable == target.id)
expect("沒有 TISInputSourceID 時用 bundle ID", zuin.parent == legacy.id && zuin.mode == nil)
expect("不是靈犀注音的 App 不處理", InputSourcePlan(bundle: "com.apple.inputmethod.TCIM", info: modern) == nil)
expect("升級：停用舊名稱，保留父項與模式",
       withMode.stale(id: legacy.id, bundle: legacy.id) && !withMode.stale(id: target.id, bundle: target.id)
       && !withMode.stale(id: mode, bundle: target.id))
expect("回復到沒有模式的版本：停用留下的模式", withoutMode.stale(id: mode, bundle: target.id) && !withoutMode.stale(id: target.id, bundle: target.id))
expect("回復到 Zuin：停用 0c 的父項與模式", zuin.stale(id: target.id, bundle: target.id) && zuin.stale(id: mode, bundle: target.id))
expect("不碰其他輸入法", !withMode.stale(id: "com.apple.inputmethod.TCIM.Zhuyin", bundle: "com.apple.inputmethod.TCIM")
       && !withMode.stale(id: other.id, bundle: nil))
expect("正在用升級前的父項時改選模式", withMode.shouldSelect(currentID: target.id, currentBundle: target.id))
expect("已選取模式時不重選", !withMode.shouldSelect(currentID: mode, currentBundle: target.id))
expect("正在用其他輸入法時不切換", !withMode.shouldSelect(currentID: other.id, currentBundle: nil)
       && !withMode.shouldSelect(currentID: "com.apple.inputmethod.TCIM.Zhuyin", currentBundle: "com.apple.inputmethod.TCIM"))

func confirmRun(_ results: [Bool], failAt: Int? = nil) -> (Int?, [Int], [TimeInterval]) {
    var enabled: [Int] = []
    var waits: [TimeInterval] = []
    var checks = 0
    let round = InputSourceLookup.confirm(
        enable: { enabled.append($0); return $0 != failAt },
        confirmed: { defer { checks += 1 }; return results[checks] },
        wait: { waits.append($0) }
    )
    return (round, enabled, waits)
}
let first = confirmRun([true])
expect("啟用後等待系統重新掃描再回讀", first.0 == 1 && first.1 == [1] && first.2 == [3])
let overwritten = confirmRun([false, true])
expect("重新掃描蓋掉啟用紀錄時整輪重來", overwritten.0 == 2 && overwritten.1 == [1, 2] && overwritten.2 == [3, 3])
let never = confirmRun(Array(repeating: false, count: InputSourceLookup.rounds))
expect("約 15 秒仍未啟用就放棄，留待登入後補做", never.0 == nil && never.1 == Array(1...InputSourceLookup.rounds)
       && never.2.reduce(0, +) == 15)
let failed = confirmRun([false], failAt: 2)
expect("登記或啟用失敗立即停止", failed.0 == nil && failed.1 == [1, 2] && failed.2 == [3])
print("\(passed) input source checks passed")
