package engine

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

// Replay 把 JSONL 的請求逐行送進一個新的 session，把快照逐行寫到 out。
// 這就是 golden transcript：同一份請求檔在 CLI、各種橋接與各平台重播，輸出必須逐位元組相同。
// 以 # 起始的行是註解，原樣保留在輸出中，讓 golden 檔可以閱讀。
func (e *Engine) Replay(reqs io.Reader, out io.Writer) error {
	id := e.OpenSession()
	defer e.CloseSession(id)
	sc := bufio.NewScanner(reqs)
	for ln := 1; sc.Scan(); ln++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] == '#' {
			fmt.Fprintf(out, "%s\n", line)
			continue
		}
		snap, err := e.Handle(id, line)
		if err != nil {
			return fmt.Errorf("第 %d 行：%w", ln, err)
		}
		fmt.Fprintf(out, "%s\n", snap)
	}
	return sc.Err()
}
