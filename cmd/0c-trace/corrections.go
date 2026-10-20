package main

import (
	"encoding/json"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 只判斷一次明確選取的區間，不把其餘未改文字或普通提交標成正確答案。
type correction struct {
	Run     string `json:"run"`
	Session uint64 `json:"session"`
	Seq     uint64 `json:"seq"`
	Start   int    `json:"start"`
	Before  string `json:"rejected"`
	After   string `json:"selected"`
}

func selectedCorrection(e event) (correction, bool) {
	c := e.Before.Candidates
	if e.Event != "request" || c == nil || c.Length <= 0 || e.After.Candidates != nil || e.After.Commit != "" {
		return correction{}, false
	}
	var r proto.Request
	if json.Unmarshal(e.Request, &r) != nil || r.Private {
		return correction{}, false
	}
	id := -1
	if r.Type == proto.Select {
		id = r.Index
	}
	if r.Type == proto.Key {
		if r.Key == proto.KeyEnter && c.Highlight >= 0 && c.Highlight < len(c.Items) {
			id = c.Items[c.Highlight].ID
		}
		if len(r.Key) == 1 && r.Key[0] >= '0' && r.Key[0] <= '9' {
			i := (int(r.Key[0]-'0') + 9) % 10
			if i < len(c.Items) {
				id = c.Items[i].ID
			}
		}
	}
	chosen := ""
	for _, v := range c.Items {
		if v.ID == id {
			chosen = v.Text
		}
	}
	for _, s := range e.Before.Preedit {
		if s.Style == proto.StylePending || s.Style == proto.StyleReading {
			return correction{}, false
		}
	}
	a, b := []rune(e.Before.Text()), []rune(e.After.Text())
	if chosen == "" || len([]rune(chosen)) != c.Length || c.Start < 0 || c.Start+c.Length > len(a) || len(a) != len(b) {
		return correction{}, false
	}
	for _, x := range chosen {
		if !zhuyin.IsHan(x) {
			return correction{}, false
		}
	}
	old := string(a[c.Start : c.Start+c.Length])
	now := string(b[c.Start : c.Start+c.Length])
	if now != chosen || old == now {
		return correction{}, false
	}
	return correction{e.Run, e.Session, e.Seq, c.Start, old, now}, true
}
