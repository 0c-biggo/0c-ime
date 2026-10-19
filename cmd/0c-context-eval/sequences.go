package main

import (
	"encoding/json"
	"fmt"
	"os"

	"git.biggo.com/0c/ime/core/engine"
	"git.biggo.com/0c/ime/core/layout"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/datadir"
	"git.biggo.com/0c/ime/internal/eval"
)

type sequenceCase struct {
	ID         string `json:"id"`
	NoLearning bool   `json:"no_learning"`
	Steps      []struct {
		Reading string `json:"reading"`
		Select  *struct {
			At   int    `json:"at"`
			Text string `json:"text"`
		} `json:"select"`
		Request *proto.Request `json:"request"`
		Want    string         `json:"want"`
	} `json:"steps"`
}

func runSequences(data, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cases []sequenceCase
	if err = json.Unmarshal(b, &cases); err != nil {
		return err
	}
	loaded, err := datadir.Open(data)
	if err != nil {
		return err
	}
	provenance := eval.Provenance(data)
	failures := 0
	for _, c := range cases {
		e := engine.New(loaded.Model.Lex, loaded.Model.LM)
		id := e.OpenSession()
		do := func(r proto.Request) proto.Snapshot {
			s, err := e.Do(id, r)
			if err != nil {
				panic(err)
			}
			return s
		}
		var steps []map[string]any
		pass := true
		for i, step := range c.Steps {
			var snap proto.Snapshot
			for _, y := range zhuyin.ParseReading(step.Reading) {
				if !y.Legal() {
					return fmt.Errorf("%s step %d 非法讀音 %s", c.ID, i, y)
				}
				for _, k := range layout.Standard.Keys(y) {
					snap = do(proto.Request{Type: proto.Key, Key: string(k)})
				}
			}
			if selection := step.Select; selection != nil {
				do(proto.Request{Type: proto.Cursor, To: selection.At})
				snap = do(proto.Request{Type: proto.Candidates, Length: len([]rune(selection.Text))})
				found := false
				for snap.Candidates != nil {
					cl := snap.Candidates
					for _, v := range cl.Items {
						if v.Text == selection.Text {
							snap = do(proto.Request{Type: proto.Select, Index: v.ID})
							found = true
							break
						}
					}
					if found || cl.Page+1 >= cl.Pages {
						break
					}
					snap = do(proto.Request{Type: proto.Page, Index: cl.Page + 1})
				}
				if !found {
					return fmt.Errorf("%s step %d 找不到候選 %s", c.ID, i, selection.Text)
				}
				// 這是語意重播，不把 cursor API 或跳頁當作一個真實按鍵。
				snap = do(proto.Request{Type: proto.Cursor, To: 1 << 30})
			}
			if step.Request != nil {
				snap = do(*step.Request)
			}
			got := snap.Text()
			if snap.Commit != "" {
				got = snap.Commit
			}
			ok := step.Want == "" || got == step.Want
			pass = pass && ok
			steps = append(steps, map[string]any{"step": i, "shown": got, "want": step.Want, "checked": step.Want != "", "pass": ok})
		}
		if c.NoLearning && e.Model.Store.Total != 0 {
			pass = false
		}
		if !pass {
			failures++
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"id": c.ID, "runtime": provenance, "personal_loaded": false, "semantic_selection": true, "steps": steps, "learned_weight": e.Model.Store.Total, "pass": pass}); err != nil {
			return err
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d 個更正操作案例未通過", failures)
	}
	return nil
}
