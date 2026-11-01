package personal

// 選過的注音文（#208）：中英混打時單打 2 接空白是數字（#162、#208 量過，中文後面打數字的人多得多），要「我ㄉ天」的人回頭按 ↓ 選 ㄉ。
// 選過一次就記下「前字＋這個鍵＋空白」，之後同樣的打法直接出注音。範圍刻意很窄：選過「我ㄉ」不影響「第2 名」「有1 個」，
// 沒有這份紀錄時行為完全相同。
//
// 鍵值是 "前字\t注音符號"，句首的前字是空字串：
//   - 只看前一個字，不看前一個詞：斷詞使用者看不到，也會隨後面打的字改變，「真的2」「的2」對他是同一件事。
//   - 存注音符號，不存實體按鍵：中英混打只在標準排列作用，兩者一一對應，符號也是使用者在畫面上看到的（「不再學」的「我ㄉ」）。
//   - 空白、英數或標點後面（「我 2 」「hello 2 」）不記：那裡的 2 通常真的是數字，選過一次不代表下次要注音。
//
// 一次選擇就生效（畫面上看得到結果，↓ 一次就改得回來），所以證據只用來退場：選回原按鍵整筆消失；
// 很久沒用的衰減到 zhuyinKeyFloor 以下不再套用、清理時拿掉（長期半衰期 30 天，一次選擇約 4 個月）；照常用著的暫停衰減，同 keepConfirmed。
// 一次組字裡同一組有選有反悔時，以後面的為準。

// zhuyinKeyFloor：選過的注音文衰減到這裡就不算了；一次選擇是 confirmUnit，與 PrunePolicy.Evidence 同值。
const zhuyinKeyFloor = 0.05

// ZhuyinKeyAction 是一次提交對選過的注音文的影響。
type ZhuyinKeyAction int

const (
	ZhuyinKeyPick ZhuyinKeyAction = iota // 使用者選了注音：記下來
	ZhuyinKeyKeep                        // 紀錄打出的注音照常留著：暫停衰減
	ZhuyinKeyDrop                        // 選回原按鍵：整筆消失
)

// ZhuyinKeyCommit 是提交時畫面上的一個注音鍵＋空白。Left 是前一個字，句首是空字串。
type ZhuyinKeyCommit struct {
	Left, Symbol string
	Action       ZhuyinKeyAction
}

// HasZhuyinKeys：沒有任何紀錄時呼叫端連前字都不必找，打字不多花時間。
func (m *Model) HasZhuyinKeys() bool { return len(m.Store.ZhuyinKeys) > 0 }

// ZhuyinKeyPicked 回報 left 後面這個注音符號的鍵接空白時，是否要直接出注音。
func (m *Model) ZhuyinKeyPicked(left, symbol string) bool {
	if len(m.Store.ZhuyinKeys) == 0 {
		return false
	}
	e, ok := m.Store.ZhuyinKeys[pair(left, symbol)]
	if !ok {
		return false
	}
	e = e.at(m.Store.Time)
	return e.Short+e.Long >= zhuyinKeyFloor
}

// LearnZhuyinKeys 在提交時依畫面最後的結果更新紀錄，依位置先後處理。「不再學」的（Forget("我ㄉ")）選了照常出字，只是不記；
// 不像手選的詞會解除 tombstone：這份紀錄本來就只由手選建立，解除的話「不再學」就跟「移除」沒有分別。要再學用「恢復學習」。
func (m *Model) LearnZhuyinKeys(events []ZhuyinKeyCommit) {
	s := m.Store
	for _, event := range events {
		k := pair(event.Left, event.Symbol)
		switch event.Action {
		case ZhuyinKeyPick:
			if s.Tombstones[event.Left+event.Symbol] {
				continue
			}
			if s.ZhuyinKeys == nil {
				s.ZhuyinKeys = map[string]UsageEvidence{}
			}
			s.ZhuyinKeys[k] = s.ZhuyinKeys[k].add(s.Time, confirmUnit)
		case ZhuyinKeyKeep:
			e, ok := s.ZhuyinKeys[k]
			if !ok || s.Time <= e.Updated {
				continue
			}
			e.Updated = s.Time
			s.ZhuyinKeys[k] = e
		case ZhuyinKeyDrop:
			if _, ok := s.ZhuyinKeys[k]; !ok {
				continue
			}
			delete(s.ZhuyinKeys, k)
		}
		m.Gen++
	}
}
