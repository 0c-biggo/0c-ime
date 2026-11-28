package session_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/session"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 小鶴雙拼的快照基準（#370，同 TestPinyinSessionGolden）：亂數詞庫上打 120 段，兩種設定（不打聲調關：等聲調或空白；
// 開：逐字轉換）。混著打聲調、不打聲調、音節後的聲調數字、j q x y 後面的 ü 打 v、空白、退格（音節中與音節後）、
// 打錯的第二鍵、Esc、方向鍵、開候選選字與送出。每一筆是那一段所有快照 JSON 的雜湊；改動雙拼的行為時要確認差異是
// 預期的再重新產生：
//
//	ZERO_C_WRITE_GOLDEN=$PWD/core/session/testdata/xiaohe-golden.json go test ./core/session -run TestXiaoheSessionGolden
func TestXiaoheSessionGolden(t *testing.T) {
	// 腳本要真的打到逐字轉換後的聲調數字與 jv：不打聲調時數字沒留在組字裡、也沒響鈴的是標了聲調。
	toned, v := 0, 0
	got := xiaoheSessionGolden(func(toneless bool, r proto.Request, snap proto.Snapshot) {
		if toneless && r.Type == proto.Key && len(r.Key) == 1 && r.Key >= "1" && r.Key <= "5" && snap.Candidates == nil &&
			!snap.Bell && !strings.Contains(snap.Text(), r.Key) {
			toned++
		}
		if r.Key == "v" {
			v++
		}
	})
	if toned < 100 || v < 30 {
		t.Fatalf("腳本沒打到逐字轉換後的聲調（%d 次）或 v 鍵（%d 次）", toned, v)
	}
	if path := os.Getenv("ZERO_C_WRITE_GOLDEN"); path != "" {
		data, _ := json.MarshalIndent(got, "", " ")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile("testdata/xiaohe-golden.json")
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

// observe（可為 nil）看每一個請求與快照，用來確認腳本真的打到各種情況。
func xiaoheSessionGolden(observe func(toneless bool, r proto.Request, snap proto.Snapshot)) []string {
	var out []string
	for config := range 2 {
		m, words := goldenSessionModel()
		rng := rand.New(rand.NewSource(int64(370 + config)))
		s := session.New(m)
		s.SetLayout(layout.Xiaohe)
		s.Toneless = config == 1
		s.ZhuyinErrorHint = true
		for range 60 {
			var log []any
			do := func(r proto.Request) {
				snap := s.Handle(r)
				if observe != nil {
					observe(s.Toneless, r, snap)
				}
				log = append(log, r, snap)
			}
			keys := func(text string) {
				for _, r := range text {
					do(proto.Request{Type: proto.Key, Key: string(r)})
				}
			}
			for range 1 + rng.Intn(3) {
				e := words[rng.Intn(len(words))]
				for _, y := range e.Reading {
					pair := layout.Xiaohe.Keys(zhuyin.Untone(y.Base()))
					if pair == "" {
						continue
					}
					if strings.ContainsRune(string(y), 'ㄩ') && pair[1] == 'u' && rng.Intn(2) == 0 {
						pair = pair[:1] + "v" // jv＝ju
					}
					switch rng.Intn(12) {
					case 0:
						keys(pair[:1])
						do(proto.Request{Type: proto.Key, Key: proto.KeyBackspace}) // 音節中退格
					case 1:
						keys(pair[:1] + "q") // 多半拼不成的第二鍵
						do(proto.Request{Type: proto.Key, Key: proto.KeyBackspace})
					}
					keys(pair)
					switch rng.Intn(6) {
					case 0, 1, 2:
						keys(strings.TrimPrefix(layout.Xiaohe.Keys(y), layout.Xiaohe.Keys(zhuyin.Untone(y.Base())))) // 聲調
					case 3:
						keys(string(rune('1' + rng.Intn(5)))) // 不一定對的聲調
					case 4:
						if rng.Intn(3) == 0 {
							keys(" ")
						}
					}
				}
				switch rng.Intn(9) {
				case 0:
					do(proto.Request{Type: proto.Key, Key: proto.KeyDown})
					do(proto.Request{Type: proto.Key, Key: string(rune('1' + rng.Intn(3)))})
				case 1:
					do(proto.Request{Type: proto.Key, Key: proto.KeyBackspace}) // 音節後退格
				case 2:
					do(proto.Request{Type: proto.Key, Key: proto.KeyEscape})
				case 3:
					do(proto.Request{Type: proto.Key, Key: proto.KeyLeft})
					keys(string(rune('1' + rng.Intn(5)))) // 游標移開後的數字照字面
				case 4:
					keys(string(rune('a' + rng.Intn(26))))
					do(proto.Request{Type: proto.Backspace})
				case 5:
					keys(",")
				case 6:
					keys(" ")
				}
			}
			do(proto.Request{Type: proto.Key, Key: proto.KeyEnter})
			do(proto.Request{Type: proto.Cancel})
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
