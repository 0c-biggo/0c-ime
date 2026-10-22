// 0c-cli 是 Zuin 的第一個「平台」：在終端機裡操作完整的輸入法行為，
// 並錄製或重播協定層級的 transcript。
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/simplified"
)

const help = `直接輸入按鍵（預設標準注音排列，-keyboard-layout eten 為倚天 41 鍵、hsu 為許氏；空白鍵是一聲），按 Enter 送出這一行的按鍵。
空行＝提交。以 : 起始的是指令：
  :c [詞長]   開候選（不給詞長則優先目前分詞）    :n ID      選候選
  :p 頁碼     換頁          :x  關閉候選            :tab       接受補全／候選內切詞長
  :bs         退格          :esc 放棄組字           :to 位置   移動游標（音節位置）
  :k 鍵名     送具名鍵：Up Down Left Right Enter Escape Delete PageUp PageDown；Ctrl+, Ctrl+. 等 Ctrl＋標點
              多排候選用方向鍵移動；表情選單啟用時 = 開啟、== 輸入等號、:tab 切顏文字
  :forget 詞  撤銷與該詞有關的學習                  :pause on|off  暫停學習
  :shift      單按 Shift 切換中英                   :caps on|off   回報 Caps Lock 狀態（-caps-lock-english 時暫時切英文）
  :learned    列出學到的個人片語                    :why       說明目前的自動近音修正（模糊音 auto）
  :q          離開
例：fup y3ru/3  → ㄑㄧㄣ ㄗˇ ㄐㄧㄥˇ      a86qi6 → ㄇㄚˊ ㄆㄛˊ      up e9  → ㄧㄣ ㄍㄞ（注意結尾空白）`

func main() {
	data := flag.String("data", "testdata/toy", "資料目錄（含 lexicon.tsv、lm.tsv）")
	user := flag.String("user", "", "個人資料檔（JSON）；空白表示不保存")
	replay := flag.String("replay", "", "重播 JSONL 請求檔，把快照寫到標準輸出後結束")
	record := flag.String("record", "", "把本次操作的請求錄成 JSONL")
	fuzzyPairs := flag.String("fuzzy", "", "啟用的模糊音配對，逗號分隔：z-zh、c-ch、s-sh、en-eng；預設是「僅供選字」，寫成 z-zh=auto 則另啟用自動修正；空白為全部關閉。錄製的請求要用相同的設定重播")
	mixedInput := flag.Bool("mixed-input", false, "中英混打試用；重播需使用相同開關，預設關閉")
	toneless := flag.Bool("toneless", false, "不打聲調也能選字（#150）：沒打聲調的音節由解碼決定，開啟時停用混打。重播需使用相同開關")
	candidateLayout := flag.String("candidate-layout", "vertical", "選字排列：vertical 或 grid")
	emojiShortcut := flag.String("emoji-shortcut", "equals", "表情選單：off 或 equals；= 開選單，== 輸入等號，Tab 切顏文字")
	emojiEqualsFirst := flag.Bool("emoji-equals-first", true, "將 = 放在 Emoji 第一項；關閉仍可按 == 輸入等號")
	escClear := flag.Bool("esc-clear", true, "按 Esc 清除整段組字；關閉時只清未打完的注音")
	capsLockEnglish := flag.Bool("caps-lock-english", false, "Caps Lock 開著時直接打英文（用 :caps on 送）；關閉時 caps_lock 不起作用。重播需使用相同開關")
	ctrlPunct := flag.Bool("ctrl-punctuation", true, "注音模式的 Ctrl＋, . ' ; ? ! 打全形標點（用 :k Ctrl+, 送）；關閉時交給應用程式")
	advanceAfterSelect := flag.Bool("advance-after-select", false, "選字後游標移到所選詞後面（前方模式往後移一個字）。重播需使用相同開關")
	outputSimplified := flag.Bool("output-simplified", false, "送出時轉成簡體字（組字與候選仍是繁體）")
	spaceSelects := flag.Bool("space-selects", true, "注音打完按空白選字；關閉時空白輸入空格、↓ 選字。重播需使用相同開關")
	selectionKeys := flag.String("selection-keys", "1234567890", "選字鍵：1234567890、1234qweras 或 asdfghjkl;（數字仍照位置選字）。重播需使用相同選字鍵")
	keyboardLayout := flag.String("keyboard-layout", "standard", "鍵盤排列：standard、eten（倚天 41 鍵，停用混打與 = 開 Emoji）或 hsu（許氏，停用混打）。重播需使用相同排列")
	flag.Parse()

	eng, err := datadir.Open(*data)
	check(err)
	eng.MixedInput = *mixedInput
	eng.Toneless = *toneless
	if *candidateLayout != "vertical" && *candidateLayout != "grid" {
		check(fmt.Errorf("未知選字排列 %q", *candidateLayout))
	}
	if *emojiShortcut != "off" && *emojiShortcut != "equals" {
		check(fmt.Errorf("未知表情快捷鍵 %q", *emojiShortcut))
	}
	if *candidateLayout == "grid" {
		eng.CandidateColumns = 5
	}
	eng.EmojiShortcut = *emojiShortcut
	eng.EmojiEqualsFirst = *emojiEqualsFirst
	eng.EscKeepComposition = !*escClear
	eng.SpaceInsertsSpace = !*spaceSelects
	eng.AdvanceAfterSelect = *advanceAfterSelect
	eng.CtrlPunct = *ctrlPunct
	eng.CapsLockEnglish = *capsLockEnglish
	if *outputSimplified {
		eng.Simplified, err = simplified.TW2S()
		check(err)
	}
	if *selectionKeys != "1234567890" && *selectionKeys != "1234qweras" && *selectionKeys != "asdfghjkl;" {
		check(fmt.Errorf("未知選字鍵 %q", *selectionKeys))
	}
	eng.SelectionKeys = *selectionKeys
	kb, ok := layout.ByName(*keyboardLayout)
	if !ok {
		check(fmt.Errorf("未知鍵盤排列 %q（standard、eten、hsu）", *keyboardLayout))
	}
	eng.Layout = kb
	for _, id := range strings.FieldsFunc(*fuzzyPairs, func(r rune) bool { return r == ',' }) {
		id, mode, _ := strings.Cut(strings.TrimSpace(id), "=")
		p, ok := fuzzy.ParsePair(id)
		if !ok {
			check(fmt.Errorf("未知的模糊音配對 %q", id))
		}
		m := fuzzy.Select
		if mode != "" {
			if m, ok = fuzzy.ParseMode(mode); !ok {
				check(fmt.Errorf("未知的模糊音模式 %q（off、select、auto）", mode))
			}
		}
		eng.Fuzzy = eng.Fuzzy.With(p, m)
	}
	userReadOnly := false
	if *user != "" {
		var newer *personal.NewerFormatError
		if err := datadir.LoadUser(eng, *user); errors.As(err, &newer) {
			fmt.Fprintln(os.Stderr, "個人資料由較新版本寫成，本次不讀入也不存檔：", err)
			userReadOnly = true
		} else {
			check(err)
		}
	}
	if *replay != "" {
		f, err := os.Open(*replay)
		check(err)
		defer f.Close()
		check(eng.Replay(f, os.Stdout))
		return
	}
	eng.Model.Now = func() int64 { return time.Now().Unix() }

	var rec *os.File
	if *record != "" {
		rec, err = os.Create(*record)
		check(err)
		defer rec.Close()
		if *mixedInput {
			fmt.Fprintln(rec, "# 重播需要相同的混打設定：0c-cli -mixed-input")
		}
		if *capsLockEnglish {
			fmt.Fprintln(rec, "# 重播需要相同的 Caps Lock 設定：0c-cli -caps-lock-english")
		}
		if *toneless {
			fmt.Fprintln(rec, "# 重播需要相同的聲調選填設定：0c-cli -toneless")
		}
		if *advanceAfterSelect {
			fmt.Fprintln(rec, "# 重播需要相同的選字後游標設定：0c-cli -advance-after-select")
		}
		if *outputSimplified {
			fmt.Fprintln(rec, "# 重播需要相同的簡體輸出設定：0c-cli -output-simplified")
		}
		if !*spaceSelects {
			fmt.Fprintln(rec, "# 重播需要相同的空白鍵設定：0c-cli -space-selects=false")
		}
		if *selectionKeys != "1234567890" {
			fmt.Fprintf(rec, "# 重播需要相同的選字鍵：0c-cli -selection-keys '%s'\n", *selectionKeys)
		}
		if kb != layout.Standard {
			fmt.Fprintf(rec, "# 重播需要相同的鍵盤排列：0c-cli -keyboard-layout %s\n", *keyboardLayout)
		}
		if key := eng.Fuzzy.Key(); key != "" {
			fmt.Fprintf(rec, "# 重播需要相同的模糊音設定：0c-cli -fuzzy %s\n", *fuzzyPairs) // 重播時 # 行是註解
		}
	}
	id := eng.OpenSession()
	mode := proto.ModeZhuyin
	do := func(r proto.Request) {
		if rec != nil {
			b, _ := json.Marshal(r)
			fmt.Fprintf(rec, "%s\n", b)
		}
		snap, err := eng.Do(id, r)
		check(err)
		render(snap)
		if snap.Mode != mode {
			mode = snap.Mode
			fmt.Printf("  〔%s〕\n", map[string]string{proto.ModeZhuyin: "注音", proto.ModeEnglish: "英文"}[mode])
		}
		if (snap.Commit != "" || snap.PersonalChanged || r.Type == proto.Forget) && *user != "" && !userReadOnly {
			check(datadir.SaveUser(eng, *user))
		}
	}

	fmt.Println("靈犀注音（0c）CLI。輸入 :help 看說明。")
	in := bufio.NewScanner(os.Stdin)
	for fmt.Print("> "); in.Scan(); fmt.Print("> ") {
		line := in.Text()
		switch {
		case line == "":
			do(proto.Request{Type: proto.Commit})
		case !strings.HasPrefix(line, ":") || strings.TrimSpace(line[1:]) == "": // 只有「:」（Shift＋; 的標點）照字面送
			for _, k := range line {
				do(proto.Request{Type: proto.Key, Key: string(k)})
			}
		default:
			f := strings.Fields(line[1:])
			arg := func() string {
				if len(f) > 1 {
					return f[1]
				}
				return ""
			}
			num := func() int { n, _ := strconv.Atoi(arg()); return n }
			switch f[0] {
			case "q":
				return
			case "help":
				fmt.Println(help)
			case "c":
				do(proto.Request{Type: proto.Candidates, Length: num()})
			case "n":
				do(proto.Request{Type: proto.Select, Index: num()})
			case "p":
				do(proto.Request{Type: proto.Page, Index: num()})
			case "x":
				do(proto.Request{Type: proto.Close})
			case "tab":
				do(proto.Request{Type: proto.Key, Key: proto.KeyTab})
			case "k":
				do(proto.Request{Type: proto.Key, Key: arg()})
			case "bs":
				do(proto.Request{Type: proto.Backspace})
			case "esc":
				do(proto.Request{Type: proto.Cancel})
			case "to":
				do(proto.Request{Type: proto.Cursor, To: num()})
			case "forget":
				do(proto.Request{Type: proto.Forget, Word: arg()})
			case "pause":
				do(proto.Request{Type: proto.Pause, On: arg() == "on"})
			case "shift":
				do(proto.Request{Type: proto.Toggle})
			case "caps":
				do(proto.Request{Type: proto.CapsLock, On: arg() == "on"})
			case "why":
				changes := eng.Session(id).AutoChanges()
				if len(changes) == 0 {
					fmt.Println("  目前沒有自動近音修正")
				}
				for _, c := range changes {
					fmt.Println("  " + c.String())
				}
			case "learned":
				fmt.Println("  個人片語：", strings.Join(eng.Session(id).Learned(), "、"))
			default:
				fmt.Println("  不認得的指令；:help 看說明")
			}
		}
	}
}

// render 把快照畫成一行：⟦鎖定⟧ ~修正~ 前綴… (未完成符號) ｜游標 ░補全建議
func render(s proto.Snapshot) {
	if s.Commit != "" {
		fmt.Printf("  ⏎ 提交：%s\n", s.Commit)
	}
	for _, n := range s.Notices {
		fmt.Printf("  ※ %s\n", n)
	}
	if len(s.Preedit) == 0 && s.Candidates == nil {
		return
	}
	var b strings.Builder
	pos := 0
	for _, g := range s.Preedit {
		open, close := "", ""
		switch g.Style {
		case proto.StyleLocked:
			open, close = "⟦", "⟧"
		case proto.StyleCorrected:
			open, close = "~", "~"
		case proto.StylePartial:
			close = "…"
		case proto.StylePending, proto.StyleReading:
			open, close = "(", ")"
		}
		b.WriteString(open)
		for _, r := range g.Text {
			if pos == s.Cursor.Rune {
				b.WriteString("｜")
			}
			b.WriteRune(r)
			pos++
		}
		b.WriteString(close)
	}
	if pos == s.Cursor.Rune {
		b.WriteString("｜")
	}
	if h := s.ReadingHint; h != nil { // #162：空白出中文的讀音，Enter 留數字
		b.WriteString("  [空白 " + h.Text + "]")
	}
	if s.Suggestion != "" {
		if s.SuggestionEdit != nil {
			b.WriteString("  ⇥ " + s.Suggestion + "  (:tab 接受，Esc 取消預覽)")
		} else {
			b.WriteString("░" + s.Suggestion + "  (:tab 接受)")
		}
	}
	fmt.Printf("  %s\n", b.String())
	if c := s.Candidates; c != nil {
		var items []string
		for i, it := range c.Items {
			label := fmt.Sprintf("%d.%s", it.ID, it.Text)
			if it.Note != "" {
				label += "〔" + it.Note + "〕"
			}
			if i == c.Highlight {
				label = "【" + label + "】"
			}
			items = append(items, label)
		}
		if c.Title != "" {
			fmt.Printf("  %s\n", c.Title)
		}
		if c.Columns > 1 {
			fmt.Printf("  候選（第 %d/%d 頁，:n ID 選取）：\n", c.Page+1, c.Pages)
			for start := 0; start < len(items); start += c.Columns {
				fmt.Printf("    %s\n", strings.Join(items[start:min(start+c.Columns, len(items))], "    "))
			}
		} else {
			fmt.Printf("  候選（第 %d/%d 頁，位置 %d 起 %d 字）：%s\n", c.Page+1, c.Pages, c.Start, c.Length, strings.Join(items, " "))
		}
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-cli:", err)
		os.Exit(1)
	}
}
