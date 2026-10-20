// Package vocabapi 是自訂詞彙的本機 HTTP API（#126）：同一份操作定義產生 REST 路由、/openapi.json、
// /docs 與 MCP 工具（/mcp，Streamable HTTP）。實際的修改交給引擎（engine.Vocab，唯一的寫入者）。
//
// 只綁 127.0.0.1、資料端點要 Bearer token、檢查 Host 與 Origin、不回 CORS 標頭；
// 只能動自訂詞與片語，碰不到設定、更新與自動學到的資料。
package vocabapi

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/vocab"
)

// Doer 執行詞彙指令；0c-server 傳入引擎本身。
type Doer interface {
	Vocab(engine.VocabRequest) engine.VocabResponse
}

// Op 是一個操作：REST 與 MCP 工具同名、同一份輸入輸出。
type Op struct {
	Name        string // MCP 工具名稱與 OpenAPI operationId
	Summary     string
	Description string
	Method      string // GET 的輸入來自網址參數，其餘來自 JSON 內容
	Path        string
	Write       bool // 會修改資料；MCP 標註 destructive／idempotent
	Input       any  // 輸入 struct 的零值（產生 schema）；nil 表示沒有輸入
	Output      any
	call        func(d Doer, in json.RawMessage) (any, *vocab.Error)
}

// WordInput 是要加入的一個詞。
type WordInput struct {
	Word     string   `json:"word" desc:"詞：1～32 個漢字；或英文詞（1～32 個英文字母、數字與 - _ .，至少一個字母，大小寫照字面），例如終端機指令 ls、kubectl，中英混打時照英文送出、不變成注音"`
	Readings []string `json:"readings,omitempty" desc:"漢字詞的讀音，每個讀音的注音音節以空白分隔，例如「ㄔㄣˊ ㄧˊ ㄐㄩㄣ」；破音字有幾種讀法就給幾個，最多 4 個。英文詞不給讀音"`
}

type searchInput struct {
	Query  string `json:"q,omitempty" desc:"篩選：詞面包含這段文字，或讀音以這段注音開頭；空白表示全部"`
	Offset int    `json:"offset,omitempty" desc:"略過前幾筆（分頁）"`
	Limit  int    `json:"limit,omitempty" desc:"最多回傳幾筆，預設與上限 1000"`
}

type wordsOutput struct {
	Words []vocab.Word `json:"words" desc:"自訂詞；readings 是空陣列的是英文詞（中英混打時照英文送出）"`
	Total int          `json:"total" desc:"符合篩選的總數"`
	Count int          `json:"count" desc:"自訂詞總數"`
	Limit int          `json:"limit" desc:"自訂詞上限"`
}

type suggestInput struct {
	Words []string `json:"words" desc:"要標注音的詞，最多 500 個"`
}

type suggestOutput struct {
	Annotations []engine.Annotation `json:"annotations" desc:"每個詞的建議讀音；chars 裡 ambiguous 為 true 的字是破音字，請依語境挑選或問使用者；unknown 為 true 表示有字查不到讀音；english 為 true 表示是英文詞，加入時不給讀音"`
}

type addInput struct {
	Words []WordInput `json:"words" desc:"要加入的詞，一次最多 500 個；已經有的詞會合併讀音"`
}

type changeOutput struct {
	Batch   *vocab.Batch `json:"batch,omitempty" desc:"這次修改的批次，可用 undo_batch 撤回；沒有任何變更時省略"`
	Count   int          `json:"count" desc:"修改後的自訂詞總數"`
	Limit   int          `json:"limit" desc:"自訂詞上限"`
	Version uint64       `json:"version" desc:"詞彙版本，每次修改加一"`
}

type removeInput struct {
	Words []string `json:"words" desc:"要刪除的詞，一次最多 500 個"`
}

type snippetsOutput struct {
	Snippets []vocab.Phrase `json:"snippets" desc:"「我的片語」，依使用者排的順序；在注音模式按 = 開選單選取"`
	Batch    *vocab.Batch   `json:"batch,omitempty"`
}

type addSnippetsInput struct {
	Snippets []string `json:"snippets" desc:"要加入的片語（常用字串、地址、簽名等），每則最多 1000 字，總數最多 200 則"`
}

type removeSnippetsInput struct {
	IDs []uint64 `json:"ids" desc:"要刪除的片語編號"`
}

type batchesOutput struct {
	Batches []engine.BatchSummary `json:"batches" desc:"最近的修改，新的在前；最多保留 50 批"`
}

type undoInput struct {
	Batch uint64 `json:"batch" desc:"要撤回的批次編號"`
}

type statusOutput struct {
	Count   int    `json:"count" desc:"自訂詞總數"`
	Limit   int    `json:"limit" desc:"自訂詞上限"`
	Version uint64 `json:"version"`
}

func decode[T any](in json.RawMessage) (T, *vocab.Error) {
	var v T
	if len(in) > 0 && string(in) != "null" {
		if err := json.Unmarshal(in, &v); err != nil {
			return v, &vocab.Error{Code: "bad_request", Item: -1, Msg: "輸入格式不對：" + err.Error()}
		}
	}
	return v, nil
}

func run(d Doer, req engine.VocabRequest) (engine.VocabResponse, *vocab.Error) {
	resp := d.Vocab(req)
	if !resp.OK {
		return resp, resp.Error
	}
	return resp, nil
}

func changed(d Doer, req engine.VocabRequest) (any, *vocab.Error) {
	req.Source = vocab.SourceAssistant // 經 API 的修改一律標成 AI 助手，管理頁可以篩選、整批復原
	resp, err := run(d, req)
	if err != nil {
		return nil, err
	}
	return changeOutput{Batch: resp.Batch, Count: resp.Count, Limit: resp.Limit, Version: resp.Version}, nil
}

// Ops 是全部的操作，順序就是文件的順序。
var Ops = []Op{
	{Name: "search_words", Summary: "查自訂詞", Method: "GET", Path: "/v1/words",
		Description: "列出使用者親手加入的自訂詞（不含自動學到的詞與輸入紀錄），可依詞面或讀音篩選。",
		Input:       searchInput{}, Output: wordsOutput{},
		call: func(d Doer, in json.RawMessage) (any, *vocab.Error) {
			q, err := decode[searchInput](in)
			if err != nil {
				return nil, err
			}
			resp, e := run(d, engine.VocabRequest{Op: engine.VocabList, Query: q.Query, Offset: q.Offset, Limit: q.Limit})
			if e != nil {
				return nil, e
			}
			return wordsOutput{Words: nonNil(resp.Words), Total: resp.Total, Count: resp.Count, Limit: resp.Limit}, nil
		}},
	{Name: "suggest_readings", Summary: "建議讀音", Method: "POST", Path: "/v1/readings",
		Description: "依內建詞庫幫一批詞標注音。破音字（例如姓氏「曾」讀ㄗㄥ）會標出來，請依語境挑選，挑不出來就問使用者；不會修改任何資料。",
		Input:       suggestInput{}, Output: suggestOutput{},
		call: func(d Doer, in json.RawMessage) (any, *vocab.Error) {
			q, err := decode[suggestInput](in)
			if err != nil {
				return nil, err
			}
			if len(q.Words) > vocab.MaxBatchItems {
				return nil, &vocab.Error{Code: vocab.CodeLimitBatch, Item: -1, Limit: vocab.MaxBatchItems, Msg: "一次最多 500 個詞"}
			}
			resp, e := run(d, engine.VocabRequest{Op: engine.VocabAnnotate, Texts: q.Words})
			if e != nil {
				return nil, e
			}
			return suggestOutput{Annotations: resp.Annotations}, nil
		}},
	{Name: "add_words", Summary: "加入自訂詞", Method: "POST", Path: "/v1/words", Write: true,
		Description: "加入一批自訂詞，加入後立刻打得出來。整批有任何一筆不合格就全部不加，錯誤會說明是哪一筆、為什麼。已經在系統詞庫的詞會當成使用者偏好。" +
			"也可以加英文詞（終端機指令、工具名，例如 ls、rm、kubectl）：不給讀音，中英混打時打出完全相同的字母（大小寫照字面）就照英文送出，不變成注音。",
		Input: addInput{}, Output: changeOutput{},
		call: func(d Doer, in json.RawMessage) (any, *vocab.Error) {
			q, err := decode[addInput](in)
			if err != nil {
				return nil, err
			}
			entries := make([]vocab.Entry, len(q.Words))
			for i, w := range q.Words {
				entries[i] = vocab.Entry{Text: w.Word, Readings: w.Readings}
			}
			return changed(d, engine.VocabRequest{Op: engine.VocabAdd, Words: entries})
		}},
	{Name: "remove_words", Summary: "刪除自訂詞", Method: "POST", Path: "/v1/words/remove", Write: true,
		Description: "刪除一批自訂詞。不存在的詞會讓整批拒絕。",
		Input:       removeInput{}, Output: changeOutput{},
		call: func(d Doer, in json.RawMessage) (any, *vocab.Error) {
			q, err := decode[removeInput](in)
			if err != nil {
				return nil, err
			}
			return changed(d, engine.VocabRequest{Op: engine.VocabRemove, Texts: q.Words})
		}},
	{Name: "list_snippets", Summary: "列出我的片語", Method: "GET", Path: "/v1/snippets",
		Description: "列出「我的片語」：常用字串、地址、簽名等，使用者在注音模式按 = 從選單選取。",
		Output:      snippetsOutput{},
		call: func(d Doer, in json.RawMessage) (any, *vocab.Error) {
			resp, e := run(d, engine.VocabRequest{Op: engine.VocabPhrases})
			if e != nil {
				return nil, e
			}
			return snippetsOutput{Snippets: nonNil(resp.Phrases)}, nil
		}},
	{Name: "add_snippets", Summary: "加入片語", Method: "POST", Path: "/v1/snippets", Write: true,
		Description: "在「我的片語」最後面加入幾則。片語沒有讀音、不參與選字。",
		Input:       addSnippetsInput{}, Output: snippetsOutput{},
		call: func(d Doer, in json.RawMessage) (any, *vocab.Error) {
			q, err := decode[addSnippetsInput](in)
			if err != nil {
				return nil, err
			}
			resp, e := run(d, engine.VocabRequest{Op: engine.VocabPhraseAdd, Texts: q.Snippets, Source: vocab.SourceAssistant})
			if e != nil {
				return nil, e
			}
			return snippetsOutput{Snippets: nonNil(resp.Phrases), Batch: resp.Batch}, nil
		}},
	{Name: "remove_snippets", Summary: "刪除片語", Method: "POST", Path: "/v1/snippets/remove", Write: true,
		Description: "依編號刪除幾則片語。",
		Input:       removeSnippetsInput{}, Output: snippetsOutput{},
		call: func(d Doer, in json.RawMessage) (any, *vocab.Error) {
			q, err := decode[removeSnippetsInput](in)
			if err != nil {
				return nil, err
			}
			resp, e := run(d, engine.VocabRequest{Op: engine.VocabPhraseRemove, IDs: q.IDs, Source: vocab.SourceAssistant})
			if e != nil {
				return nil, e
			}
			return snippetsOutput{Snippets: nonNil(resp.Phrases), Batch: resp.Batch}, nil
		}},
	{Name: "list_batches", Summary: "最近的修改", Method: "GET", Path: "/v1/batches",
		Description: "列出最近的修改（新的在前），包含使用者自己、組字中加入與 AI 助手做的修改。",
		Output:      batchesOutput{},
		call: func(d Doer, in json.RawMessage) (any, *vocab.Error) {
			resp, e := run(d, engine.VocabRequest{Op: engine.VocabBatches})
			if e != nil {
				return nil, e
			}
			return batchesOutput{Batches: nonNil(resp.Batches)}, nil
		}},
	{Name: "undo_batch", Summary: "撤回一批修改", Method: "POST", Path: "/v1/batches/undo", Write: true,
		Description: "整批撤回一次修改。那些詞後來又被改過時會拒絕，不會蓋掉之後的修改。",
		Input:       undoInput{}, Output: changeOutput{},
		call: func(d Doer, in json.RawMessage) (any, *vocab.Error) {
			q, err := decode[undoInput](in)
			if err != nil {
				return nil, err
			}
			return changed(d, engine.VocabRequest{Op: engine.VocabUndo, Batch: q.Batch})
		}},
	{Name: "status", Summary: "詞彙狀態", Method: "GET", Path: "/v1/status",
		Description: "自訂詞總數、上限與版本。",
		Output:      statusOutput{},
		call: func(d Doer, in json.RawMessage) (any, *vocab.Error) {
			resp, e := run(d, engine.VocabRequest{Op: engine.VocabStatus})
			if e != nil {
				return nil, e
			}
			return statusOutput{Count: resp.Count, Limit: resp.Limit, Version: resp.Version}, nil
		}},
}

func nonNil[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}

// queryInput 把 GET 的網址參數轉成輸入 JSON（只支援字串與整數欄位）。
func queryInput(q url.Values) json.RawMessage {
	m := map[string]any{}
	for k, vs := range q {
		if len(vs) == 0 {
			continue
		}
		if n, err := strconv.Atoi(vs[0]); err == nil && k != "q" {
			m[k] = n
		} else {
			m[k] = strings.TrimSpace(vs[0])
		}
	}
	b, _ := json.Marshal(m)
	return b
}
