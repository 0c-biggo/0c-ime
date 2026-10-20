package corpus

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// #172：日文、簡體句子整句不計；台灣也用的字（台、后）與注音符號、中點不算標記。
func TestScriptFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "STCharacters.txt")
	os.WriteFile(path, []byte("# c\n国\t國\n后\t後 后\n台\t臺 檯 颱 台\n这\t這\n发\t發 髮\n"), 0o644)
	simplified, err := SimplifiedOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []rune("国这发") {
		if !simplified[c] {
			t.Errorf("%c 是簡化字", c)
		}
	}
	for _, c := range []rune("后台") {
		if simplified[c] {
			t.Errorf("%c 的轉換目標含自己，不是只當簡化字用", c)
		}
	}
	taiwan := map[rune]bool{'台': true, '國': true, '這': true, '发': true} // 假設詞庫另有一般來源收了「发」
	f := NewScriptFilter(simplified, func(r rune) bool { return taiwan[r] })
	for s, want := range map[string]string{
		"今天空氣很好。":   Taiwan,
		"今日は空気がいい。": Japanese,
		"アニメの主題歌":   Japanese,
		"ｱﾆﾒ":       Japanese,
		"中国的国家":     Simplified,
		"这是":        Simplified,
		"头发":        Taiwan, // 「发」是台灣用字（taiwan），不當標記；「头」不在測試的簡化字表
		"ㄏㄏ好啦":      Taiwan,
		"約翰・藍儂":     Taiwan,
		"台北的天氣":     Taiwan,
		"後來":        Taiwan,
		"佢食咗飯":      Cantonese,
	} {
		if got := f.Foreign(s); got != want {
			t.Errorf("Foreign(%q) = %q，應為 %q", s, got, want)
		}
	}
}

func TestSentences(t *testing.T) {
	var got []string
	Sentences("第一句。第二句！\n今日は晴れ？最後", func(s string) { got = append(got, s) })
	want := []string{"第一句。", "第二句！", "\n", "今日は晴れ？", "最後"}
	if !slices.Equal(got, want) {
		t.Errorf("Sentences = %q，應為 %q", got, want)
	}
}
