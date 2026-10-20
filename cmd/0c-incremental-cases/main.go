// 0c-incremental-cases 凍結逐音節實際 session 候選；答案只寫在評測標籤，不參與召回。
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/session"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
)

type capture struct{ request *rerank.Request }

func (c *capture) Score(r rerank.Request) ([]float64, error) { c.request = &r; return nil, nil }
func main() {
	data := flag.String("data", "", "系統資料包")
	input := flag.String("input", "", "既有開發資料 JSONL")
	output := flag.String("output", "", "新輸出 JSONL")
	partial := flag.Bool("allow-partial", false, "診斷等長前綴候選；不刪掉候選")
	split := flag.String("split", "dev", "資料分割；dev/calibration/test，輸出仍不含模型評分")
	flag.Parse()
	if err := run(*data, *input, *output, *partial, *split); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(data, input, output string, partial bool, split string) (retErr error) {
	eng, err := datadir.Open(data)
	if err != nil {
		return err
	}
	in, err := os.Open(input)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	defer func() {
		if retErr != nil {
			_ = os.Remove(output)
		}
	}()
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), 1<<20)
	writer := json.NewEncoder(out)
	count := 0
	for scan.Scan() {
		var row struct {
			Publisher string   `json:"publisher"`
			Date      string   `json:"date"`
			Cohort    string   `json:"cohort"`
			ID        string   `json:"case_id"`
			Group     string   `json:"group_id"`
			Split     string   `json:"split"`
			Targets   []string `json:"targets"`
			Query     struct {
				Reading []struct {
					Base   string `json:"base"`
					Tone   int    `json:"tone"`
					Status string `json:"status"`
				} `json:"reading"`
				Dictionary string `json:"dictionary_sha256"`
			} `json:"query"`
		}
		if err = json.Unmarshal(scan.Bytes(), &row); err != nil {
			return err
		}
		if row.Split != split || split != "dev" && split != "calibration" && split != "test" {
			return fmt.Errorf("input partition mismatch")
		}
		if len(row.Targets) != 1 || len([]rune(row.Targets[0])) != len(row.Query.Reading) {
			return fmt.Errorf("unaligned input")
		}
		s := session.New(personal.NewModel(eng.Model.Source(), eng.Model.LM))
		if eng.DocumentContextWeight != nil {
			s.Opt.DocumentContextWeight = *eng.DocumentContextWeight
		}
		observer := &capture{}
		s.Rerank = rerank.Options{Scorer: observer, AllowPartial: partial}
		target := []rune(row.Targets[0])
		reading := []zhuyin.Syllable{}
		for i, item := range row.Query.Reading {
			if item.Status != "complete" || item.Tone < 1 || item.Tone > 5 {
				return fmt.Errorf("unsupported source reading")
			}
			syl := zhuyin.Syllable(item.Base).WithTone(item.Tone)
			reading = append(reading, syl)
			observer.request = nil
			var snap proto.Snapshot
			for _, key := range layout.Standard.Keys(syl) {
				snap = s.Handle(proto.Request{Type: proto.Key, Key: string(key)})
			}
			record := map[string]any{"publisher": row.Publisher, "date": row.Date, "cohort": row.Cohort, "case_id": row.ID, "group_id": row.Group, "prefix": i + 1, "target": string(target[:i+1]), "production": snap.Text(), "reading": reading, "dictionary_sha256": row.Query.Dictionary, "request": observer.request}
			if observer.request != nil && observer.request.Candidates[0].Text != snap.Text() {
				return fmt.Errorf("candidate/session mismatch")
			}
			if err = writer.Encode(record); err != nil {
				return err
			}
			count++
		}
	}
	if err = scan.Err(); err != nil {
		return err
	}
	fmt.Printf("Frozen %d prefixes without model scores.\n", count)
	return nil
}
