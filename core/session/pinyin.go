package session

import (
	"strings"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 漢語拼音鍵盤（#324，鍵盤排列選 layout.Pinyin）：用拼音打繁體，切成音節、轉成注音後交給同一個引擎，
// 詞庫、語言模型、神經選字與學習都跟注音一樣。按鍵規則同新酷音的「漢語拼音」鍵盤配置，另外接受不打聲調：
//
//   - 字母 a–z 是拼音（ü 打 v），先存在 pyKeys，組字區照打的字母顯示（StylePending、Unfinished），還不是字。
//   - 拼音後面的 1–5 是聲調（5 是輕聲），收進這串拼音：切成音節，最後一個帶這個聲調，前面的（ni hao3 的 ni）不打聲調、
//     由解碼決定。空白是一聲（同新酷音、小麥注音），開了「不打聲調也能選字」時空白把拼音換成字並送出（同一般拼音輸入法）。
//     聲調與空白只收得成拼音的字母：切不成音節、或帶這個聲調的音節沒有字時不收，字母留著、快照的 Bell 提示（同 #300）。
//   - 拼音後面的 ' 是隔音符號（xi'an），存在拼音裡；沒有拼音時 ' 照舊是頓號。
//   - 沒有拼音時數字、空白照一般規則：組字中數字照字面留在組字區、空白開候選；沒在組字時交還應用程式。
//     候選開著時數字是選字鍵（候選先處理），所以數字當聲調只在剛打的拼音後面，三種用法不會同時成立。
//   - 退格刪一個字母、Esc 清掉拼音。其他按鍵（Enter、標點、方向鍵、Tab、↓、Shift＋字母……）先把拼音不打聲調就完成，
//     再照一般規則處理；切不成音節的字母（hello、iphone）照字面留下，所以 Enter 送得出打的英文字母。
//   - 中英混打停用（mixedInputActive 只在標準排列）：字母都是拼音，英文用 Shift 切換。
//
// 切法：一串字母有好幾種切法時（fangan 是 fan gan 或 fang an），在按鍵圖（input.Hyp.Span）上由解碼器依
// 詞庫與語言模型一次決定，左邊組好的字當前後文；' 一定是切點，自己就是一個音節的一段（xian）不再切開。解不出時照
// zhuyin.SplitPinyin 的規則（拼音正詞法：a o e 開頭的音節前沒打 ' 就不這樣切）。

// pinyinLayout 回報目前是不是漢語拼音鍵盤。
func (s *Session) pinyinLayout() bool { return s.layout == layout.Pinyin }

// pinyinPending 回報是否有還沒收成音節的拼音字母。
func (s *Session) pinyinPending() bool { return s.pyKeys != "" }

// keysPending 回報是否有還沒收成音節的按鍵：九宮格的鍵或拼音字母。
func (s *Session) keysPending() bool { return s.ninePending() || s.pinyinPending() }

// pinyinKey 在漢語拼音鍵盤處理一個按鍵。consumed 為 true 時按鍵已處理完；false 時照一般規則繼續（未收的拼音已經完成）。
// commit 是這一鍵送出的文字（不打聲調時的空白）。
func (s *Session) pinyinKey(k string) (consumed bool, commit string) {
	if !s.pinyinLayout() || s.english || s.cand != nil {
		return false, ""
	}
	rs := []rune(k)
	single := len(rs) == 1
	switch {
	case single && zhuyin.PinyinLetter(rs[0]):
		s.pyKeys += k
		return true, ""
	case !s.pinyinPending():
		return false, ""
	case k == string(zhuyin.PinyinSeparator):
		if !strings.HasSuffix(s.pyKeys, k) {
			s.pyKeys += k
		}
		return true, ""
	case single && rs[0] >= '1' && rs[0] <= '5':
		s.closePinyin(int(rs[0]-'0'), true)
		return true, ""
	case k == " ":
		if !s.Toneless {
			s.closePinyin(1, true)
			return true, ""
		}
		// 不打聲調：空白把拼音換成字並送出整段組字（同 RIME、Fcitx5 拼音、ibus-libpinyin 與系統內建拼音的空白，
		// 2026-11-26 負責人決定）。切不成音節時不收也不送，字母留著。要先看字再送的話按 ↓ 或方向鍵（先換成字、不送出）。
		if s.closePinyin(0, true) {
			return true, s.commit()
		}
		return true, ""
	case k == proto.KeyBackspace:
		s.pyKeys = s.pyKeys[:len(s.pyKeys)-1]
		return true, ""
	case k == proto.KeyEscape:
		s.pyKeys = ""
		return true, ""
	}
	s.closePinyin(0, false)
	return false, ""
}

// closePinyin 把還沒收的拼音收成音節：tone 1–5 是最後一個音節的聲調，0 是都不打聲調。strict（聲調鍵、空白）時切不成
// 音節或最後一個音節帶這個聲調沒有字就不收，字母留著、回傳 false；不 strict（其他按鍵帶來的完成）時切不成音節的字母
// 照字面留在組字區。
func (s *Session) closePinyin(tone int, strict bool) bool {
	keys := s.pyKeys
	if keys == "" {
		return false
	}
	parts, ok := s.splitPinyin(keys, tone)
	if !ok {
		if strict {
			s.bell = s.ZhuyinErrorHint
			return false
		}
		s.pyKeys = ""
		s.insertLiteral(keys)
		return true
	}
	syls := make([]zhuyin.Syllable, len(parts))
	for i, p := range parts {
		base, _ := zhuyin.PinyinSyllable(p)
		syls[i] = zhuyin.Untone(base)
		if i == len(parts)-1 && tone > 0 {
			syls[i] = base.WithTone(tone)
		}
	}
	if last := syls[len(syls)-1]; strict && tone > 0 && !s.readingReachable(s.neutralFallback(last)) && !s.nearReachable(last) {
		s.bell = s.ZhuyinErrorHint // 這個聲調沒有字（hei2）：不收，改聲調或字母
		return false
	}
	s.pyKeys = ""
	// 前面的音節都沒打聲調、一定有字，直接插進組字；最後一個照一般音節收（解不出時原樣留下等規則都在 completeSyllable），
	// 整串只解碼一次，不是每個音節各解一次。
	s.compNear = nil
	for _, y := range syls[:len(syls)-1] {
		s.insert(y)
	}
	last := syls[len(syls)-1]
	var before zhuyin.Composer
	for _, r := range last.Base() {
		before.Add(r)
	}
	allow := s.allowRerank
	s.allowRerank = true // 同 finishUntoned：多了音節，讓神經評分一次
	s.completeSyllable(before, last)
	s.allowRerank = allow
	return true
}

// splitPinyin 把 keys 切成音節的拼法（見檔頭的「切法」）；切不成完整音節時 ok 為 false。
func (s *Session) splitPinyin(keys string, tone int) ([]string, bool) {
	fallback, ok := zhuyin.SplitPinyin(keys)
	if !ok {
		return nil, false
	}
	var letters []byte
	stops := map[int]bool{} // ' 的位置（字母索引）
	whole := true
	for _, chunk := range strings.Split(keys, string(zhuyin.PinyinSeparator)) {
		if chunk == "" {
			continue
		}
		if len(letters) > 0 {
			stops[len(letters)] = true
		}
		letters = append(letters, chunk...)
		_, one := zhuyin.PinyinSyllable(chunk)
		whole = whole && one
	}
	if whole || len(fallback) == 1 {
		return fallback, true // 每一段自己就是一個音節：沒有別的切法
	}
	if cuts := s.decodePinyinCuts(string(letters), stops, tone); cuts != nil {
		out := make([]string, 0, len(cuts))
		prev := 0
		for _, c := range cuts {
			out = append(out, string(letters[prev:c]))
			prev = c
		}
		return out, true
	}
	return fallback, true
}

// decodePinyinCuts 在按鍵圖上決定 letters 的切法：游標前同一段（字面文字之後）已經組好的音節照原本的音節格當前文，
// 後面接上每個字母一個位置，每段組得成音節的字母（最多 MaxPinyinLetters 個、不跨過 ' 的位置 stops）是一條音節弧；
// 自己就是一個音節的一段（兩個切點之間）只有整段那一條。最後一個音節帶聲調 tone（0 不打聲調）。
// 回傳每個音節在 letters 裡結束的位置；解不出路徑時回傳 nil。
func (s *Session) decodePinyinCuts(letters string, stops map[int]bool, tone int) []int {
	begin := s.cursor
	for begin > 0 && !s.literals[begin-1] {
		begin--
	}
	sylLat := s.lattice()
	g := make(input.Lattice, 0, s.cursor-begin+len(letters))
	g = append(g, sylLat[begin:s.cursor]...)
	off := len(g)
	g = append(g, make(input.Lattice, len(letters))...)
	arc := func(i, j int) {
		base, ok := zhuyin.PinyinSyllable(letters[i:j])
		if !ok {
			return
		}
		if j == len(letters) && tone > 0 {
			g[off+i] = append(g[off+i], input.Hyp{Syl: base.WithTone(tone), Span: j - i})
			return
		}
		for _, v := range zhuyin.Untone(base).Tones() {
			g[off+i] = append(g[off+i], input.Hyp{Syl: v, Untoned: true, Span: j - i})
		}
	}
	for a := 0; a < len(letters); {
		b := a + 1
		for b < len(letters) && !stops[b] {
			b++
		}
		if _, one := zhuyin.PinyinSyllable(letters[a:b]); one {
			arc(a, b)
		} else {
			for i := a; i < b; i++ {
				for j := i + 1; j <= min(b, i+zhuyin.MaxPinyinLetters); j++ {
					arc(i, j)
				}
			}
		}
		a = b
	}
	s.dec.Beam, s.dec.Continue = s.Opt.Beam, s.Opt.Continue
	s.dec.SeparateContinuation = s.Opt.SeparateContinuation
	s.dec.PrefixCacheKeys = s.Opt.PrefixCacheKeys
	evidence, penalty := s.dec.Evidence, s.dec.CorrectionPenalty
	defer func() { s.dec.Evidence, s.dec.CorrectionPenalty = evidence, penalty }()
	s.configureDocumentContext(begin)
	s.dec.Evidence, s.dec.CorrectionPenalty = nil, nil // 依位置的證據以音節為單位；切法只看詞庫與語言模型
	path, _ := s.dec.Best(g, s.locksIn(begin, s.cursor), false)
	if len(path) == 0 {
		return nil
	}
	s.pathSig = "" // 解碼器的前文設定給了這一段，下一次顯示要重新解
	var cuts []int
	for _, seg := range path {
		ends := seg.Ends()
		if ends == nil {
			for u := seg.Start + 1; u <= seg.End; u++ {
				ends = append(ends, u)
			}
		}
		for _, u := range ends {
			if u > off {
				cuts = append(cuts, u-off)
			}
		}
	}
	if len(cuts) == 0 || cuts[len(cuts)-1] != len(letters) {
		return nil
	}
	return cuts
}

// syllableDisplay 是音節給人看的寫法：注音排列是注音（Text：沒打聲調的不帶聲調、一聲不標），
// 漢語拼音是拼音加聲調數字（ㄕˋ 是 shi4，沒打聲調的 shi）。單字候選最後的原樣項目用它（#324）。
func (s *Session) syllableDisplay(y zhuyin.Syllable) string {
	if s.pinyinLayout() {
		if k := layout.Pinyin.Keys(y); k != "" {
			return k
		}
	}
	return y.Text()
}

// readingDisplay 是以空白分隔的讀音（詞庫寫法，例如近音候選的讀音）給人看的寫法，見 syllableDisplay。
func (s *Session) readingDisplay(reading string) string {
	if !s.pinyinLayout() || reading == "" {
		return reading
	}
	syls := zhuyin.ParseReading(reading)
	out := make([]string, len(syls))
	for i, y := range syls {
		out[i] = s.syllableDisplay(y)
	}
	return strings.Join(out, " ")
}
