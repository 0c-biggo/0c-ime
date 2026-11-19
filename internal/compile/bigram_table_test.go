package compile

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lm"
)

// pruneBySort 是改用堆積之前的 pruneBigrams：全部計分、排序後取前 n 筆。
func pruneBySort(bi map[[2]string]float64, uni map[string]float64, n int, mu float64, by string, keep map[[2]string]bool) map[[2]string]float64 {
	maxUni := 0.0
	for _, p := range uni {
		maxUni = math.Max(maxUni, p)
	}
	type scored struct {
		k [2]string
		s float64
	}
	var all []scored
	for k, p := range bi {
		if keep[k] {
			continue
		}
		pl := uni[k[0]]
		if k[0] == lm.BOS {
			pl = maxUni
		}
		gain := math.Log1p(mu * p / ((1 - mu) * uni[k[1]]))
		s := pl * p * gain
		switch by {
		case "joint":
			s = pl * p
		case "ratio":
			s = gain
		}
		all = append(all, scored{k, s})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].s != all[j].s {
			return all[i].s > all[j].s
		}
		return all[i].k[0]+"\t"+all[i].k[1] < all[j].k[0]+"\t"+all[j].k[1]
	})
	out := map[[2]string]float64{}
	for k := range keep {
		out[k] = bi[k]
	}
	for _, x := range all[:max(0, min(len(all), n-len(out)))] {
		out[x.k] = bi[x.k]
	}
	return out
}

// 堆積選出的集合要和全部排序取前 n 筆完全相同，包括大量同分（機率只取少數幾個值）與人工 bigram。
func TestBigramPruneMatchesFullSort(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	words := []string{lm.BOS}
	uni := map[string]float64{}
	for i := range 40 {
		w := fmt.Sprintf("詞%d", i)
		words = append(words, w)
		uni[w] = float64(1+r.Intn(4)) / 100
	}
	for trial := range 60 {
		bi := map[[2]string]float64{}
		keep := map[[2]string]bool{}
		for range 300 {
			k := [2]string{words[r.Intn(len(words))], words[1+r.Intn(len(words)-1)]}
			bi[k] = float64(1+r.Intn(3)) / 4
			if trial%2 == 1 && r.Intn(20) == 0 {
				keep[k] = true
			}
		}
		for _, by := range []string{"entropy", "joint", "ratio"} {
			for _, n := range []int{0, 1, len(keep), len(keep) + 1, len(bi) / 3, len(bi) - 1, len(bi), len(bi) + 5} {
				want := pruneBySort(bi, uni, n, 0.7, by, keep)
				got := pruneBigrams(bi, uni, n, 0.7, by, keep)
				if len(got) != len(want) {
					t.Fatalf("trial %d %s n=%d：%d 筆，應為 %d", trial, by, n, len(got), len(want))
				}
				for k, p := range want {
					if got[k] != p {
						t.Fatalf("trial %d %s n=%d：%v 應為 %v，得到 %v", trial, by, n, k, p, got[k])
					}
				}
			}
		}
	}
}

// bigramTable.read 的篩選與累加要和原本以字串為鍵時相同：前詞可以是句首，詞不行；詞庫外的配對略過。
func TestBigramTableRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bi.tsv")
	data := "# 註解\n\n<s>\t企業\t0.5\n企業\t風景\t0.25\n企業\t<s>\t0.5\n外面\t風景\t0.5\n企業\t風景\t0.5\n"
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	tb := newBigramTable(map[string]bool{"企業": true, "風景": true, "停用": false})
	if err := tb.read(p, 2); err != nil {
		t.Fatal(err)
	}
	got := tb.pairs()
	want := map[[2]string]float64{{lm.BOS, "企業"}: 1, {"企業", "風景"}: 1.5}
	if len(got) != len(want) || got[[2]string{lm.BOS, "企業"}] != 1 || got[[2]string{"企業", "風景"}] != 1.5 {
		t.Errorf("%v，應為 %v", got, want)
	}
	if err := os.WriteFile(p, []byte("企業\t風景\t2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tb.read(p, 1); err == nil {
		t.Error("不合法的機率應報錯")
	}
}
