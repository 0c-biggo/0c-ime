package engine

import (
	"strings"

	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/vocab"
)

// 「靈犀學到的」頁的移除、不再學、恢復學習與復原（#182）。學到的詞存在 user.json（學習資料），
// 不是 vocab.json 的批次：復原靠操作前留下的複本（personal.Undo），只留在引擎記憶體裡。

const (
	maxLearnedUndo = 20      // 最多留幾次操作的複本
	learnedUndoTTL = 10 * 60 // 秒；複本裡有使用者要求刪除的學習，不無限期留著（宿主沒給時鐘時不過期）
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
	now := e.now()
	e.learnedUndo = filterUndo(e.learnedUndo, func(x learnedUndo) bool { return x.at == 0 || now-x.at <= learnedUndoTTL })
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

func filterUndo(xs []learnedUndo, keep func(learnedUndo) bool) []learnedUndo {
	out := xs[:0]
	for _, x := range xs {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}
