// 0c-compile 把 data/ 的來源格式編譯成執行期資料包（docs/13、docs/19）。
// 任何驗證錯誤都會阻擋出版；匯入檔中略過的行與字詞不一致寫在 report.txt。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/0c-biggo/0c-ime/internal/compile"
	"github.com/0c-biggo/0c-ime/internal/datadir"
)

func main() {
	src := flag.String("data", "data", "來源目錄")
	out := flag.String("out", "build/data", "輸出的資料包目錄")
	o := compile.DefaultOptions()
	flag.BoolVar(&o.Public, "public", false, "公開包：拒絕不可散布或缺少授權聲明的來源；預設為研究／開發包")
	// 詞頻與 bigram 的預設來源 ID 屬於 data/，配置放在命令列這一層。
	// 0c_collect 的 bigram 分成多字配對（-ns）與單字配對（-ss）：單字配對多是未收錄名稱被拆開的碎片，權重低一些。
	o.Unigram, o.Bigram = "mcbopomofo=2,0c_collect=1", "chewing-v4=3,zhwiki=1,0c_collect-ns=3,0c_collect-ss=2"
	// 罕用字（全字庫獨有、小麥注音中不在 Big5 者）排在常用字之後，避免候選第一頁滿是
	// 字型沒有的擴充區字（使用者回報「選字會出現很多亂碼」），也不讓日文新字體與簡化字
	// 排到常用字前面（#171：ㄑㄧˋ 的「気」排在「氣」前面）
	o.Rare = "cns11643,mcbopomofo-rare"
	flag.BoolVar(&o.RelativeReadings, "relative-readings", true, "以詞內最高正常讀音為基準保留相對成本（來源等級不變；false 可重跑舊公式）")
	flag.BoolVar(&o.InheritVariants, "inherit", o.InheritVariants, "L2：單字變體讀音繼承到詞")
	flag.BoolVar(&o.YiBuSandhi, "yibu", o.YiBuSandhi, "L3：一、不的變調")
	flag.BoolVar(&o.NeutralTone, "neutral", o.NeutralTone, "L3：輕聲以本調輸入")
	flag.BoolVar(&o.PolyphoneCommon, "polyphone", o.PolyphoneCommon, "L3：破音字以主讀音輸入")
	flag.IntVar(&o.MaxVariants, "max-variants", o.MaxVariants, "每個詞由規則產生的讀音上限")
	flag.StringVar(&o.Unigram, "unigram", o.Unigram, "詞頻來源與權重，如 mcbopomofo=1,chewing-v4=1；空白為全部等權重")
	flag.StringVar(&o.UnigramFallback, "unigram-fallback", "chewing-v4,zhwiki", "補足主要詞頻缺口的共同支持來源（至少兩個；空白停用）")
	flag.Float64Var(&o.FallbackScale, "fallback-scale", 0.25, "補足頻率取各來源正規化頻率的最小值乘此係數，範圍 (0,1]")
	flag.StringVar(&o.Bigram, "bigram", o.Bigram, "bigram 來源 ID（data/lm/bigram/<ID>.tsv）；空白為不用 bigram")
	flag.Float64Var(&o.UnigramAlpha, "alpha", o.UnigramAlpha, "詞頻平滑：每個詞加上的虛擬次數")
	flag.IntVar(&o.BigramMax, "bigram-max", o.BigramMax, "bigram 保留上限（熵剪枝；0 為不剪枝）")
	flag.Float64Var(&o.PruneMu, "prune-mu", 0.7, "剪枝時假設的執行期內插權重 μ")
	flag.StringVar(&o.PruneBy, "prune-by", "entropy", "剪枝排序標準：entropy、joint、ratio")
	flag.StringVar(&o.Demote, "demote", o.Demote, "罕用來源的沒有詞頻的字詞 unigram 乘上係數，如 cns11643=0.01（影響解碼）")
	flag.StringVar(&o.Rare, "rare", o.Rare, "罕用來源（逗號分隔）：只來自這些來源的字詞在候選視窗排在其他字之後")
	flag.StringVar(&o.Exclude, "exclude", o.Exclude, "排除的來源 ID（逗號分隔），供消融使用")
	flag.StringVar(&o.Boost, "boost", o.Boost, "沒有詞頻的詞依來源給虛擬次數，如 moi-places=5")
	seal := flag.String("seal", "", "只混淆既有資料包目錄中的字典檔（打包時使用），不編譯")
	flag.Parse()

	if *seal != "" {
		if err := datadir.SealDir(*seal); err != nil {
			fmt.Fprintln(os.Stderr, "0c-compile:", err)
			os.Exit(1)
		}
		if _, err := datadir.Open(*seal); err != nil {
			fmt.Fprintln(os.Stderr, "0c-compile: 混淆後無法載入：", err)
			os.Exit(1)
		}
		return
	}

	d, errs, err := compile.Load(*src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-compile:", err)
		os.Exit(1)
	}
	res := compile.Build(d, o)
	res.Errors = append(errs, res.Errors...)
	fmt.Print(res.Report.String())
	if len(res.Errors) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d 個錯誤，不出版：\n", len(res.Errors))
		for _, e := range res.Errors {
			fmt.Fprintln(os.Stderr, "  "+e.Error())
		}
		os.Exit(1)
	}
	if err := res.Write(*out, *src+"/sources/licenses", o); err != nil {
		fmt.Fprintln(os.Stderr, "0c-compile:", err)
		os.Exit(1)
	}
	fmt.Printf("\n已寫入 %s\n", *out)
}
