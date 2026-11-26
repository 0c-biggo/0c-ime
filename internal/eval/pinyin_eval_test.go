package eval_test

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
)

// TestPinyinAccuracy 量漢語拼音鍵盤（#324，core/session/pinyin.go）跟注音打同一段文字的整句與逐字正確率，
// 以及每個請求的處理時間。參考讀音照資料（注音）轉成拼音按鍵，同一句各種打法都用同一個讀音：
//
//   - zhuyin：標準排列注音，打聲調（一聲按空白）。
//   - pinyin：漢語拼音，數字標調（ni3hao3，一聲按 1）。
//   - zhuyin-toneless：標準排列注音，開「不打聲調也能選字」、一個聲調都不打（音節由逐鍵貪婪切）。
//   - pinyin-toneless：漢語拼音，不打聲調也不打 '，最後按空白收完（音節由解碼器在按鍵圖上切）。
//   - pinyin-toneless-sep：同上，但音節之間打 '（音節邊界是使用者給的）。
//
// 只用統計引擎（不接神經）、不學習（暫停學習、每句 Enter 送出後重開）。命中是 Enter 送出的文字等於參考句。
// 讀音裡有拼音鍵盤打不出的音節（單獨的 ㄝ 等）的句子整句略過，所有打法用同一批句子。
//
//	ZERO_C_PACK=<資料包目錄> ZERO_C_PINYIN_CASES=<TSV，最後兩欄是文字與讀音，逗號分隔多個> [ZERO_C_PINYIN_LIMIT=N] \
//	  go test ./internal/eval -run TestPinyinAccuracy -count=1 -v
//
// 兩個環境變數都要設，沒設時跳過。
func TestPinyinAccuracy(t *testing.T) {
	pack := os.Getenv("ZERO_C_PACK")
	if pack == "" {
		t.Skip("set ZERO_C_PACK to measure the pinyin keyboard")
	}
	files := strings.Split(os.Getenv("ZERO_C_PINYIN_CASES"), ",")
	if files[0] == "" {
		t.Skip("set ZERO_C_PINYIN_CASES to the cases to measure")
	}
	limit := 0
	fmt.Sscan(os.Getenv("ZERO_C_PINYIN_LIMIT"), &limit)
	for _, file := range files {
		cases, skipped := pinyinCases(t, file, limit)
		t.Logf("%s：%d 句（%d 句有拼音打不出的音節，略過）", file, len(cases), skipped)
		for _, arm := range []string{"zhuyin", "pinyin", "zhuyin-toneless", "pinyin-toneless", "pinyin-toneless-sep"} {
			e, err := datadir.Open(pack)
			if err != nil {
				t.Fatal(err)
			}
			e.Toneless = strings.HasSuffix(arm, "toneless") || strings.HasSuffix(arm, "-sep")
			e.ZhuyinErrorHint = true
			if strings.HasPrefix(arm, "pinyin") {
				e.Layout = layout.Pinyin
			}
			var times []time.Duration
			hits, chars, charHits, keys := 0, 0, 0, 0
			var misses []string
			for _, c := range cases {
				id := e.OpenSession()
				do := func(r proto.Request) proto.Snapshot {
					start := time.Now()
					snap, err := e.Do(id, r)
					times = append(times, time.Since(start))
					if err != nil {
						t.Fatal(err)
					}
					return snap
				}
				do(proto.Request{Type: proto.Pause, On: true})
				times = times[:len(times)-1]
				for _, k := range pinyinArmKeys(arm, c.reading) {
					keys++
					do(proto.Request{Type: proto.Key, Key: k})
				}
				got := do(proto.Request{Type: proto.Key, Key: proto.KeyEnter}).Commit
				e.CloseSession(id)
				if got == c.text {
					hits++
				} else if len(misses) < 20 {
					misses = append(misses, c.text+"→"+got)
				}
				ref, out := []rune(c.text), []rune(got)
				chars += len(ref)
				for i := range min(len(ref), len(out)) {
					if ref[i] == out[i] {
						charHits++
					}
				}
			}
			slices.Sort(times)
			ms := func(d time.Duration) string { return fmt.Sprintf("%.2f ms", float64(d.Microseconds())/1000) }
			t.Logf("%-20s 整句 %d/%d（%.2f%%）逐字 %.2f%%  每字按鍵 %.2f  請求 p50 %s、p95 %s、p99 %s、最慢 %s",
				arm, hits, len(cases), 100*float64(hits)/float64(len(cases)), 100*float64(charHits)/float64(chars),
				float64(keys)/float64(chars),
				ms(times[len(times)/2]), ms(times[len(times)*95/100]), ms(times[len(times)*99/100]), ms(times[len(times)-1]))
			if os.Getenv("ZERO_C_PINYIN_MISSES") != "" {
				t.Logf("  錯例：%s", strings.Join(misses, "、"))
			}
		}
	}
}

type pinyinCase struct {
	text    string
	reading []zhuyin.Syllable
}

// pinyinCases 讀 TSV：最後一欄是讀音時取前一欄當文字（id、文字、讀音），否則第 1、2 欄是文字、讀音（RIME 研究的格式）。
func pinyinCases(t *testing.T, path string, limit int) (out []pinyinCase, skipped int) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() && (limit == 0 || len(out) < limit) {
		cols := strings.Split(sc.Text(), "\t")
		if len(cols) < 2 {
			continue
		}
		text, reading := cols[0], zhuyin.ParseReading(cols[1])
		if len(cols) >= 3 && len(reading) != len([]rune(text)) {
			text, reading = cols[len(cols)-2], zhuyin.ParseReading(cols[len(cols)-1])
		}
		ok := len(reading) > 0 && len(reading) == len([]rune(text))
		for _, y := range reading {
			ok = ok && y.Legal()
		}
		if !ok {
			continue
		}
		typable := true
		for _, y := range reading {
			typable = typable && layout.Pinyin.Keys(y) != ""
		}
		if !typable {
			skipped++
			continue
		}
		out = append(out, pinyinCase{text, reading})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out, skipped
}

// pinyinArmKeys 是打法 arm 打出 reading 的按鍵（不含最後的 Enter）。
func pinyinArmKeys(arm string, reading []zhuyin.Syllable) []string {
	var out []string
	add := func(s string) {
		for _, r := range s {
			out = append(out, string(r))
		}
	}
	for i, y := range reading {
		switch arm {
		case "zhuyin":
			add(layout.Standard.Keys(y))
		case "pinyin":
			add(layout.Pinyin.Keys(y))
		case "zhuyin-toneless":
			add(strings.TrimSuffix(layout.Standard.Keys(y.Base()), " "))
		case "pinyin-toneless":
			add(zhuyin.PinyinKeys(y))
		case "pinyin-toneless-sep":
			if i > 0 {
				add("'")
			}
			add(zhuyin.PinyinKeys(y))
		}
	}
	if strings.HasPrefix(arm, "pinyin-toneless") {
		add(" ")
	}
	return out
}
