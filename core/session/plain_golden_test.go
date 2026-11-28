package session_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/session"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 沒開「自動切字」（Session.NineSegment）時，session 的每個快照跟加入自動切字之前逐一相同。
//
// testdata/plain-golden.json 是 main dee4f8ed（沒有自動切字）的 session 對同一串請求回的快照：亂數詞庫上，三種設定（一般注音、
// 聲調選填、手機的聲調選填＋簡拼＋混用）各打 60 段，混著注音鍵、聲調、九宮格的鍵與聲調鍵、左欄的 nine_reading、
// 開候選、選字、fix、移游標、退格與送出；每一筆是那一段所有快照（含 PreviewPending）JSON 的雜湊。
//
// 重新產生（只在要換基準時；必須用加入自動切字之前的程式碼跑）：
//
//	ZERO_C_WRITE_GOLDEN=$PWD/core/session/testdata/plain-golden.json go test ./core/session -run TestPlainSessionGolden
func TestPlainSessionGolden(t *testing.T) {
	got := plainSessionGolden()
	if path := os.Getenv("ZERO_C_WRITE_GOLDEN"); path != "" {
		data, _ := json.MarshalIndent(got, "", " ")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile("testdata/plain-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(got) {
		t.Fatalf("%d cases, golden has %d", len(got), len(want))
	}
	bad := 0
	for i := range got {
		if got[i] != want[i] {
			bad++
			if bad <= 5 {
				t.Errorf("case %d differs", i)
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d cases differ", bad, len(got))
	}
}

var goldenNineGroups = []string{"ㄅㄉㄚ", "ㄍㄐㄞㄧ", "ㄓㄗㄢㄦ", "ㄆㄊㄛ", "ㄎㄑㄟㄨ", "ㄔㄘㄣ", "ㄇㄋㄜㄝ", "ㄏㄒㄠㄩ", "ㄕㄙㄤㄥ", "ㄈㄌㄡㄖ"}

func goldenNineKey(r rune) string {
	for _, g := range goldenNineGroups {
		if strings.ContainsRune(g, r) {
			return g
		}
	}
	return string(r)
}

// goldenSessionModel 是亂數詞庫（同 core/decode 的 plain_golden_test：常用音節的單字與 2–4 字詞）。
func goldenSessionModel() (*personal.Model, []lexicon.Entry) {
	rng := rand.New(rand.NewSource(51))
	bases := zhuyin.Inventory()
	slices.Sort(bases)
	var syls []zhuyin.Syllable
	for _, b := range bases {
		if len(syls) == 120 {
			break
		}
		if rng.Intn(4) == 0 {
			syls = append(syls, b.WithTone(1+rng.Intn(5)))
		}
	}
	lex, sys := lexicon.NewMem(), lm.NewMem()
	var words, singles []lexicon.Entry
	char := 0x4E00
	add := func(surface string, reading []zhuyin.Syllable) lexicon.Entry {
		e := lexicon.Entry{Surface: surface, Reading: reading, PRW: []float64{1, 1, 0.5}[rng.Intn(3)]}
		lex.Add(e)
		sys.SetUni(surface, math.Exp(-4-8*rng.Float64()))
		words = append(words, e)
		return e
	}
	for _, y := range syls {
		for range 1 + rng.Intn(3) {
			singles = append(singles, add(string(rune(char)), []zhuyin.Syllable{y}))
			char++
		}
	}
	for range 1500 {
		n := 2 + rng.Intn(3)
		var surface strings.Builder
		var reading []zhuyin.Syllable
		for range n {
			s := singles[rng.Intn(len(singles))]
			surface.WriteString(s.Surface)
			reading = append(reading, s.Reading...)
		}
		add(surface.String(), reading)
	}
	for range 3000 {
		a, b := words[rng.Intn(len(words))], words[rng.Intn(len(words))]
		sys.SetBi(a.Surface, b.Surface, math.Exp(-1-5*rng.Float64()))
	}
	return personal.NewModel(lex, sys), words
}

// withoutReadingLabel 拿掉原樣注音候選的 proto.CandidateReading 標示（#361 加的，dee4f8ed 還沒有）再算雜湊：
// 基準仍是 dee4f8ed 的快照，其餘欄位與候選順序都要逐一相同。
func withoutReadingLabel(snap proto.Snapshot) proto.Snapshot {
	snap.Candidates = listWithoutReadingLabel(snap.Candidates)
	return snap
}

func listWithoutReadingLabel(c *proto.CandidateList) *proto.CandidateList {
	if c == nil {
		return nil
	}
	list := *c
	list.Items = slices.Clone(list.Items)
	for i := range list.Items {
		if list.Items[i].Label == proto.CandidateReading {
			list.Items[i].Label = ""
		}
	}
	return &list
}

func plainSessionGolden() []string {
	var out []string
	for config := range 3 {
		m, words := goldenSessionModel()
		rng := rand.New(rand.NewSource(int64(100 + config)))
		s := session.New(m)
		switch config {
		case 1:
			s.Toneless = true
		case 2:
			s.Toneless, s.Abbreviation, s.MixedWithToneless = true, true, true
			s.CandidateBeforeCursor = true
		}
		for range 60 {
			var log []any
			var snap proto.Snapshot
			do := func(r proto.Request) {
				snap = s.Handle(r)
				log = append(log, r, withoutReadingLabel(snap))
				if config > 0 {
					log = append(log, listWithoutReadingLabel(s.PreviewPending()))
				}
			}
			for range 1 + rng.Intn(3) {
				e := words[rng.Intn(len(words))]
				nine := config > 0 && rng.Intn(2) == 0
				for _, y := range e.Reading {
					switch {
					case nine:
						for _, r := range string(y.Base()) {
							do(proto.Request{Type: proto.Symbol, Group: goldenNineKey(r)})
						}
						switch rng.Intn(4) {
						case 0:
							do(proto.Request{Type: proto.Symbol, Symbol: "ˉ"})
						case 1:
							tone := strings.TrimPrefix(string(y), string(y.Base()))
							if tone == "" {
								tone = "ˉ"
							}
							do(proto.Request{Type: proto.Symbol, Symbol: tone})
						default:
							do(proto.Request{Type: proto.Symbol, Group: "ˊˇˋ˙"})
						}
					default:
						typed := string(y)
						if config > 0 && rng.Intn(2) == 0 {
							typed = string(y.Base()) // 沒打聲調
							if config == 2 && rng.Intn(3) == 0 {
								typed = string([]rune(typed)[:1]) // 簡拼
							}
						}
						do(proto.Request{Type: proto.Symbol, Symbol: typed})
					}
				}
				switch rng.Intn(8) {
				case 0:
					do(proto.Request{Type: proto.Candidates, Length: rng.Intn(3)})
					if snap.Candidates != nil && len(snap.Candidates.Items) > 0 {
						do(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[rng.Intn(len(snap.Candidates.Items))].ID})
					}
				case 1:
					if r := snap.NineReadings; r != nil && len(r.Readings) > 0 {
						do(proto.Request{Type: proto.NineReading, To: r.At, Word: r.Readings[rng.Intn(len(r.Readings))]})
					}
				case 2:
					if text := snap.Text(); text != "" {
						n := utf8.RuneCountInString(text)
						at := rng.Intn(n)
						length := 1 + rng.Intn(min(3, n-at))
						w := words[rng.Intn(len(words))].Surface
						if utf8.RuneCountInString(w) != length {
							w = string([]rune(text)[at : at+length])
						}
						do(proto.Request{Type: proto.Fix, To: at, Length: length, Word: w})
					}
				case 3:
					do(proto.Request{Type: proto.Backspace})
				case 4:
					do(proto.Request{Type: proto.Cursor, To: rng.Intn(5)})
				case 5:
					do(proto.Request{Type: proto.Key, Key: proto.KeyLeft})
				}
			}
			if rng.Intn(3) == 0 {
				do(proto.Request{Type: proto.Key, Key: proto.KeyEnter})
			} else {
				do(proto.Request{Type: proto.Commit})
			}
			data, err := json.Marshal(log)
			if err != nil {
				panic(err)
			}
			sum := sha256.Sum256(data)
			out = append(out, hex.EncodeToString(sum[:8]))
		}
	}
	return out
}
