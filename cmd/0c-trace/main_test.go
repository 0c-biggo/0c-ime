package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.biggo.com/0c/ime/core/proto"
)

func TestReadRotatedAndIncompleteEvents(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{
		"trace.1.jsonl": "{\"event\":\"request\",\"unix_us\":100,\"seq\":1}\n",
		"trace.jsonl":   "{\"event\":\"request\",\"unix_us\":300,\"seq\":3}\n{\"event\":\"gap\",\"unix_us\":200,\"dropped\":2}\n{\"broken\":",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	es, size, bad, err := readEvents(dir, time.UnixMicro(150))
	if err != nil || len(es) != 2 || es[0].Event != "gap" || es[1].Seq != 3 || size == 0 || bad != 1 {
		t.Fatalf("輪替、時間篩選、順序與半行處理：%+v %d %d %v", es, size, bad, err)
	}
}

func TestNoVisibleChangeDoesNotCountCommitOrSelectionMovement(t *testing.T) {
	e := event{Before: proto.Snapshot{Mode: "zhuyin", Revision: 1}, After: proto.Snapshot{Mode: "zhuyin", Revision: 2, Handled: true}}
	if !unchanged(e) {
		t.Fatal("revision 本身不是可見改變")
	}
	e.After.Commit = "字"
	if unchanged(e) {
		t.Fatal("提交不能算沒有變化")
	}
	e.After.Commit = ""
	e.After.Candidates = &proto.CandidateList{Highlight: 1}
	if unchanged(e) {
		t.Fatal("候選變化必須被辨識")
	}
}
