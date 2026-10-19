package behavior

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/personal"
)

var update = flag.Bool("update", false, "以目前的行為重寫 golden transcript")

// Golden transcript：協定層級的「請求 → 快照」序列。同一批請求檔日後也會經
// C ABI、行程間傳輸與各平台外殼重播，輸出必須逐位元組相同（docs/19）。
func TestTranscripts(t *testing.T) {
	reqs, _ := filepath.Glob("../../testdata/transcripts/*.req.jsonl")
	if len(reqs) == 0 {
		t.Fatal("找不到 transcript")
	}
	for _, req := range reqs {
		name := strings.TrimSuffix(filepath.Base(req), ".req.jsonl")
		t.Run(name, func(t *testing.T) {
			in, err := os.ReadFile(req)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := newUser(t, personal.DefaultParams()).e.Replay(bytes.NewReader(in), &out); err != nil {
				t.Fatal(err)
			}
			golden := strings.TrimSuffix(req, ".req.jsonl") + ".golden.jsonl"
			if *update {
				if err := os.WriteFile(golden, out.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v（以 -update 產生）", err)
			}
			if !bytes.Equal(out.Bytes(), want) {
				t.Errorf("與 golden 不符；確認是預期的行為改變後以 -update 重寫\n實際：\n%s", out.String())
			}
		})
	}
}
