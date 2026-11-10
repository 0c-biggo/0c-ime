package session

import (
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// lateInitialSession 收數字鍵聲母（ㄅ 1、ㄉ 2、ㄓ 5）的兩鍵音節，量級照 0.2.9 資料包（#119）。
func lateInitialSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"的", "ㄉㄜ˙", 1.6e-2}, {"這", "ㄓㄜˋ", 6e-3}, {"不", "ㄅㄨˋ", 6e-3}, {"到", "ㄉㄠˋ", 1.2e-3}, {"被", "ㄅㄟˋ", 1.3e-3},
		{"本", "ㄅㄣˇ", 1.2e-3}, {"我", "ㄨㄛˇ", 2e-3}, {"好", "ㄏㄠˇ", 1e-3}, {"你", "ㄋㄧˇ", 1e-3}, {"是", "ㄕˋ", 5e-3},
		{"低", "ㄉㄧ", 2e-4}, {"震", "ㄓㄣˋ", 5e-5}, {"播", "ㄅㄛˋ", 5e-5}, {"跛", "ㄅㄛˇ", 1e-6}, {"博", "ㄅㄛˊ", 5e-4}, {"辣", "ㄌㄚˋ", 3e-5},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	var words []english.Word
	for _, w := range []string{"a", "hello"} {
		words = append(words, english.Word{Text: w, Weight: 65})
	}
	s.English = english.New(words)
	return s
}

// #119：快打時韻母鍵比左手上排的數字鍵聲母先到（的 是 2k7，打成 k27），在句首、空白、全形標點後、前一段已送出時
// 也照位置組字，跟緊接中文時一樣；接著打的注音照常是中文，不再整串變成英數。
func TestMixedLateDigitInitialComposes(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"k27", "的"}, {"k54", "這"}, {"j14", "不"}, {"l24", "到"}, {"o14", "被"}, {"p13", "本"},
		{"k27ji3", "的我"}, {"k54g4", "這是"}, {"j14cl3", "不好"},
		{"hello k27", "hello 的"}, {"ji3<k27", "我，的"}, {"ji3k27", "我的"},
		// 取捨：ㄅㄛˊ 連「博士」等詞首算是常用字，i16（iPhone 16 的簡寫）句首也先出博，跟 mp3 先出允一樣 ↓ 有原按鍵。
		{"i16", "博"},
	} {
		s := lateInitialSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
	// 前一段已經送出（組字區是空的），跟句首一樣。
	s := lateInitialSession()
	mixedType(s, "ji3")
	if got := key(s, proto.KeyEnter).Commit; got != "我" {
		t.Fatalf("先送出的 我：%q", got)
	}
	mixedType(s, "k27")
	if got := key(s, proto.KeyEnter).Commit; got != "的" {
		t.Errorf("送出後再打 k27：%q，要 的", got)
	}
}

// #119：照位置組出的字上按 ↓，原按鍵仍在候選裡，選了變回 k27。
func TestMixedLateDigitInitialKeepsKeys(t *testing.T) {
	s := lateInitialSession()
	mixedType(s, "k27")
	snap := key(s, proto.KeyDown)
	got := itemTexts(snap)
	id := slices.Index(got, "k27")
	if len(got) == 0 || got[0] != "的" || id < 0 {
		t.Fatalf("k27 的 ↓：%q", got)
	}
	s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[id].ID})
	if commit := key(s, proto.KeyEnter).Commit; commit != "k27" {
		t.Errorf("選原按鍵後提交 %q", commit)
	}
}

// #119 不改 #249 的英數寫法：沒有聲調（u2 Enter）、接空白（u2␣）、組成罕用字（i14 播、i13 跛，iPhone 的簡寫）、
// 數字開頭（8x4，乘號）的照舊照字面；英文字緊接著打的（hellok27）照舊。
func TestMixedLateDigitInitialKeepsLiterals(t *testing.T) {
	for _, keys := range []string{"u2", "u2 ", "i14", "i13", "8x4", "p54", "hellok27"} {
		s := lateInitialSession()
		mixedType(s, keys)
		if got := key(s, proto.KeyEnter).Commit; got != keys {
			t.Errorf("%q 應照字面：%q", keys, got)
		}
	}
}

// #119：照字面留下的反序兩鍵接聲調（p54 的 ㄓㄣˋ 震是罕用字），後面接著打的注音從它後面切開組字，不再一路接成英數
// （客戶說的「出現 k27 之後所有中文都會無法呈現」）。帳號、生日（nina87、lisa03）後面接著打的注音，#249 起也從數字後面切開
// （PCDVD、PTT 語料裡 leehamin777的、mobile01發 這類型號、帳號接中文常見，抽樣重播沒有因此由對變錯的）。
func TestMixedLateDigitInitialSplitsAfterLiteral(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"p54cl3", "p54好"}, {"p54su3cl3", "p54你好"}, {"i14cl3", "i14好"},
		{"nina87su3", "nina87你"}, {"lisa03su3", "lisa03你"},
	} {
		s := lateInitialSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #119：常打 iPhone 簡寫的人在博上按 ↓ 選回 i16，隔天再選、三次之後就照字面（同 #249 的 mp3）。
func TestMixedLateDigitInitialPickedBackIsLearned(t *testing.T) {
	s := lateInitialSession()
	now := int64(1_800_000_000)
	s.model.Now = func() int64 { return now }
	for day := range 3 {
		now += 86400 * int64(day+1)
		mixedType(s, "i16")
		snap := key(s, proto.KeyDown)
		for snap.Candidates != nil && !slices.Contains(itemTexts(snap), "i16") && snap.Candidates.Page+1 < snap.Candidates.Pages {
			snap = key(s, proto.KeyPageDown)
		}
		id := slices.Index(itemTexts(snap), "i16")
		if id < 0 {
			t.Fatalf("第 %d 天的 ↓ 找不到 i16：%q", day, itemTexts(snap))
		}
		s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[id].ID})
		if got := key(s, proto.KeyEnter).Commit; got != "i16" {
			t.Fatalf("第 %d 天提交 %q", day, got)
		}
	}
	mixedType(s, "i16")
	if got := key(s, proto.KeyEnter).Commit; got != "i16" {
		t.Errorf("學會之後：%q，要 i16", got)
	}
}

// #119＋#55：只連打「我」讓個人機率稀釋到門檻下，k27 仍照位置組成「的」（系統機率是下限）。
func TestMixedLateDigitInitialSurvivesUnrelatedLearning(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{{"k27", "的"}, {"j14", "不"}, {"k54", "這"}} {
		t.Run(tc.keys, func(t *testing.T) {
			s := lateInitialSession()
			for i := 0; i < 300; i++ {
				mixedType(s, "ji3")
				if got := key(s, proto.KeyEnter).Commit; got != "我" {
					t.Fatalf("learning commit: %q", got)
				}
			}
			mixedType(s, tc.keys)
			if got := key(s, proto.KeyEnter).Commit; got != tc.want {
				t.Fatalf("after unrelated learning: %q, want %q", got, tc.want)
			}
		})
	}
}
