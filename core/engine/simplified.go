package engine

import (
	"fmt"

	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhconv"
)

// SimplifiedSwitch 是從輸入法選單即時切換「輸出簡體字」需要的宿主功能（#169）。兩者都由宿主提供，
// 核心不讀檔：Load 在第一次開啟時解析轉換字典（之後回傳同一份），Save 把新的值寫進設定檔。
type SimplifiedSwitch struct {
	Load func() (*zhconv.Converter, error)
	Save func(on bool) error
}

// 切換後快照帶的提示；外殼照原樣顯示。
const (
	NoticeSimplifiedOn   = "已切換為輸出簡體字"
	NoticeSimplifiedOff  = "已切換為輸出繁體字"
	NoticeSimplifiedFail = "簡體字典無法載入，維持輸出繁體字"
	noticeNotSaved       = "（設定沒有存起來，輸入法重新啟動後會回到原本的設定）"
)

// OutputSimplified 回報目前送出的文字是否轉成簡體。
func (e *Engine) OutputSimplified() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Simplified != nil
}

// outputSimplified 處理 proto.OutputSimplified（#169）：換掉引擎與所有 session 的轉換器，之後每個輸入框送出的
// 文字（包括已經在組字區的）照新的設定；再請宿主寫進設定檔。組字、候選與學習都不動。
// 宿主沒有提供 SimplifiedSwitch 時（舊的用法、測試）回 handled=false、什麼都不改，外殼退回原本的設定流程。
func (e *Engine) outputSimplified(id uint64, req proto.Request) (proto.Snapshot, error) {
	e.switchMu.Lock() // 兩個輸入框同時切換時，引擎的狀態與設定檔以後到的那一次為準
	defer e.switchMu.Unlock()
	sw := e.SimplifiedSwitch
	live := sw.Load != nil && sw.Save != nil
	var conv *zhconv.Converter
	var loadErr error
	if live && req.On {
		conv, loadErr = sw.Load() // 鎖外解析：第一次約 1 ms，不擋住其他輸入框的按鍵
		if loadErr == nil && conv == nil {
			loadErr = fmt.Errorf("沒有轉換器")
		}
	}
	e.mu.Lock()
	if _, ok := e.sessions[id]; !ok {
		e.mu.Unlock()
		return proto.Snapshot{}, fmt.Errorf("session %d 不存在", id)
	}
	switched := live && loadErr == nil
	if switched {
		e.Simplified = conv
		for _, s := range e.sessions {
			s.Simplified = conv
		}
	}
	e.mu.Unlock()
	var saveErr error
	if switched {
		saveErr = sw.Save(req.On) // 寫檔不在引擎鎖內：別的輸入框照常打字
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[id]
	if !ok {
		return proto.Snapshot{}, fmt.Errorf("session %d 不存在", id)
	}
	snap := s.Handle(req)
	switch {
	case !live:
		snap.Handled = false
	case loadErr != nil:
		snap.Notices = append(snap.Notices, NoticeSimplifiedFail)
	default:
		notice := NoticeSimplifiedOff
		if req.On {
			notice = NoticeSimplifiedOn
		}
		if saveErr != nil {
			notice += noticeNotSaved
		}
		snap.Notices = append(snap.Notices, notice)
	}
	return snap, nil
}
