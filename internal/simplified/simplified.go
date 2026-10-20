// Package simplified 內附 OpenCC 的 tw2s 字典，提供「輸出簡體字」設定用的轉換器（#118）。
//
// 字典是 OpenCC ver.1.4.2 的上游原文（opencc/，來源與雜湊見 opencc/README.md），編進執行檔裡，
// 不另外放進資料包：四個平台的引擎都是同一支 0c-server，跟著外殼更新就帶到。
// 轉換規則在 core/zhconv。
package simplified

import (
	"embed"
	"sync"

	"git.biggo.com/0c/ime/core/zhconv"
)

//go:embed opencc/*.txt opencc/LICENSE
var files embed.FS

// License 是 OpenCC 的授權全文（Apache-2.0），供第三方元件聲明使用。
func License() string { return read("LICENSE") }

func read(name string) string {
	b, err := files.ReadFile("opencc/" + name)
	if err != nil {
		panic(err) // 編進執行檔的檔案一定在
	}
	return string(b)
}

var load = sync.OnceValues(func() (*zhconv.Converter, error) {
	return zhconv.TW2S(zhconv.Data{
		CJKCompatibility:     read("CJK_Compatibility_Ideographs.txt"),
		TWVariants:           read("TWVariants.txt"),
		TWVariantsRevPhrases: read("TWVariantsRevPhrases.txt"),
		TSPhrases:            read("TSPhrases.txt"),
		TSCharacters:         read("TSCharacters.txt"),
	})
})

// TW2S 回傳台灣正體轉簡體的轉換器；第一次呼叫時才解析字典，之後共用同一份。
func TW2S() (*zhconv.Converter, error) { return load() }
