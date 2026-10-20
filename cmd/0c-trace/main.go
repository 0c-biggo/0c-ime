// 0c-trace 檢視本機 UX 飛行紀錄；預設只印統計，-details 才顯示輸入文字。
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/0c-biggo/0c-ime/core/proto"
)

type event struct {
	Server      map[string]string `json:"server"`
	Event       string            `json:"event"`
	Run         string            `json:"run"`
	Seq         uint64            `json:"seq"`
	Session     uint64            `json:"session"`
	UnixUS      int64             `json:"unix_us"`
	DurationUS  int64             `json:"duration_us"`
	EngineUS    int64             `json:"engine_us"`
	Dropped     int               `json:"dropped"`
	Reconnected bool              `json:"reconnected"`
	Request     json.RawMessage   `json:"request"`
	Before      proto.Snapshot    `json:"before"`
	After       proto.Snapshot    `json:"after"`
}

func readEvents(dir string, since time.Time) (events []event, bytes int64, malformed int, err error) {
	for i := 3; i >= 0; i-- {
		name := "trace.jsonl"
		if i > 0 {
			name = fmt.Sprintf("trace.%d.jsonl", i)
		}
		f, e := os.Open(filepath.Join(dir, name))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, bytes, malformed, e
		}
		if stat, e := f.Stat(); e == nil {
			bytes += stat.Size()
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 4096), 128*1024)
		for scan.Scan() {
			var item event
			if json.Unmarshal(scan.Bytes(), &item) != nil {
				malformed++
				continue
			}
			if item.UnixUS >= since.UnixMicro() {
				events = append(events, item)
			}
		}
		e = scan.Err()
		f.Close()
		if e != nil {
			return nil, bytes, malformed, e
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].UnixUS != events[j].UnixUS {
			return events[i].UnixUS < events[j].UnixUS
		}
		if events[i].Run != events[j].Run {
			return events[i].Run < events[j].Run
		}
		return events[i].Seq < events[j].Seq
	})
	return
}

func unchanged(e event) bool {
	a, b := e.Before, e.After
	return b.Handled && b.Commit == "" && a.Mode == b.Mode && a.Cursor == b.Cursor &&
		a.Suggestion == b.Suggestion && reflect.DeepEqual(a.Preedit, b.Preedit) &&
		reflect.DeepEqual(a.Candidates, b.Candidates) && reflect.DeepEqual(a.Notices, b.Notices)
}

func defaultDir() string {
	if dir := os.Getenv("ZUIN_TRACE_DIR"); dir != "" {
		return dir
	}
	if dir := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "zuin", "trace")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "zuin", "trace")
}

func main() {
	dir := flag.String("dir", defaultDir(), "UX 紀錄目錄")
	last := flag.Duration("last", 15*time.Minute, "最近多久；0 表示全部保留紀錄")
	details := flag.Bool("details", false, "顯示操作及輸入文字（預設只有統計）")
	limit := flag.Int("limit", 80, "詳細模式最多顯示幾筆")
	corrections := flag.Bool("corrections", false, "只分析明確改選區間；搭配 -details 才印被替換及選取文字")
	session := flag.Uint64("session", 0, "只看指定 session；0 不篩選")
	run := flag.String("run", "", "只看指定外殼執行批次")
	baselineJSON := flag.Bool("baseline", false, "輸出依程式與資料版本分組的 JSON 操作基線；不含輸入文字")
	sinceArg := flag.String("since", "", "起點 RFC3339（含）；覆蓋 -last")
	untilArg := flag.String("until", "", "終點 RFC3339（不含）；預設現在")
	flag.Parse()
	if *last < 0 || *limit < 1 {
		fmt.Fprintln(os.Stderr, "last 必須 ≥ 0，limit 必須 ≥ 1")
		os.Exit(2)
	}
	since := time.Unix(0, 0)
	if *last > 0 {
		since = time.Now().Add(-*last)
	}
	until := time.Now()
	for _, arg := range []struct {
		value  string
		target *time.Time
	}{{*sinceArg, &since}, {*untilArg, &until}} {
		if arg.value != "" {
			t, err := time.Parse(time.RFC3339Nano, arg.value)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			*arg.target = t
		}
	}
	if !since.Before(until) {
		fmt.Fprintln(os.Stderr, "since 必須早於 until")
		os.Exit(2)
	}
	items, bytes, bad, err := readEvents(*dir, since)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var events []event
	for _, e := range items {
		if e.UnixUS < until.UnixMicro() && (*session == 0 || e.Session == *session) && (*run == "" || e.Run == *run) {
			events = append(events, e)
		}
	}
	if *baselineJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(baseline(events, since, until, bad)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	fmt.Printf("紀錄：%s；檔案總量 %.2f MiB；範圍內 %d 筆事件；無法解析 %d 行\n", *dir, float64(bytes)/(1024*1024), len(events), bad)
	var durations []int64
	requests, reconnects, noChange, dropped := 0, 0, 0, 0
	var engineMax, presentMax int64
	for _, e := range events {
		dropped += e.Dropped
		if e.Event == "present" {
			presentMax = max(presentMax, e.DurationUS)
		}
		if e.Event != "request" {
			continue
		}
		requests++
		durations = append(durations, e.DurationUS)
		engineMax = max(engineMax, e.EngineUS)
		if e.Reconnected {
			reconnects++
		}
		if unchanged(e) {
			noChange++
		}
	}
	fmt.Printf("請求 %d；新連線／重連 %d；已處理但可見狀態未變 %d；佇列／大小限制丟棄 %d\n", requests, reconnects, noChange, dropped)
	var changes []correction
	for _, e := range events {
		if c, ok := selectedCorrection(e); ok {
			changes = append(changes, c)
		}
	}
	fmt.Printf("明確替換候選 %d 次（只確認改選範圍；普通提交不當成正確答案）\n", len(changes))
	if len(durations) > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		q := func(percent int) float64 { return float64(durations[(len(durations)-1)*percent/100]) / 1000 }
		fmt.Printf("往返含重試：p50 %.2f ms、p95 %.2f ms、p99 %.2f ms、最大 %.2f ms；核心最大 %.2f ms；呈現呼叫最大 %.2f ms\n", q(50), q(95), q(99), q(100), float64(engineMax)/1000, float64(presentMax)/1000)
	}
	if !*details {
		return
	}
	if *corrections {
		for _, c := range changes[max(0, len(changes)-*limit):] {
			fmt.Printf("run=%s session=%d seq=%d @%d %q → %q\n", c.Run, c.Session, c.Seq, c.Start, c.Before, c.After)
		}
		return
	}
	for _, e := range events[max(0, len(events)-*limit):] {
		fmt.Printf("%s run=%s session=%d seq=%d %s %.2fms\n", time.UnixMicro(e.UnixUS).Local().Format("01-02 15:04:05.000"), e.Run, e.Session, e.Seq, e.Event, float64(e.DurationUS)/1000)
		if e.Event == "request" {
			fmt.Printf("  %s\n  %q @%d → %q @%d；提交 %q；補全 %q；候選 %v\n", e.Request,
				short(e.Before.Text()), e.Before.Cursor.Rune, short(e.After.Text()), e.After.Cursor.Rune,
				short(e.After.Commit), short(e.After.Suggestion), e.After.Candidates)
		}
	}
}

func short(s string) string {
	r := []rune(s)
	if len(r) > 160 {
		return string(r[:160]) + "…"
	}
	return s
}
