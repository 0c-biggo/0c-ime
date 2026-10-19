package datadir

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"git.biggo.com/0c/ime/core/engine"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
)

func TestSaveUserReplacesFileAndLeavesNoTemp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	if err := os.WriteFile(path, []byte("舊資料"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &engine.Engine{Model: &personal.Model{Store: personal.NewStore()}}
	e.Model.Store.Phrases["陳怡君"] = "ㄔㄣˊ ㄧˊ ㄐㄩㄣ"
	if err := SaveUser(e, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("暫存檔還在：%v", err)
	}
	loaded := &engine.Engine{Model: &personal.Model{Store: personal.NewStore()}}
	if err := LoadUser(loaded, path); err != nil {
		t.Fatal(err)
	}
	if got := loaded.Model.Store.Phrases["陳怡君"]; got != "ㄔㄣˊ ㄧˊ ㄐㄩㄣ" {
		t.Fatalf("讀回 %q", got)
	}
}

// 背景存檔與提交同時進行時不能同時讀寫個人資料的 map（go test -race 會抓到）。
func TestSaveUserWhileCommitting(t *testing.T) {
	lf, err := os.Open("../../testdata/toy/lexicon.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	lex, err := lexicon.Load(lf)
	if err != nil {
		t.Fatal(err)
	}
	mf, err := os.Open("../../testdata/toy/lm.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer mf.Close()
	sys, err := lm.Load(mf)
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(lex, sys)
	id := e.OpenSession()
	path := filepath.Join(t.TempDir(), "user.json")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			// 忘記一個詞會在引擎鎖內寫入個人資料的 map（墓碑），跟提交學習一樣。
			e.Do(id, proto.Request{Type: proto.Forget, Word: fmt.Sprint("詞", i)})
		}
	}()
	for {
		if err := SaveUser(e, path); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
			return
		default:
		}
	}
}
