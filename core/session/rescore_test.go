package session

import (
	"errors"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
)

// asyncSession 模擬外殼的非同步評分器：每個請求第一次回錯誤（還在算），ready 之後回分數，偏好含「井」的候選。
func asyncSession(ready *bool, calls *int) *Session {
	s := newSession()
	s.Opt.PrefixAware = false
	s.Rerank = rerank.Options{Weight: .1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		*calls++
		if !*ready {
			return nil, errors.New("pending")
		}
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if strings.Contains(c.Text, "井") {
				scores[i] = 1000
			}
		}
		return scores, nil
	})}
	return s
}

func TestRescoreWithoutScorerIsNoop(t *testing.T) {
	s := newSession()
	s.Opt.PrefixAware = false
	typeSyl(s, "ㄨㄛˇ")
	before := typeSyl(s, "ㄐㄧㄥˇ").Text()
	if s.Rescore() {
		t.Fatal("沒有評分器時不該重排")
	}
	if got := s.View().Text(); got != before {
		t.Fatalf("畫面變了：%q → %q", before, got)
	}
}

func TestRescoreAdoptsAsyncScores(t *testing.T) {
	ready, calls := false, 0
	s := asyncSession(&ready, &calls)
	typeSyl(s, "ㄨㄛˇ")
	stat := typeSyl(s, "ㄐㄧㄥˇ").Text()
	if stat == "我井" || calls == 0 {
		t.Fatalf("評分器還在算時應顯示統計首選：%q calls=%d", stat, calls)
	}
	ready = true
	n := calls
	if !s.Rescore() {
		t.Fatal("評分器算完後應重排")
	}
	if got := s.View().Text(); got != "我井" || calls == n {
		t.Fatalf("重排後應換成模型首選：%q calls=%d→%d", got, n, calls)
	}
	cursor := s.cursor
	if s.Rescore(); s.cursor != cursor || s.View().Text() != "我井" {
		t.Fatal("再呼叫一次不該改變游標或文字")
	}
}

func TestRescoreKeepsOpenCandidates(t *testing.T) {
	ready, calls := false, 0
	s := asyncSession(&ready, &calls)
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	open := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	if open.Candidates == nil {
		t.Fatal("候選沒有開")
	}
	ready = true
	n := calls
	if s.Rescore() {
		t.Fatal("候選開著時不該重排")
	}
	got := s.View()
	if got.Text() != open.Text() || got.Candidates == nil || calls != n {
		t.Fatalf("候選開著時畫面變了：%q → %q calls=%d→%d", open.Text(), got.Text(), n, calls)
	}
}

func TestRescoreDoesNotAdoptLaterLearning(t *testing.T) {
	ready, calls := false, 0
	s := asyncSession(&ready, &calls)
	typeSyl(s, "ㄨㄛˇ")
	stat := typeSyl(s, "ㄐㄧㄥˇ").Text()
	ready = true
	s.model.Gen++ // 別的欄位學到東西：照神經畫面保留規則，已顯示的文字不換，等下次改音節。
	if s.Rescore() || s.View().Text() != stat {
		t.Fatalf("模型變過後不該重排：%q", s.View().Text())
	}
	if got := typeSyl(s, "ㄉㄜ˙").Text(); !strings.HasPrefix(got, "我井") {
		t.Fatalf("下次改音節應照常採用評分：%q", got)
	}
}
