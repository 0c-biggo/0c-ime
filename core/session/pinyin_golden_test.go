package session_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/session"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #324：漢語拼音打聲調時，每打完一個音節的快照（組字、游標、候選、補全）都跟用注音打同一個讀音完全相同，
// 送出的文字也相同。拼音只是另一種按鍵，選字全交給同一個引擎。詞庫同 plainSessionGolden 的亂數詞庫。
func TestPinyinMatchesZhuyin(t *testing.T) {
	m, words := goldenSessionModel()
	rng := rand.New(rand.NewSource(324))
	zs, ps := session.New(m), session.New(m)
	ps.SetLayout(layout.Pinyin)
	compared := 0
	for range 200 {
		var reading []zhuyin.Syllable
		for range 1 + rng.Intn(3) {
			reading = append(reading, words[rng.Intn(len(words))].Reading...)
		}
		typable := true
		for _, y := range reading {
			typable = typable && layout.Pinyin.Keys(y) != ""
		}
		if !typable {
			continue
		}
		for _, y := range reading {
			var zsnap, psnap proto.Snapshot
			for _, k := range layout.Standard.Keys(y) {
				zsnap = zs.Handle(proto.Request{Type: proto.Key, Key: string(k)})
			}
			for _, k := range layout.Pinyin.Keys(y) {
				psnap = ps.Handle(proto.Request{Type: proto.Key, Key: string(k)})
			}
			zsnap.Revision, psnap.Revision = 0, 0
			if !reflect.DeepEqual(zsnap, psnap) {
				t.Fatalf("%v 打到 %s：注音 %+v\n拼音 %+v", reading, y, zsnap, psnap)
			}
			compared++
		}
		if rng.Intn(3) == 0 { // 開候選選第二個，兩邊一樣
			zsnap := zs.Handle(proto.Request{Type: proto.Key, Key: proto.KeyDown})
			psnap := ps.Handle(proto.Request{Type: proto.Key, Key: proto.KeyDown})
			if zsnap.Candidates != nil && len(zsnap.Candidates.Items) > 1 {
				zsnap = zs.Handle(proto.Request{Type: proto.Key, Key: "2"})
				psnap = ps.Handle(proto.Request{Type: proto.Key, Key: "2"})
			}
			if zsnap.Text() != psnap.Text() {
				t.Fatalf("%v 選字後：注音 %q、拼音 %q", reading, zsnap.Text(), psnap.Text())
			}
		}
		z := zs.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter}).Commit
		p := ps.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter}).Commit
		if z != p || z == "" {
			t.Fatalf("%v 送出：注音 %q、拼音 %q", reading, z, p)
		}
	}
	if compared < 300 {
		t.Fatalf("只比較了 %d 個音節", compared)
	}
}

// 拼音鍵盤的快照基準：亂數詞庫上打 120 段，混著打聲調、不打聲調、隔音符號、空白、退格、Esc、方向鍵、開候選選字與送出，
// 兩種設定（聲調選填關、開）。每一筆是那一段所有快照 JSON 的雜湊；改動拼音鍵盤的行為時要確認差異是預期的再重新產生：
//
//	ZERO_C_WRITE_GOLDEN=$PWD/core/session/testdata/pinyin-golden.json go test ./core/session -run TestPinyinSessionGolden
func TestPinyinSessionGolden(t *testing.T) {
	got := pinyinSessionGolden()
	if path := os.Getenv("ZERO_C_WRITE_GOLDEN"); path != "" {
		data, _ := json.MarshalIndent(got, "", " ")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile("testdata/pinyin-golden.json")
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

func pinyinSessionGolden() []string {
	var out []string
	for config := range 2 {
		m, words := goldenSessionModel()
		rng := rand.New(rand.NewSource(int64(324 + config)))
		s := session.New(m)
		s.SetLayout(layout.Pinyin)
		s.Toneless = config == 1
		s.ZhuyinErrorHint = true
		for range 60 {
			var log []any
			do := func(r proto.Request) {
				log = append(log, r, withoutReadingLabel(s.Handle(r))) // 基準早於 #361 的標示，見 plain_golden_test
			}
			keys := func(text string) {
				for _, r := range text {
					do(proto.Request{Type: proto.Key, Key: string(r)})
				}
			}
			for range 1 + rng.Intn(3) {
				e := words[rng.Intn(len(words))]
				for i, y := range e.Reading {
					k := zhuyin.PinyinKeys(y)
					if k == "" {
						continue
					}
					if i > 0 && rng.Intn(4) == 0 {
						keys("'")
					}
					keys(k)
					switch rng.Intn(5) {
					case 0, 1:
						keys(strings.TrimPrefix(layout.Pinyin.Keys(y), k)) // 聲調
					case 2:
						if rng.Intn(3) == 0 {
							keys(" ")
						}
					}
				}
				switch rng.Intn(8) {
				case 0:
					do(proto.Request{Type: proto.Key, Key: proto.KeyDown})
					do(proto.Request{Type: proto.Key, Key: string(rune('1' + rng.Intn(3)))})
				case 1:
					do(proto.Request{Type: proto.Key, Key: proto.KeyBackspace})
				case 2:
					do(proto.Request{Type: proto.Key, Key: proto.KeyEscape})
				case 3:
					do(proto.Request{Type: proto.Key, Key: proto.KeyLeft})
				case 4:
					keys(string(rune('a' + rng.Intn(26))))
					do(proto.Request{Type: proto.Backspace})
				case 5:
					keys(",")
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
