package englishpack

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ImportLists normalizes hash-pinned upstream exports, with no network or
// upstream executable invocation. A source update requires reviewed new hashes.
func ImportLists(src, dir string) error {
	b, err := read(filepath.Join(src, "source.json"), 64<<10)
	if err != nil {
		return err
	}
	var meta SourceMetadata
	if err = json.Unmarshal(b, &meta); err != nil {
		return err
	}
	if len(meta.Exports) != 7 {
		return fmt.Errorf("英文匯出清單缺少等級")
	}
	grades := map[string]int{}
	blocked := map[string]bool{}
	seen := map[int]bool{}
	for _, export := range meta.Exports {
		if !validSize(export.Size) || seen[export.Size] || export.File != fmt.Sprintf("%d.txt", export.Size) || !hashOK(export.SHA256) {
			return fmt.Errorf("英文匯出清單不合法")
		}
		seen[export.Size] = true
		b, err := read(filepath.Join(dir, export.File), 16<<20)
		if err != nil {
			return err
		}
		if Sum(b) != export.SHA256 {
			return fmt.Errorf("英文匯出來源雜湊不符：%s", export.File)
		}
		scan := bufio.NewScanner(bytes.NewReader(b))
		for scan.Scan() {
			line := scan.Text()
			w := strings.TrimSuffix(line, "!")
			if !lowerWord(w) {
				continue
			}
			if grade, ok := grades[w]; !ok || export.Size < grade {
				grades[w] = export.Size
			}
			if line != w {
				blocked[w] = true
			}
		}
		if err := scan.Err(); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(grades))
	for w := range grades {
		keys = append(keys, w)
	}
	sort.Strings(keys)
	var out bytes.Buffer
	for _, w := range keys {
		flag := 0
		if blocked[w] {
			flag = 1
		}
		fmt.Fprintf(&out, "%s\t%d\t%d\n", w, grades[w], flag)
	}
	if len(keys) != meta.Words || Sum(out.Bytes()) != meta.NormalizedSHA256 {
		return fmt.Errorf("英文正規化結果與固定來源不符")
	}
	tmp, err := os.CreateTemp(src, ".esdb-")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err = tmp.Write(out.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(0644); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(src, "esdb.tsv"))
}
