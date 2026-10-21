package engine

import (
	"strings"

	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/vocab"
)

// 「靈犀學到的」頁的移除、不再學、恢復學習與復原（#182）。學到的詞存在 user.json（學習資料），
// 不是 vocab.json 的批次：復原靠操作前留下的複本（personal.Undo），只留在引擎記憶體裡。

const (
	maxLearnedUndo = 20 // 最多留幾次操作的複本
	// learnedUndoTTL（秒）：複本裡有使用者要求刪除的學習，不無限期留著。下一次操作時、以及宿主定時呼叫
	// ExpireLearnedUndo 時清掉過期的。宿主沒給時鐘（Model.Now 為 nil）時操作時間記為 0、不會過期，
	// 最多 maxLearnedUndo 份留到行程結束；0c-server 有給時鐘，並在存檔的計時裡每秒清一次。
	learnedUndoTTL = 10 * 60
)

type learnedUndo struct {
	id uint64
	at int64
	u  *personal.Undo
}

// LearningOp 回報這個詞彙指令改的是學習資料（user.json），宿主要照輸入法的忘記處理（盡快存檔），
// 而不是存 vocab.json。
func LearningOp(op string) bool {
	switch op {
	case VocabForget, VocabUnlearn, VocabRelearn, VocabLearnedUndo:
		return true
	}
	return false
}

func (e *Engine) learnedOp(req VocabRequest, resp *VocabResponse) error {
	m := e.Model
	now := e.expireLearnedUndo()
	defer func() { resp.ForgottenCount = len(m.Forgotten()) }()
	switch req.Op {
	case VocabForgotten:
		resp.Forgotten = m.Forgotten()
		return nil
	case VocabLearnedUndo:
		for i, x := range e.learnedUndo {
			if x.id == req.Undo {
				e.learnedUndo = append(e.learnedUndo[:i:i], e.learnedUndo[i+1:]...)
				if m.Restore(x.u) {
					resp.Changed = true
					return nil
				}
				break
			}
		}
		return &vocab.Error{Code: vocab.CodeNotFound, Item: -1, Msg: "已經無法復原（太久以前的操作，或輸入法引擎重新啟動過）"}
	}
	var texts []string
	for _, t := range req.Texts {
		if t = strings.TrimSpace(t); t != "" {
			texts = append(texts, t)
		}
	}
	if len(texts) == 0 {
		return &vocab.Error{Code: vocab.CodeEmpty, Item: -1, Msg: "沒有選任何詞"}
	}
	u := m.Undoable(func() {
		for _, t := range texts {
			switch req.Op {
			case VocabForget:
				m.Forget(t)
			case VocabUnlearn:
				m.Unlearn(t)
			case VocabRelearn:
				m.Relearn(t)
			}
		}
	})
	if u == nil {
		return nil // 沒有可以改的（例如已經恢復過）
	}
	e.undoSerial++
	e.learnedUndo = append(e.learnedUndo, learnedUndo{id: e.undoSerial, at: now, u: u})
	if n := len(e.learnedUndo); n > maxLearnedUndo {
		e.learnedUndo = append([]learnedUndo(nil), e.learnedUndo[n-maxLearnedUndo:]...)
	}
	resp.Changed, resp.Undo = true, e.undoSerial
	return nil
}

// ExpireLearnedUndo 清掉超過 learnedUndoTTL 的復原複本。使用者操作完不再動「靈犀學到的」頁時，
// 複本不會等到下一次操作才放掉；宿主（0c-server）定時呼叫。沒有複本時只拿一下鎖。
func (e *Engine) ExpireLearnedUndo() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.expireLearnedUndo()
}

// expireLearnedUndo 在引擎鎖內清掉過期的複本，回傳現在的時間。
func (e *Engine) expireLearnedUndo() int64 {
	now := e.now()
	if len(e.learnedUndo) > 0 {
		e.learnedUndo = filterUndo(e.learnedUndo, func(x learnedUndo) bool { return x.at == 0 || now-x.at <= learnedUndoTTL })
	}
	return now
}

// filterUndo 就地留下 keep 的項目；後面空出來的位置清成零值，被丟掉的複本才真的放掉（不留在底層陣列裡）。
func filterUndo(xs []learnedUndo, keep func(learnedUndo) bool) []learnedUndo {
	out := xs[:0]
	for _, x := range xs {
		if keep(x) {
			out = append(out, x)
		}
	}
	clear(xs[len(out):])
	return out
}
