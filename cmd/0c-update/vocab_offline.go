package main

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"git.biggo.com/0c/ime/core/engine"
	"git.biggo.com/0c/ime/core/vocab"
	"git.biggo.com/0c/ime/internal/autoupdate"
	"git.biggo.com/0c/ime/internal/datadir"
	"git.biggo.com/0c/ime/internal/enginedir"
	"git.biggo.com/0c/ime/internal/userlock"
)

// 引擎沒有執行時的詞彙指令（#120 的退路）：引擎服務化之前（或服務管理員不可用時），設定程式、
// 腳本與測試照樣能改自訂詞彙。先拿個人資料目錄的寫入者鎖（與 0c-server 同一把，internal/userlock），
// 拿到了才直接讀寫 vocab.json；拿不到表示有引擎在用，改交給引擎。引擎啟動時等這把鎖，
// 放開後才讀檔，所以不會有兩個寫入者。
//
// 限制：離線加詞只改 vocab.json，不碰學習資料（user.json）：加詞取代以前不一致手選的那一步（personal.vocabChanged）
// 要等引擎執行時才會發生；刪詞也不會留下 tombstone。標注音與排名載入資料包計算（第一次約一兩百毫秒）。

var errEngineBusy = errors.New("另一個引擎正在使用個人資料")

// offlineVocabOp：不需要引擎狀態、可以只靠 vocab.json 與資料包回答的指令。
func offlineVocabOp(op string) bool {
	switch op {
	case engine.VocabLearned, engine.VocabKeep, engine.VocabForget:
		return false // 要讀寫學習資料，只能交給引擎
	}
	return true
}

// vocabOffline 在寫入者鎖內執行一個詞彙指令；修改了就原子寫回 vocab.json。
func vocabOffline(dial autoupdate.Dialer, base string, req engine.VocabRequest) (engine.VocabResponse, error) {
	if !offlineVocabOp(req.Op) {
		return engine.VocabResponse{}, errors.New("引擎沒有在執行，學到的詞要在輸入法執行時才能管理；切換到靈犀注音打一個字後再試")
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return engine.VocabResponse{}, err
	}
	lock, err := userlock.TryLock(base)
	if errors.Is(err, userlock.ErrBusy) {
		return engine.VocabResponse{}, errEngineBusy
	}
	if err != nil {
		lock = nil // 檔案系統不支援鎖：照舊寫（同 0c-server 的做法）
	}
	defer lock.Release()
	// 拿到鎖之前引擎可能剛好起來又在等鎖：它會在我們放開之後才讀檔，看得到這次的修改。
	e := engine.New(nil, nil)
	if needsLexicon(req.Op) {
		if data := dataPackDir(base); data != "" {
			if full, err := datadir.Open(data); err == nil {
				e = full
			}
		}
	}
	path := filepath.Join(base, datadir.VocabFileName)
	if err := datadir.LoadVocab(e, path); err != nil {
		var newer *vocab.NewerFormatError
		if errors.As(err, &newer) {
			return engine.VocabResponse{}, errors.New("自訂詞彙檔由較新版本寫成，請更新到最新版再修改")
		}
		return engine.VocabResponse{}, err
	}
	e.Model.Now = func() int64 { return time.Now().Unix() }
	resp := e.Vocab(req)
	if resp.Changed {
		if err := datadir.SaveVocab(e, path); err != nil {
			return engine.VocabResponse{}, err
		}
	}
	return resp, nil
}

func needsLexicon(op string) bool { return op == engine.VocabAnnotate || op == engine.VocabRank }

// dataPackDir 是目前安裝版本的資料包：核心更新的指標優先，沒有指標時用各平台完整安裝的位置。
func dataPackDir(base string) string {
	if _, data, ok := enginedir.Resolve(base); ok {
		return data
	}
	if data := installedDataDir(base); data != "" {
		if _, err := os.Stat(filepath.Join(data, "manifest.json")); err == nil {
			return data
		}
	}
	return ""
}
