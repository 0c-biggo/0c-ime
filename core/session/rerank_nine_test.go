package session

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
)

// 神經選字幫九宮格挑拼法：宣告 TextAnyLength 的評分器收到有多種拼法的九宮格段落時是文字評分。

// recordingScorer 記下每個請求的 JSON（送給評分器的位元組），分數偏好 prefer 開頭的候選。
func recordingScorer(log *[]string, prefer string) rankFunc {
	return func(r rerank.Request) ([]float64, error) {
		b, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		*log = append(*log, string(b))
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if prefer != "" && strings.HasPrefix(c.Text, prefer) {
				scores[i] = 1
			}
		}
		return scores, nil
	}
}

// 沒有九宮格位置的段落：宣告與否，送給評分器的每個請求逐位元組相同，結果也相同（含單音節文字評分）。
func TestNineSpellingTextLeavesOtherRequestsAlone(t *testing.T) {
	runs := []struct {
		name string
		open func() *Session
		typ  func(*Session) proto.Snapshot
	}{
		{"toned", newSession, func(s *Session) proto.Snapshot {
			typeSyl(s, "ㄨㄛˇ")
			typeSyl(s, "ㄒㄧㄤˇ")
			return typeSyl(s, "ㄐㄧㄥˇ")
		}},
		{"single-with-context", newSession, func(s *Session) proto.Snapshot {
			s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
			s.neuralLeft = "好"
			return typeSyl(s, "ㄐㄧㄥˇ")
		}},
		{"single-without-context", newSession, func(s *Session) proto.Snapshot { return typeSyl(s, "ㄐㄧㄥˇ") }},
		{"toneless", func() *Session { return newTonelessSession(true) }, func(s *Session) proto.Snapshot { return typeBare(s, "ㄏㄨㄟㄧ ㄒㄧㄢㄗㄞ") }},
		{"abbreviation", func() *Session { return newAbbreviationSession(true) }, func(s *Session) proto.Snapshot { return typeBare(s, "ㄏㄨㄟㄧ") }},
		{"nine-then-literal", func() *Session { return newTonelessSession(true) }, func(s *Session) proto.Snapshot {
			press(s, 7, 2, 0, 8, 8, 0) // 九宮格的「你好」，接英數 1（解碼邊界），再用一般注音鍵打 ㄏㄨㄟ ㄧ
			s.Handle(proto.Request{Type: proto.Toggle})
			key(s, "1")
			s.Handle(proto.Request{Type: proto.Toggle})
			return typeBare(s, "ㄏㄨㄟㄧ")
		}},
	}
	for _, r := range runs {
		var want []string
		var wantText, wantCommit string
		for _, declared := range []bool{false, true} {
			var log []string
			s := r.open()
			s.Opt.PrefixAware = false
			s.Rerank = rerank.Options{UnknownTones: true, Weight: 1, TextWeight: 2, TextMargin: 1, ContextRunes: rerank.MaxContextRunes,
				TextAnyLength: declared, Scorer: recordingScorer(&log, "井")}
			text := r.typ(s).Text()
			commit := s.Handle(proto.Request{Type: proto.Commit}).Commit
			if r.name == "nine-then-literal" {
				// 九宮格那一段宣告時會變，只比英數後面一般注音那一段的請求。
				log = slices.DeleteFunc(log, func(b string) bool { return !strings.Contains(b, `"reading":["ㄏㄨㄟ","ㄧ"]`) })
				text, commit = "", ""
			}
			if !declared {
				want, wantText, wantCommit = log, text, commit
				if len(want) == 0 && r.name != "single-without-context" {
					t.Fatalf("%s: no requests", r.name)
				}
				continue
			}
			if !slices.Equal(log, want) || text != wantText || commit != wantCommit {
				t.Fatalf("%s: requests or result changed when declared\n got %q %q %v\nwant %q %q %v", r.name, text, commit, log, wantText, wantCommit, want)
			}
		}
	}
}

// 沒宣告時九宮格段落的請求跟以前逐位元組相同（讀音評分，讀音是統計首選的拼法），評分器換不了拼法。
// 期望值是 9e44d091（加這個功能之前）同一個測試送出的位元組。
func TestNineSpellingTextOffIsUnchanged(t *testing.T) {
	var log []string
	s := nineSession()
	s.Opt.PrefixAware = false
	s.Rerank = rerank.Options{UnknownTones: true, Weight: 100, Scorer: recordingScorer(&log, "米")}
	press(s, 7, 2, 0, 8, 8, 0)
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "你好" {
		t.Fatalf("commit %q", got)
	}
	want := []string{nineOffRequest}
	if !slices.Equal(log, want) {
		t.Fatalf("requests\n got %q\nwant %q", log, want)
	}
}

const nineOffRequest = `{"revision":6,"reading":["ㄋㄧ","ㄏㄠ"],"candidates":[{"text":"你好","log_score":-7.418580902748127,"segments":[{"Word":"你好","Start":0,"End":2,"Partial":false,"Correction":false,"Matched":""}]},{"text":"泥好","log_score":-13.004580341747944,"segments":[{"Word":"泥","Start":0,"End":1,"Partial":false,"Correction":false,"Matched":""},{"Word":"好","Start":1,"End":2,"Partial":false,"Correction":false,"Matched":""}]},{"text":"米好","log_score":-13.227723893062155,"segments":[{"Word":"米","Start":0,"End":1,"Partial":false,"Correction":false,"Matched":""},{"Word":"好","Start":1,"End":2,"Partial":false,"Correction":false,"Matched":""}]},{"text":"買好","log_score":-13.515405965513935,"segments":[{"Word":"買","Start":0,"End":1,"Partial":false,"Correction":false,"Matched":""},{"Word":"好","Start":1,"End":2,"Partial":false,"Correction":false,"Matched":""}]},{"text":"咪好","log_score":-13.920871073622099,"segments":[{"Word":"咪","Start":0,"End":1,"Partial":false,"Correction":false,"Matched":""},{"Word":"好","Start":1,"End":2,"Partial":false,"Correction":false,"Matched":""}]},{"text":"妮好","log_score":-14.614018254182046,"segments":[{"Word":"妮","Start":0,"End":1,"Partial":false,"Correction":false,"Matched":""},{"Word":"好","Start":1,"End":2,"Partial":false,"Correction":false,"Matched":""}]},{"text":"奶好","log_score":-14.614018254182046,"segments":[{"Word":"奶","Start":0,"End":1,"Partial":false,"Correction":false,"Matched":""},{"Word":"好","Start":1,"End":2,"Partial":false,"Correction":false,"Matched":""}]}],"tone_known":[false,false]}`

// 宣告時：九宮格段落送文字評分，候選跨拼法，前文是空的，Reading 是統計首選的拼法（只表示字數）；
// 評分器偏好另一個拼法的字就換過去。自動切字開或關都一樣。
func TestNineSpellingTextPicksAnotherSpelling(t *testing.T) {
	for _, segment := range []bool{false, true} {
		var log []string
		s := nineSession()
		s.NineSegment = segment
		s.Opt.PrefixAware = false
		s.Rerank = rerank.Options{UnknownTones: true, Weight: 100, TextAnyLength: true, Scorer: recordingScorer(&log, "米")}
		if segment {
			press(s, 7, 2, 8, 8) // 不按聲調鍵，由核心切
		} else {
			press(s, 7, 2, 0, 8, 8, 0)
		}
		if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "米好" {
			t.Fatalf("segment=%v: commit %q, requests %v", segment, got, log)
		}
		var r rerank.Request
		if err := json.Unmarshal([]byte(log[len(log)-1]), &r); err != nil {
			t.Fatal(err)
		}
		var texts []string
		for _, c := range r.Candidates {
			texts = append(texts, c.Text)
		}
		if r.Mode != rerank.ModeText || r.Context != "" || strings.Join(texts, " ") == "" ||
			!slices.Contains(texts, "你好") || !slices.Contains(texts, "米好") || len(r.Reading) != 2 || r.Reading[0] != "ㄋㄧ" {
			t.Fatalf("segment=%v: request %s", segment, log[len(log)-1])
		}
	}
}

// 九宮格音節的鍵只拼得出一個讀音（或固定了拼法）時沒有拼法可挑，照讀音評分。
func TestNineSpellingTextNeedsAChoice(t *testing.T) {
	var log []string
	s := nineSession()
	s.Opt.PrefixAware = false
	s.Rerank = rerank.Options{UnknownTones: true, Weight: 100, TextAnyLength: true, Scorer: recordingScorer(&log, "米")}
	press(s, 7, 2, 0, 8, 8, 0)
	s.Handle(proto.Request{Type: proto.NineReading, To: 0, Word: "ㄋㄧ"}) // 固定第一個音節
	s.Handle(proto.Request{Type: proto.NineReading, To: 1, Word: "ㄏㄠ"})
	log = nil
	s.Rescore()
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "你好" {
		t.Fatalf("commit %q", got)
	}
	for _, b := range log {
		if strings.Contains(b, `"mode"`) {
			t.Fatalf("text mode without a spelling to pick: %s", b)
		}
	}
}
