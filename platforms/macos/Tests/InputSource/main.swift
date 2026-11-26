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

// #238：父項與模式一次只送一個啟用要求，等回讀到已啟用才送下一個；每個要求只送一次。
// approvals：模擬系統，送出要求後經過幾秒（使用者按「允許」）才啟用；沒列出的來源一直不啟用。
struct ApprovalRun {
    var outcome: InputSourceApproval.Outcome
    var requests: [String]
    var requestedAt: [TimeInterval]
    var elapsed: TimeInterval
}
// together：父項被允許時，系統把這些來源一起寫進啟用清單（模式預設啟用，#348）；togetherAfter：晚幾秒才寫進。
func approvalRun(_ ids: [String], initial: Set<String> = [], approvals: [String: TimeInterval] = [:],
                 forced: String? = nil, unreadable: Bool = false, failing: String? = nil,
                 lost: Set<String> = [], together: Set<String> = [], togetherAfter: TimeInterval = 0,
                 follows: Bool = false) -> ApprovalRun {
    var now: TimeInterval = 0
    var enabled = initial
    var lostOnce = lost
    var pending: [String: TimeInterval] = [:]
    var requests: [String] = []
    var requestedAt: [TimeInterval] = []
    let outcome = InputSourceApproval.run(
        ids: ids, forced: forced, follows: follows,
        state: {
            for (id, at) in pending where now >= at {
                // 父項還沒啟用時，模式的「允許」不生效（macOS 26.6.2 實測）
                if id == ids.last && ids.count > 1 && !enabled.contains(ids[0]) { continue }
                enabled.insert(id)
                pending[id] = nil
                if id == ids.first { for other in together where pending[other] == nil { pending[other] = now + togetherAfter } }
            }
            if unreadable { return nil }
            var state: [String: Bool] = [:]
            for id in ids { state[id] = enabled.contains(id) }
            return state
        },
        request: { id in
            requests.append(id)
            requestedAt.append(now)
            if id == failing { return "無法啟用 \(id)（-50）" }
            if let delay = approvals[id] { pending[id] = now + delay }
            return nil
        },
        wait: { seconds in
            now += seconds
            // 換掉 App 後系統重新掃描，蓋掉剛啟用的紀錄（一次）
            for id in lostOnce where enabled.contains(id) && seconds == InputSourceApproval.settle {
                enabled.remove(id)
                lostOnce.remove(id)
            }
        },
        clock: { now }
    )
    return ApprovalRun(outcome: outcome, requests: requests, requestedAt: requestedAt, elapsed: now)
}
let both = [target.id, mode]
let already = approvalRun(both, initial: Set(both))
expect("已啟用的父項與模式不再送要求（不重複加入、不再詢問）", already.outcome == .enabled && already.requests.isEmpty)
let fresh = approvalRun(both, approvals: [target.id: 2, mode: 3])
expect("全新安裝：先送父項，回讀到啟用才送模式，各只送一次",
       fresh.outcome == .enabled && fresh.requests == both && fresh.requestedAt[1] >= 2)
let late = approvalRun(both, approvals: [target.id: 25, mode: 40])
expect("晚按「允許」（25 秒、40 秒）仍在時限內完成", late.outcome == .enabled && late.requests == both)
let ignored = approvalRun(both)
expect("父項一直沒按「允許」：只送一次父項，不送模式，時限到就留給登入後補做",
       ignored.outcome == .pending(target.id) && ignored.requests == [target.id]
       && ignored.elapsed >= InputSourceApproval.window && ignored.elapsed < InputSourceApproval.window + 2)
let modeIgnored = approvalRun(both, approvals: [target.id: 1])
expect("模式一直沒按「允許」：模式只送一次，時限到就結束",
       modeIgnored.outcome == .pending(mode) && modeIgnored.requests == both)
let upgraded = approvalRun(both, initial: [target.id], approvals: [mode: 2])
expect("父項已啟用（dev.59 升級、或上次只允許了父項）：只送模式", upgraded.outcome == .enabled && upgraded.requests == [mode])
let signature = approvalRun(both, initial: Set(both), forced: target.id)
expect("簽章改變：父項重新送一次要求、不等，已啟用的模式不再送",
       signature.outcome == .enabled && signature.requests == [target.id])
let blind = approvalRun(both, approvals: [target.id: 1, mode: 1], unreadable: true)
expect("回讀一直失敗：不重送要求，時限到就結束", blind.outcome == .pending(target.id) && blind.requests == [target.id])
let error = approvalRun(both, failing: target.id)
expect("啟用回傳錯誤立即停止", error.outcome == .failed("無法啟用 \(target.id)（-50）") && error.requests == [target.id])
let overwritten = approvalRun(both, approvals: [target.id: 1, mode: 1], lost: [mode])
expect("重新掃描蓋掉剛啟用的模式：再走一次，只補送模式",
       overwritten.outcome == .enabled && overwritten.requests == both + [mode])
// #348：模式預設啟用（follows），父項的「允許」一起把模式寫進啟用清單，只問一次；模式的要求一律不送。
let once = approvalRun(both, approvals: [target.id: 2], together: [mode], follows: true)
expect("模式預設啟用：全新安裝只送父項的要求，允許後模式一起啟用", once.outcome == .enabled && once.requests == [target.id])
let onceLate = approvalRun(both, approvals: [target.id: 50], together: [mode], follows: true)
expect("模式預設啟用：晚按（50 秒）也只送父項", onceLate.outcome == .enabled && onceLate.requests == [target.id])
let onceSlow = approvalRun(both, approvals: [target.id: 2], together: [mode], togetherAfter: 3, follows: true)
expect("模式晚幾秒才寫進清單：等一下，不送模式的要求", onceSlow.outcome == .enabled && onceSlow.requests == [target.id])
let onceIgnored = approvalRun(both, follows: true)
expect("模式預設啟用、父項一直沒按「允許」：只送一次父項，留給之後", onceIgnored.outcome == .pending(target.id) && onceIgnored.requests == [target.id])
let trap = approvalRun(both, initial: [target.id], approvals: [mode: 2], follows: true)
expect("啟用清單只有父項（0.2.14 以前晚按、dev.60 以前升級）：不送沒用的模式要求，回報要使用者移除再加入",
       trap.outcome == .unlisted(mode) && trap.requests.isEmpty)
let notTogether = approvalRun(both, approvals: [target.id: 2, mode: 1], follows: true)
expect("允許父項後模式沒有一起進清單：等 follow 秒後回報，不送模式的要求",
       notTogether.outcome == .unlisted(mode) && notTogether.requests == [target.id]
       && notTogether.elapsed >= 2 + InputSourceApproval.follow)
let followsSignature = approvalRun(both, initial: Set(both), forced: target.id, follows: true)
expect("模式預設啟用、簽章改變：父項重送一次，模式不送", followsSignature.outcome == .enabled && followsSignature.requests == [target.id])
expect("讀 Info.plist：模式預設啟用時 modeFollowsParent",
       InputSourcePlan(bundle: target.id, info: ["TISInputSourceID": target.id,
                       "ComponentInputModeDict": ["tsInputModeListKey": [mode: ["tsInputModeDefaultStateKey": true]],
                                                  "tsVisibleInputModeOrderedArrayKey": [mode]]])!.modeFollowsParent
       && !withMode.modeFollowsParent && !withoutMode.modeFollowsParent)
let topLevel = approvalRun([target.id], approvals: [target.id: 2])
expect("沒有模式的舊版只送頂層來源", topLevel.outcome == .enabled && topLevel.requests == [target.id])
expect("整體上限涵蓋兩個來源的等待", InputSourceApproval.deadline > InputSourceApproval.window * 2)
expect("解析回讀輸出：enabled／selected 算已啟用",
       InputSourceApproval.parse("\(target.id) enabled\n\(mode) disabled\n") == [target.id: true, mode: false]
       && InputSourceApproval.parse("\(target.id) enabled\n\(mode) enabled\n\(mode) selected\n")[mode] == true
       && InputSourceApproval.parse("\(target.id) missing\n")[target.id] == false)
// #348：「已啟用」要看系統的啟用清單。TIS 對預設啟用的模式一直回報 enabled。
let parentEntry: [String: Any] = ["Bundle ID": target.id, "InputSourceKind": "Keyboard Input Method"]
let modeEntry: [String: Any] = ["Bundle ID": target.id, "Input Mode": mode, "InputSourceKind": "Input Mode"]
let foreign: [String: Any] = ["Bundle ID": "org.openvanilla.inputmethod.McBopomofo", "Input Mode": "org.openvanilla.inputmethod.McBopomofo.Bopomofo", "InputSourceKind": "Input Mode"]
expect("啟用清單有父項與模式", withMode.listed([modeEntry, parentEntry]) == [target.id: true, mode: true])
expect("啟用清單只有父項：模式不算", withMode.listed([parentEntry, foreign]) == [target.id: true, mode: false])
expect("清單是空的：都不算", withMode.listed([]) == [target.id: false, mode: false])
expect("讀不到清單：判斷不了", withMode.listed(nil) == nil)
// 全新帳號還沒允許過第三方輸入法：com.apple.inputsources 整個不存在，HIToolbox 只有 Apple 的來源（.13 macOS 15.8.2）。
// 不能當成判斷不了，否則預設啟用的模式照 TIS 算成已啟用（#348 驗收 S2）。
let abc: [String: Any] = ["InputSourceKind": "Keyboard Layout", "KeyboardLayout ID": 252, "KeyboardLayout Name": "ABC"]
let freshAccount = InputSourcePlan.mergeLists(thirdParty: nil, system: [abc])
expect("全新帳號：沒有第三方清單、系統清單只有 ABC，父項與模式都不算",
       withMode.listed(freshAccount) == [target.id: false, mode: false]
       && withMode.effective(tis: [target.id: false, mode: true], listed: withMode.listed(freshAccount)) == [target.id: false, mode: false])
expect("舊版 macOS 記在 HIToolbox 的也算", withMode.listed(InputSourcePlan.mergeLists(thirdParty: nil, system: [abc, parentEntry, modeEntry]))
       == [target.id: true, mode: true])
expect("兩份都讀不到才是判斷不了", InputSourcePlan.mergeLists(thirdParty: nil, system: nil) == nil)
let following = InputSourcePlan(bundle: target.id, info: ["TISInputSourceID": target.id,
                                "ComponentInputModeDict": ["tsInputModeListKey": [mode: ["tsInputModeDefaultStateKey": true]],
                                                           "tsVisibleInputModeOrderedArrayKey": [mode]]])!
expect("讀不到清單、模式預設啟用：父項還沒啟用時模式不算（重新開機時讀不到清單，#348 驗收 S3）",
       following.effective(tis: [target.id: false, mode: true], listed: nil) == [target.id: false, mode: false]
       && following.effective(tis: [target.id: true, mode: true], listed: nil) == [target.id: true, mode: true])
expect("沒有模式的舊版看頂層那筆", withoutMode.listed([parentEntry]) == [target.id: true])
expect("Zuin 的清單不算 0c", zuin.listed([parentEntry, modeEntry]) == [legacy.id: false])
let tisAll = [target.id: true, mode: true]
expect("TIS 顯示啟用但模式不在清單：模式算未啟用",
       withMode.effective(tis: tisAll, listed: [target.id: true, mode: false]) == [target.id: true, mode: false])
expect("讀不到清單時照 TIS", withMode.effective(tis: [target.id: false, mode: true], listed: nil) == [target.id: false, mode: true])
expect("在清單裡但 TIS 未啟用：不算", withMode.effective(tis: [target.id: false, mode: true], listed: [target.id: true, mode: true])
       == [target.id: false, mode: true])
print("\(passed) input source checks passed")
