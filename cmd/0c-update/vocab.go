package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"git.biggo.com/0c/ime/bridge/ipc"
	"git.biggo.com/0c/ime/core/engine"
	"git.biggo.com/0c/ime/core/vocab"
	"git.biggo.com/0c/ime/internal/autoupdate"
)

// vocab 指令（#122）：自訂詞彙的管理，設定程式、本機 API 與測試共用。
// 修改交給執行中的引擎（唯一的寫入者）；引擎沒有執行時拿個人資料目錄的寫入者鎖直接讀寫 vocab.json。

const vocabUsage = `用法：0c-update vocab <指令> [參數] [-json] [-source manual|compose|assistant]
  status                         版本與數量
  list [-query 文字或注音] [-offset N] [-limit N]
  add 詞 讀音 [詞 讀音 ...]       讀音的音節以空白分隔（一個讀音一個參數，例如 "ㄔㄣˊ ㄧˊ ㄐㄩㄣ"）
                                 英文詞（ls、kubectl：英文字母、數字與 - _ .）不給讀音，混打時照英文送出
  add -                          從標準輸入讀 JSON：[{"word":"陳怡君","readings":["ㄔㄣˊ ㄧˊ ㄐㄩㄣ"]},{"word":"ls"}]
  remove 詞 [詞 ...]
  readings 詞 讀音 [讀音 ...]      改寫這個詞的全部讀音
  undo 批次編號
  batches [批次編號]              最近的變更；給編號時列出明細
  rank [詞 ...]                  只打這組注音時排第幾、前面是哪個詞
  annotate 詞 [詞 ...]           依詞庫建議讀音，標出要確認的破音字
  learned                        自動學到的片語
  keep 詞 [詞 ...]               學到的片語轉成自訂詞
  forget 詞 [詞 ...]             撤銷自動學到的詞
  phrases | phrase-add 文字 ... | phrase-remove 編號 ... | phrase-move 編號 位置
  watch [-since 版本]            版本改變時印出一行 JSON（給管理頁即時同步）
  request                        從標準輸入讀一個完整的指令（JSON），印出結果`

// vocabRequestTimeout 是一個指令的上限；rank 可能要解碼幾百個詞。
const vocabRequestTimeout = 30 * time.Second

func vocabCommand(args []string) error {
	asJSON, source, rest, opts, err := parseVocabArgs(args)
	if err != nil {
		return usageError(err.Error())
	}
	if len(rest) == 0 {
		return usageError(vocabUsage)
	}
	op, params := rest[0], rest[1:]
	if op == "watch" {
		return vocabWatch(opts)
	}
	req := engine.VocabRequest{Op: op, Source: source, Query: opts["query"]}
	req.Offset, _ = strconv.Atoi(opts["offset"])
	req.Limit, _ = strconv.Atoi(opts["limit"])
	switch op {
	case engine.VocabStatus, engine.VocabList, engine.VocabLearned, engine.VocabPhrases:
		if len(params) > 0 {
			return usageError(vocabUsage)
		}
	case engine.VocabAdd:
		if len(params) == 1 && params[0] == "-" {
			if err := json.NewDecoder(os.Stdin).Decode(&req.Words); err != nil {
				return usageError("標準輸入不是詞的 JSON 陣列：" + err.Error())
			}
			break
		}
		var ok bool
		if req.Words, ok = addArgs(params); !ok {
			return usageError(vocabUsage)
		}
	case engine.VocabRemove, engine.VocabKeep, engine.VocabForget, engine.VocabPhraseAdd, engine.VocabAnnotate:
		if len(params) == 0 {
			return usageError(vocabUsage)
		}
		req.Texts = params
	case engine.VocabRank:
		req.Texts = params
	case engine.VocabReadings:
		if len(params) < 2 {
			return usageError(vocabUsage)
		}
		req.Text, req.Readings = params[0], params[1:]
	case engine.VocabUndo, engine.VocabBatches:
		if op == engine.VocabUndo && len(params) != 1 || len(params) > 1 {
			return usageError(vocabUsage)
		}
		if len(params) == 1 {
			if req.Batch, err = strconv.ParseUint(params[0], 10, 64); err != nil {
				return usageError("批次編號要是數字")
			}
		}
	case engine.VocabPhraseRemove:
		for _, p := range params {
			id, err := strconv.ParseUint(p, 10, 64)
			if err != nil {
				return usageError("片語編號要是數字")
			}
			req.IDs = append(req.IDs, id)
		}
		if len(req.IDs) == 0 {
			return usageError(vocabUsage)
		}
	case engine.VocabPhraseMove:
		if len(params) != 2 {
			return usageError(vocabUsage)
		}
		if req.ID, err = strconv.ParseUint(params[0], 10, 64); err != nil {
			return usageError("片語編號要是數字")
		}
		if req.To, err = strconv.Atoi(params[1]); err != nil {
			return usageError("位置要是數字")
		}
	case "request":
		if len(params) > 0 {
			return usageError(vocabUsage)
		}
		if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
			return usageError("標準輸入不是詞彙指令的 JSON：" + err.Error())
		}
		asJSON = true
	default:
		return usageError(vocabUsage)
	}
	resp, err := vocabDo(dialEngine, basePath(), req)
	if err != nil {
		return vocabFail(asJSON, &vocab.Error{Code: "engine", Item: -1, Msg: err.Error()})
	}
	if asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
			return err
		}
	} else if resp.OK {
		printVocab(os.Stdout, req.Op, resp)
	}
	if !resp.OK {
		if asJSON {
			return errReported
		}
		return errors.New(resp.Error.Msg)
	}
	return nil
}

// addArgs 把 add 的參數轉成詞：漢字詞後面跟一個讀音，英文詞（vocab.IsEnglish）沒有讀音。
func addArgs(params []string) ([]vocab.Entry, bool) {
	var out []vocab.Entry
	for i := 0; i < len(params); i++ {
		if vocab.IsEnglish(strings.TrimSpace(params[i])) {
			out = append(out, vocab.Entry{Text: params[i]})
			continue
		}
		if i+1 >= len(params) {
			return nil, false
		}
		out = append(out, vocab.Entry{Text: params[i], Readings: []string{params[i+1]}})
		i++
	}
	return out, len(out) > 0
}

func parseVocabArgs(args []string) (asJSON bool, source string, rest []string, opts map[string]string, err error) {
	opts = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-json" || a == "--json":
			asJSON = true
		case a == "-source" || a == "-query" || a == "-offset" || a == "-limit" || a == "-since":
			if i+1 >= len(args) {
				return false, "", nil, nil, fmt.Errorf("%s 需要一個值", a)
			}
			i++
			if a == "-source" {
				source = args[i]
			} else {
				opts[strings.TrimPrefix(a, "-")] = args[i]
			}
		default:
			rest = append(rest, a)
		}
	}
	switch source {
	case "", vocab.SourceManual, vocab.SourceCompose, vocab.SourceAssistant:
	default:
		return false, "", nil, nil, fmt.Errorf("未知的來源 %q", source)
	}
	return
}

func vocabFail(asJSON bool, e *vocab.Error) error {
	if !asJSON {
		return errors.New(e.Msg)
	}
	json.NewEncoder(os.Stdout).Encode(engine.VocabResponse{Error: e})
	return errReported
}

// vocabDo 把指令交給執行中的引擎；引擎沒有執行時在寫入者鎖內直接處理 vocab.json（vocab_offline.go）。
func vocabDo(dial autoupdate.Dialer, base string, req engine.VocabRequest) (engine.VocabResponse, error) {
	for attempt := 0; ; attempt++ {
		conn, err := dial(2 * time.Second)
		if err != nil {
			resp, oerr := vocabOffline(dial, base, req)
			if errors.Is(oerr, errEngineBusy) && attempt < 3 {
				time.Sleep(time.Second) // 引擎正在啟動（已拿鎖、還沒開始服務）：等它開始服務再交給它
				continue
			}
			return resp, oerr
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(vocabRequestTimeout))
		if err := ipc.WriteFrame(conn, ipc.Envelope{Op: ipc.OpVocab, Vocab: &req}); err != nil {
			return engine.VocabResponse{}, err
		}
		var r ipc.Reply
		if err := ipc.ReadFrame(conn, &r); err != nil {
			return engine.VocabResponse{}, err
		}
		if !r.OK {
			return engine.VocabResponse{}, errors.New(r.Error)
		}
		if r.Vocab == nil { // 舊版引擎不認得這個操作
			return engine.VocabResponse{}, errors.New("執行中的引擎不支援自訂詞彙，請更新到最新版")
		}
		return *r.Vocab, nil
	}
}

// vocabWatch 每秒查一次版本，改變時印出一行 {"version":N,"count":M}；給沒有 API 的管理頁即時同步。
// 啟動它的設定程式被強制結束（沒機會收掉這支行程）時就跟著結束（parentWatch），不必等到下一次
// 版本改變、寫入失敗才離開；在 Windows 留著不走會鎖住版本目錄裡的 0c-update.exe，解除安裝刪不掉。
func vocabWatch(opts map[string]string) error {
	since, _ := strconv.ParseUint(opts["since"], 10, 64)
	first := opts["since"] == ""
	parentGone := parentWatch()
	for {
		if parentGone() {
			return nil
		}
		resp, err := vocabDo(dialEngine, basePath(), engine.VocabRequest{Op: engine.VocabStatus})
		if err == nil && (first || resp.Version != since) {
			first, since = false, resp.Version
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"version": resp.Version, "count": resp.Count}); err != nil {
				return err // 讀的一方關掉了
			}
		}
		time.Sleep(time.Second)
	}
}

func printVocab(w io.Writer, op string, r engine.VocabResponse) {
	if r.Error != nil {
		fmt.Fprintln(w, r.Error.Msg)
		return
	}
	switch {
	case r.Words != nil || op == engine.VocabList:
		for _, x := range r.Words {
			if x.English() {
				fmt.Fprintf(w, "%s\t（英文）\n", x.Text)
				continue
			}
			fmt.Fprintf(w, "%s\t%s\n", x.Text, strings.Join(x.Readings, "／"))
		}
		fmt.Fprintf(w, "共 %d 個（上限 %d）\n", r.Total, r.Limit)
	case r.Ranks != nil:
		for _, x := range r.Ranks {
			switch {
			case x.First:
				fmt.Fprintf(w, "%s\t%s\t首選\n", x.Text, x.Reading)
			case x.Page > 0:
				fmt.Fprintf(w, "%s\t%s\t第 %d 頁第 %d 個，前面是「%s」\n", x.Text, x.Reading, x.Page, (x.Index-1)%9+1, x.Ahead)
			default:
				fmt.Fprintf(w, "%s\t%s\t不在候選裡\n", x.Text, x.Reading)
			}
		}
	case r.Annotations != nil:
		for _, a := range r.Annotations {
			line := a.Text + "\t" + a.Reading
			if a.English {
				line = a.Text + "\t（英文詞，不需要讀音）"
			}
			if a.Unknown {
				line = a.Text + "\t（有字查不到讀音，請自己填）"
			}
			for _, c := range a.Chars {
				if c.Ambiguous {
					line += fmt.Sprintf("\t%s 可讀 %s", c.Char, strings.Join(c.Options, "／"))
				}
			}
			if a.Exists {
				line += "\t（詞庫已有，加入後當作偏好）"
			}
			fmt.Fprintln(w, line)
		}
	case r.Learned != nil || op == engine.VocabLearned:
		for _, x := range r.Learned {
			mark := ""
			if x.Vocab {
				mark = "\t（已是自訂詞）"
			}
			fmt.Fprintf(w, "%s\t%s\t%d 次%s\n", x.Text, x.Reading, x.Uses, mark)
		}
	case r.Batches != nil || op == engine.VocabBatches && r.Batch == nil:
		for _, b := range r.Batches {
			state := ""
			if b.Undone {
				state = "（已復原）"
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d 筆：%s%s\n", b.ID, time.Unix(b.Time, 0).Format("01-02 15:04"), b.Source, b.Op, b.Count, strings.Join(b.Preview, "、"), state)
		}
	case r.Phrases != nil || op == engine.VocabPhrases:
		for _, p := range r.Phrases {
			fmt.Fprintf(w, "%d\t%s\n", p.ID, p.Text)
		}
	case r.Batch != nil:
		fmt.Fprintf(w, "批次 %d（%s）：%d 筆變更\n", r.Batch.ID, r.Batch.Op, len(r.Batch.Changes))
	}
	if op == engine.VocabStatus || r.Changed {
		fmt.Fprintf(w, "自訂詞 %d／%d，版本 %d\n", r.Count, r.Limit, r.Version)
	}
	for _, n := range r.Notes {
		fmt.Fprintln(w, "注意：", n)
	}
}
