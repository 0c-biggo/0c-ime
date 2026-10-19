package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// nameList 是一份政府開放資料的名稱清單：CSV 的某一欄，正規化成使用者會打的寫法後，
// 以詞庫斷詞標音（annotateNames）。每份清單是獨立的來源 ID，可以分開評測、分開套用。
type nameList struct {
	origin string
	files  []string // 相對於 -raw
	column string
	latest string                           // 非空：只取這一欄數值最大的列（學年度、統計年月）
	keep   func(row map[string]string) bool // nil＝全收
	forms  func(name string) []string
	maxLen int
	header string
}

var (
	ownerPrefix = regexp.MustCompile(`^(國立|市立|縣立|私立|公立)`)
	trailParen  = regexp.MustCompile(`[（(][^）)]*[）)]$`)
	corporate   = regexp.MustCompile(`公司|有限|股份|協會|學會|工會|公會|基金會|法人|事務所|合作社|商行|企業社|工作室`)
	agencyTail  = regexp.MustCompile(`(府|院|部|會|署|局|處|所|館|中心|隊|站|廳|校|學)$`)
)

var nameLists = []nameList{
	{
		origin: "moe-schools",
		files:  []string{"moe/u1.csv", "moe/high.csv", "moe/j1.csv", "moe/e1.csv"},
		column: "學校名稱", latest: "學年度", maxLen: 8,
		// 「市立崇明國中」「國立政治大學」：全名與去掉公私立的寫法都收
		forms: func(n string) []string {
			n = trailParen.ReplaceAllString(n, "")
			return []string{n, ownerPrefix.ReplaceAllString(n, "")}
		},
		header: "# 詞面\t讀音\tgrade\tnorm\torigin（由 0c-import 自教育部統計處大專、高中、國中、國小名錄產生；讀音以詞庫斷詞自動標注）",
	},
	{
		origin: "nhi-hospitals",
		files:  []string{"nhi/center.csv", "nhi/regional.csv", "nhi/district.csv"},
		column: "醫事機構名稱", maxLen: 8,
		// 「長庚醫療財團法人高雄長庚紀念醫院」→ 高雄長庚紀念醫院；「…附設中和紀念醫院」→ 中和紀念醫院
		forms: func(n string) []string {
			if i := strings.LastIndex(n, "法人"); i >= 0 {
				n = n[i+len("法人"):]
			}
			n = ownerPrefix.ReplaceAllString(trailParen.ReplaceAllString(n, ""), "")
			out := []string{n}
			if i := strings.LastIndex(n, "附設"); i >= 0 {
				out = append(out, n[i+len("附設"):])
			}
			return out
		},
		header: "# 詞面\t讀音\tgrade\tnorm\torigin（由 0c-import 自健保署特約醫學中心、區域醫院、地區醫院名單產生；讀音以詞庫斷詞自動標注）",
	},
	{
		origin: "moi-villages",
		files:  []string{"moi/villages.csv"},
		column: "村里", latest: "統計年月", maxLen: 8,
		forms:  func(n string) []string { return []string{n} },
		header: "# 詞面\t讀音\tgrade\tnorm\torigin（由 0c-import 自內政部村里戶數人口資料的村里名稱產生；讀音以詞庫斷詞自動標注）",
	},
	{
		origin: "gov-agencies",
		files:  []string{"agencies/org.csv"},
		column: "機關名稱", maxLen: 8,
		// 公文通訊錄也收公司行號與團體：只留有機關代碼、不是內部單位、名稱不像公司團體、以機關字尾結尾的
		keep: func(r map[string]string) bool {
			n := r["機關名稱"]
			return r["機關代碼"] != "" && r["單位代碼"] == "" && !corporate.MatchString(n) && agencyTail.MatchString(trailParen.ReplaceAllString(n, ""))
		},
		forms:  func(n string) []string { return []string{trailParen.ReplaceAllString(n, "")} },
		header: "# 詞面\t讀音\tgrade\tnorm\torigin（由 0c-import 自筆硯公文製作通訊錄的機關名稱產生；讀音以詞庫斷詞自動標注）",
	},
}

// importNameLists 讀每份名稱清單，寫出 words-<來源>.tsv。缺檔的清單略過。
func importNameLists(data, raw, imp string) []string {
	var out []string
	for _, l := range nameLists {
		names := map[string]bool{}
		missing := ""
		for _, f := range l.files {
			rows, err := readCSV(filepath.Join(raw, f))
			if err != nil {
				missing = f
				break
			}
			latest := 0.0
			if l.latest != "" {
				for _, r := range rows {
					if v, err := strconv.ParseFloat(r[l.latest], 64); err == nil && v > latest {
						latest = v
					}
				}
			}
			for _, r := range rows {
				if l.latest != "" {
					if v, err := strconv.ParseFloat(r[l.latest], 64); err != nil || v != latest {
						continue
					}
				}
				if l.keep != nil && !l.keep(r) {
					continue
				}
				for _, n := range l.forms(strings.TrimSpace(r[l.column])) {
					names[n] = true
				}
			}
		}
		if missing != "" {
			out = append(out, l.origin+"：略過（沒有 "+missing+"）")
			continue
		}
		out = append(out, l.origin+"："+annotateNames(data, imp, names, l.origin, l.maxLen, "words-"+l.origin+".tsv", l.header))
	}
	return out
}

// readCSV 讀有標題列的 CSV（UTF-8，可有 BOM），回傳以標題為鍵的列。
func readCSV(p string) ([]map[string]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.LazyQuotes, r.FieldsPerRecord = true, -1
	all, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, nil
	}
	head := all[0]
	head[0] = strings.TrimPrefix(head[0], "\uFEFF")
	var out []map[string]string
	for _, rec := range all[1:] {
		m := map[string]string{}
		for i, h := range head {
			if i < len(rec) {
				m[strings.TrimSpace(h)] = rec[i]
			}
		}
		out = append(out, m)
	}
	return out, nil
}
