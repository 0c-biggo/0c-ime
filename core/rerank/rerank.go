// Package rerank 定義可選神經評分器的純資料契約；模型載入、時間與取消由宿主負責。
package rerank

import (
	"math"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// MaxContextRunes 是送給評分器的已確認前文上限，與序列模型訓練時的前文窗口相同。
const MaxContextRunes = 64

// MaxCandidates 是一次評分的候選上限：打出讀音的 N-best 8 個，加上輕聲擴充最多 8 個。
const MaxCandidates = 16

// ModeText 是文字評分：評分器只以前文續寫機率評候選（log P(候選文字 | 前文)），不看讀音。
//
//   - 單音節（所有評分器）：讓句尾助詞（嗎／吧／啦…）在打成一聲或輕聲時仍由語境決定。必須有前文。
//   - 2–32 字（只送給宣告 Options.TextAnyLength 的評分器）：注音九宮格同一串鍵有多種拼法時，候選跨拼法，
//     讀音評分的分數不能互相比，改用文字評分讓所有候選在同一個尺度上。前文可以是空的。
//
// 兩種情況的 Reading 都是統計首選的讀音（同讀音評分，沒打聲調的照 MaskTones），只用來表示字數：
// 評分器不看它，每個候選的字數等於 len(Reading)。
const ModeText = "text"

// Request 只含已輸入的完整音節與既有候選，沒有參考答案或任意文件文字。
// Context 是外殼確認已寫入文件的自身提交文字（最多 MaxContextRunes 字）；
// 只有宣告需要前文的評分器才會收到，私人／暫停／英數狀態一律為空。
//
// 英數狀態唯一的例外是 DigitBefore（#337）：游標前一個字是阿拉伯數字（英數模式或數字鍵盤打的，核心看到按鍵交還
// 應用程式；Windows 由外殼讀游標前一個字）。只是一個位元，不含數字本身；只在記憶體，不寫檔、不學習、不進 trace，
// 私人欄位、暫停與前文設定關閉時不帶，游標移動、換焦點、Esc 清掉組字時清掉。只用在組字的第一段、Context 是空的
// 讀音評分（Mode ""），只送給宣告 Options.DigitContext 的評分器；融合權重照有前文的 Weight（見 Options.WeightOf）。
type Request struct {
	AllowPartial bool                `json:"allow_partial,omitempty"`
	Mode         string              `json:"mode,omitempty"` // "" 音字評分；ModeText 只看前文（單音節聲調容錯）
	Revision     int                 `json:"revision"`
	Context      string              `json:"context,omitempty"`
	DigitBefore  bool                `json:"digit_before,omitempty"`
	Reading      []zhuyin.Syllable   `json:"reading"`
	Candidates   []decode.Hypothesis `json:"candidates"`

	// ToneKnown 逐音節標出聲調是否已知（models zuin-tone-mask-v1）；false 的音節在 Reading 裡是不帶聲調符號的基底。
	// 全部已知時為 nil，請求與舊協定逐位元組相同。只送給 Options.UnknownTones 的評分器，見 MaskTones。
	ToneKnown []bool `json:"tone_known,omitempty"`
}

type Scorer interface {
	Score(Request) ([]float64, error)
}

type Options struct {
	AllowPartial bool    // 僅限經獨立校準的等長前綴候選；預設仍整例回退。
	ContextRunes int     // 0 不送前文；序列模型套件宣告 MaxContextRunes。
	HintGap      float64 // >0 時，融合分數與首選差距小於此值的次佳候選以 Tab 替換提示呈現；0 停用
	Scorer       Scorer
	Weight       float64
	Margin       float64
	TextWeight   float64 // >0 時單音節段以 ModeText 評分；0 停用
	TextMargin   float64
	// NeutralExpand >0 時，段落最後一個音節的基底在 NeutralBases 內且不是輕聲，
	// 就另取「最後音節改輕聲」的 N-best，把新候選的引擎分數扣 NeutralExpand 後併入。
	NeutralExpand float64
	NeutralBases  map[zhuyin.Syllable]bool
	// UnknownTones：評分器接受未知聲調（套件宣告 tone-mask，見 UnknownTonesDeclared）。開啟時沒打聲調的段落
	// 也送評分，讀音以 MaskTones 轉成基底加 ToneKnown；關閉時這種段落照統計引擎（#150）。
	UnknownTones bool
	// Bases 是評分器字表的音節基底（不帶聲調，見 ParseBases）。非 nil 時，基底不在表內的段落在本機就不送評分，
	// 不必等評分器整例回退；nil 不檢查。
	Bases map[zhuyin.Syllable]bool
	// Chars 是評分器字表的字（見 ParseChars）。非 nil 時，含字表外字元的候選在本機就拿掉（KeepEncodable），
	// 其他候選照常評分，不讓評分器因為一個候選整批拒絕；nil 不檢查。
	Chars map[rune]bool
	// ContextlessWeight >0 時，前文是空的請求改用這個融合權重（平台設定，例：手機沒前文 0.5，見 MobileWeight）；
	// 0 一律用 Weight。單音節文字評分一定有前文，用 TextWeight。
	ContextlessWeight float64
	// ShortContextlessWeight >0 時，前文是空的而且讀音不超過 ShortContextlessSyllables 個音節的請求改用這個權重，
	// 優先於 ContextlessWeight（平台設定，例：桌面短讀音沒前文 0.5，見 DesktopContextlessWeight）。
	ShortContextlessWeight    float64
	ShortContextlessSyllables int
	// TextAnyLength：評分器接受 2–32 字、前文可以是空的 ModeText（見 ModeText）。由外殼宣告：模型的文字評分本來就有
	// （套件 text_weight > 0），多字只是外殼的 Prepare 要把候選樹接在沒有讀音的 OUT 後面，是外殼程式的能力，不是套件的。
	// 開啟時，有九宮格位置可以換拼法的段落改送文字評分（session rerankSegmentBody）；其他請求不變。
	// 關閉時（桌面、還沒實作的外殼）所有請求與決策都跟以前逐位元組相同。
	TextAnyLength bool
	// DigitContext：評分器認得 Request.DigitBefore（#337，internal/neural 把它換成模型的前文 "1"）。
	// 關閉時（手機、沒有前文的套件）不送這個旗標，請求與決策跟以前相同。
	DigitContext bool
}

// Supported 採整例回退，不以刪除 Partial/Correction 路徑製造虛假的召回改善。這是所有評分器都接受的請求；
// 多字的 ModeText 另看評分器的 Options.TextAnyLength（Options.Supports）。
func Supported(r Request) bool { return supported(r, false) }

// supported 是 Supported；anyText 時另外接受 2–32 字的 ModeText，前文可以是空的。
func supported(r Request, anyText bool) bool {
	switch r.Mode {
	case "":
		if len(r.Reading) < 2 || len(r.Reading) > 32 {
			return false
		}
	case ModeText:
		switch n := len(r.Reading); {
		case n == 1:
			if r.Context == "" {
				return false
			}
		case n < 1 || n > 32 || !anyText:
			return false
		}
	default:
		return false
	}
	if len(r.Candidates) < 2 || len(r.Candidates) > MaxCandidates {
		return false
	}
	if !utf8.ValidString(r.Context) || utf8.RuneCountInString(r.Context) > MaxContextRunes {
		return false
	}
	if r.DigitBefore && (r.Context != "" || r.Mode != "") {
		return false // 旗標只代表「沒有前文、前一個字是數字」，只用在讀音評分
	}
	if r.ToneKnown != nil && len(r.ToneKnown) != len(r.Reading) {
		return false
	}
	for i, y := range r.Reading {
		if !y.Valid() || r.ToneKnown != nil && !r.ToneKnown[i] && y != y.Base() {
			return false
		}
	}
	seen := map[string]bool{}
	for _, c := range r.Candidates {
		if seen[c.Text] || utf8.RuneCountInString(c.Text) != len(r.Reading) || !finite(c.LogScore) {
			return false
		}
		seen[c.Text] = true
		text := []rune(c.Text)
		end := 0
		for _, g := range c.Segments {
			if g.Start != end || g.End <= end || g.End > len(text) || (g.Partial && !r.AllowPartial) || g.Correction || g.Word != string(text[end:g.End]) {
				return false
			}
			end = g.End
		}
		if end != len(text) {
			return false
		}
	}
	return true
}
func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// Choose 同分保留原首選；無效或缺失評分整例回退。
func Choose(rows []decode.Hypothesis, scores []float64, weight, margin float64) int {
	if len(rows) == 0 || len(rows) != len(scores) || !finite(weight) || weight <= 0 || !finite(margin) || margin < 0 {
		return 0
	}
	best := 0
	values := make([]float64, len(rows))
	for i, c := range rows {
		values[i] = c.LogScore + weight*scores[i]
		if !finite(c.LogScore) || !finite(scores[i]) || !finite(values[i]) {
			return 0
		}
		if values[i] > values[best] {
			best = i
		}
	}
	if values[best]-values[0] > margin {
		return best
	}
	return 0
}
