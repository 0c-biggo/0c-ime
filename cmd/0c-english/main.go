// 0c-english compiles the independent, licensed English vocabulary pack.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/0c-biggo/0c-ime/internal/englishpack"
)

func main() {
	src := flag.String("source", "data/english", "已審查的英文詞表來源目錄")
	out := flag.String("out", "build/english", "輸出英文資料包（須為新目錄或空目錄）")
	lists := flag.String("import-lists", "", "先從固定雜湊的 ESDB 匯出檔重建來源 TSV（不下載或執行上游程式）")
	flag.Parse()
	if *lists != "" {
		if err := englishpack.ImportLists(*src, *lists); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if err := englishpack.Compile(*src, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	d, _, err := englishpack.Load(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("英文資料包：%s，%d 詞\n", *out, d.Len())
}
