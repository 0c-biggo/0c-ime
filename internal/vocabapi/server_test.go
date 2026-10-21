package vocabapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func testServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, w := range []struct{ w, r string }{{"曾", "ㄘㄥˊ"}, {"曾", "ㄗㄥ"}, {"曾經", "ㄘㄥˊ ㄐㄧㄥ"}, {"曾孫", "ㄗㄥ ㄙㄨㄣ"}, {"小", "ㄒㄧㄠˇ"}, {"明", "ㄇㄧㄥˊ"}} {
		lex.Add(lexicon.Entry{Surface: w.w, Reading: zhuyin.ParseReading(w.r), PRW: 1})
		sys.SetUni(w.w, 1e-4)
	}
	dir := t.TempDir()
	tok, err := EnsureToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Doer: engine.New(lex, sys), Dir: dir, Version: "test"}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, tok.Value
}

func do(t *testing.T, ts *httptest.Server, method, path, token, body string, header ...string) (int, map[string]any, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(header); i += 2 {
		if header[i] == "Host" {
			req.Host = header[i+1]
		} else {
			req.Header.Set(header[i], header[i+1])
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	return resp.StatusCode, m, resp.Header
}

func TestDocsWithoutTokenDataWithToken(t *testing.T) {
	s, ts, tok := testServer(t)
	code, doc, h := do(t, ts, "GET", "/openapi.json", "", "")
	if code != 200 || doc["openapi"] != "3.1.0" || h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("%d %v", code, h)
	}
	paths := doc["paths"].(map[string]any)
	if paths["/v1/words"].(map[string]any)["post"].(map[string]any)["operationId"] != "add_words" {
		t.Fatalf("%v", paths["/v1/words"])
	}
	if code, _, _ := do(t, ts, "GET", "/docs", "", ""); code != 200 {
		t.Fatal(code)
	}
	if code, _, h := do(t, ts, "GET", "/v1/words", "", ""); code != 401 || h.Get("WWW-Authenticate") == "" {
		t.Fatalf("沒帶 token：%d", code)
	}
	if code, _, _ := do(t, ts, "GET", "/v1/words", "0c_wrong", ""); code != 401 {
		t.Fatalf("錯的 token：%d", code)
	}
	if code, out, _ := do(t, ts, "GET", "/v1/words", tok, ""); code != 200 || out["total"].(float64) != 0 || s.LastUsed() == 0 {
		t.Fatalf("%d %v", code, out)
	}
}

func TestHostOriginAndContentTypeGuards(t *testing.T) {
	_, ts, tok := testServer(t)
	if code, _, _ := do(t, ts, "GET", "/v1/words", tok, "", "Host", "evil.example:47100"); code != 403 {
		t.Fatalf("Host：%d", code)
	}
	if code, _, _ := do(t, ts, "GET", "/openapi.json", "", "", "Host", "evil.example"); code != 403 {
		t.Fatalf("文件也檢查 Host：%d", code)
	}
	if code, _, _ := do(t, ts, "GET", "/v1/words", tok, "", "Origin", "https://evil.example"); code != 403 {
		t.Fatalf("Origin：%d", code)
	}
	if code, _, _ := do(t, ts, "GET", "/v1/words", tok, "", "Origin", "http://localhost:47100"); code != 200 {
		t.Fatalf("本機 Origin：%d", code)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/v1/words", strings.NewReader(`{"words":[]}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "text/plain")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 415 {
		t.Fatalf("瀏覽器簡單請求的內容類型：%d", resp.StatusCode)
	}
}

func TestRESTAddSearchSuggestUndo(t *testing.T) {
	_, ts, tok := testServer(t)
	code, out, _ := do(t, ts, "POST", "/v1/readings", tok, `{"words":["曾小明"]}`)
	ann := out["annotations"].([]any)[0].(map[string]any)
	if code != 200 || ann["reading"] != "ㄘㄥˊ ㄒㄧㄠˇ ㄇㄧㄥˊ" || ann["chars"].([]any)[0].(map[string]any)["ambiguous"] != true {
		t.Fatalf("%d %v", code, out)
	}
	code, out, _ = do(t, ts, "POST", "/v1/words", tok, `{"words":[{"word":"曾小明","readings":["ㄗㄥ ㄒㄧㄠˇ ㄇㄧㄥˊ"]}]}`)
	if code != 200 || out["count"].(float64) != 1 || out["batch"].(map[string]any)["source"] != "assistant" {
		t.Fatalf("%d %v", code, out)
	}
	batch := out["batch"].(map[string]any)["id"].(float64)
	code, out, _ = do(t, ts, "POST", "/v1/words", tok, `{"words":[{"word":"a c","readings":["ㄅ"]}]}`)
	if code != 400 || out["error"].(map[string]any)["code"] != "invalid_word" {
		t.Fatalf("不合格整批拒絕：%d %v", code, out)
	}
	if code, out, _ = do(t, ts, "GET", "/v1/words?q=ㄗㄥ", tok, ""); code != 200 || out["total"].(float64) != 1 {
		t.Fatalf("%d %v", code, out)
	}
	if code, out, _ = do(t, ts, "POST", "/v1/batches/undo", tok, `{"batch":`+jsonNum(batch)+`}`); code != 200 || out["count"].(float64) != 0 {
		t.Fatalf("撤回：%d %v", code, out)
	}
	if code, out, _ = do(t, ts, "POST", "/v1/snippets", tok, `{"snippets":["——","台北市"]}`); code != 200 || len(out["snippets"].([]any)) != 2 {
		t.Fatalf("%d %v", code, out)
	}
	if code, out, _ = do(t, ts, "GET", "/v1/batches", tok, ""); code != 200 || len(out["batches"].([]any)) != 3 {
		t.Fatalf("%d %v", code, out)
	}
}

func jsonNum(f float64) string { b, _ := json.Marshal(f); return string(b) }

// #146：英文詞經 API 新增（不給讀音）、列出（readings 是空陣列）、刪除；標注音標成 english。
func TestRESTEnglishWords(t *testing.T) {
	_, ts, tok := testServer(t)
	code, out, _ := do(t, ts, "POST", "/v1/readings", tok, `{"words":["kubectl"]}`)
	if ann := out["annotations"].([]any)[0].(map[string]any); code != 200 || ann["english"] != true || ann["unknown"] != nil {
		t.Fatalf("%d %v", code, out)
	}
	code, out, _ = do(t, ts, "POST", "/v1/words", tok, `{"words":[{"word":"ls"},{"word":"kubectl","readings":[]}]}`)
	if code != 200 || out["count"].(float64) != 2 {
		t.Fatalf("%d %v", code, out)
	}
	code, out, _ = do(t, ts, "GET", "/v1/words?q=ls", tok, "")
	words := out["words"].([]any)
	if code != 200 || len(words) != 1 || words[0].(map[string]any)["word"] != "ls" || len(words[0].(map[string]any)["readings"].([]any)) != 0 {
		t.Fatalf("%d %v", code, out)
	}
	if code, out, _ = do(t, ts, "POST", "/v1/words", tok, `{"words":[{"word":"ls","readings":["ㄋㄠ"]}]}`); code != 400 || out["error"].(map[string]any)["code"] != "invalid_reading" {
		t.Fatalf("英文詞不收讀音：%d %v", code, out)
	}
	if code, out, _ = do(t, ts, "POST", "/v1/words/remove", tok, `{"words":["ls"]}`); code != 200 || out["count"].(float64) != 1 {
		t.Fatalf("%d %v", code, out)
	}
	_, doc, _ := do(t, ts, "GET", "/openapi.json", "", "")
	add := doc["paths"].(map[string]any)["/v1/words"].(map[string]any)["post"].(map[string]any)
	item := add["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)["words"].(map[string]any)["items"].(map[string]any)
	if req, _ := json.Marshal(item["required"]); string(req) != `["word"]` || !strings.Contains(add["description"].(string), "英文詞") {
		t.Fatalf("OpenAPI：英文詞不必給 readings：%s %v", req, add["description"])
	}
}

func TestTokenRegenInvalidatesOldToken(t *testing.T) {
	s, ts, old := testServer(t)
	nt, err := RegenToken(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := do(t, ts, "GET", "/v1/status", old, ""); code != 401 {
		t.Fatalf("舊 token 立即失效：%d", code)
	}
	if code, _, _ := do(t, ts, "GET", "/v1/status", nt.Value, ""); code != 200 {
		t.Fatalf("新 token：%d", code)
	}
}

func TestUnavailableDuringHandoff(t *testing.T) {
	s, ts, tok := testServer(t)
	s.Unavailable = func() string { return "輸入法引擎正在更新，稍後重試" }
	if code, _, h := do(t, ts, "GET", "/v1/status", tok, ""); code != 503 || h.Get("Retry-After") == "" {
		t.Fatalf("%d", code)
	}
}

func TestMCP(t *testing.T) {
	_, ts, tok := testServer(t)
	rpc := func(body string) (int, map[string]any) {
		code, out, _ := do(t, ts, "POST", "/mcp", tok, body)
		return code, out
	}
	code, out := rpc(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	res := out["result"].(map[string]any)
	if code != 200 || res["protocolVersion"] != "2025-03-26" || res["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("%d %v", code, out)
	}
	if code, _ := rpc(`{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		t.Fatalf("通知：%d", code)
	}
	_, out = rpc(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := out["result"].(map[string]any)["tools"].([]any)
	if len(tools) != len(Ops) || tools[0].(map[string]any)["inputSchema"].(map[string]any)["type"] != "object" {
		t.Fatalf("%v", tools)
	}
	_, out = rpc(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add_words","arguments":{"words":[{"word":"曾小明","readings":["ㄗㄥ ㄒㄧㄠˇ ㄇㄧㄥˊ"]}]}}}`)
	res = out["result"].(map[string]any)
	if res["isError"] == true || res["structuredContent"].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("%v", out)
	}
	_, out = rpc(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"remove_words","arguments":{"words":["不存在"]}}}`)
	res = out["result"].(map[string]any)
	if res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "not_found") {
		t.Fatalf("工具錯誤以結果回報：%v", out)
	}
	_, out = rpc(`{"jsonrpc":"2.0","id":5,"method":"nope"}`)
	if out["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("%v", out)
	}
	if code, _, _ := do(t, ts, "GET", "/mcp", tok, ""); code != 405 {
		t.Fatalf("GET /mcp：%d", code)
	}
	if code, _, _ := do(t, ts, "POST", "/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != 401 {
		t.Fatalf("MCP 也要 token：%d", code)
	}
}

func TestConfigAndTokenFiles(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadConfig(dir)
	if err != nil || c.Enabled || c.Addr() != "127.0.0.1:47100" {
		t.Fatalf("%+v %v", c, err)
	}
	if err := SaveConfig(dir, Config{Enabled: true, Port: 47123}); err != nil {
		t.Fatal(err)
	}
	if c, _ = LoadConfig(dir); !c.Enabled || c.Addr() != "127.0.0.1:47123" {
		t.Fatalf("%+v", c)
	}
	a, _ := EnsureToken(dir)
	b, _ := EnsureToken(dir)
	if a.Value != b.Value || !strings.HasPrefix(a.Value, "0c_") || len(a.Value) != 51 {
		t.Fatalf("%q %q", a.Value, b.Value)
	}
}

// #196：會改資料的操作（REST 與 MCP）成功後一定呼叫 Save，API 回成功時檔案已寫入；
// 唯讀的操作與整批拒絕的修改不呼叫。存檔失敗時回 500 not_saved，不讓呼叫端以為已經存好。
func TestWritesCallSave(t *testing.T) {
	s, ts, tok := testServer(t)
	var saves int
	var saveErr error
	s.Save = func() error { saves++; return saveErr }
	expect := func(what string, want int) {
		t.Helper()
		if saves != want {
			t.Fatalf("%s：存檔 %d 次，應該 %d 次", what, saves, want)
		}
	}
	code, out, _ := do(t, ts, "POST", "/v1/words", tok, `{"words":[{"word":"曾小明","readings":["ㄗㄥ ㄒㄧㄠˇ ㄇㄧㄥˊ"]},{"word":"ls"}]}`)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	expect("add_words", 1)
	if code, out, _ = do(t, ts, "POST", "/v1/words", tok, `{"words":[{"word":"a c","readings":["ㄅ"]}]}`); code != 400 {
		t.Fatalf("%d %v", code, out)
	}
	expect("整批拒絕不存", 1)
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/v1/words", ""}, {"GET", "/v1/status", ""}, {"GET", "/v1/batches", ""}, {"GET", "/v1/snippets", ""},
		{"POST", "/v1/readings", `{"words":["曾小明"]}`},
	} {
		if code, out, _ := do(t, ts, r.method, r.path, tok, r.body); code != 200 {
			t.Fatalf("%s %s：%d %v", r.method, r.path, code, out)
		}
	}
	expect("唯讀的操作不存", 1)
	if code, out, _ = do(t, ts, "POST", "/v1/words/remove", tok, `{"words":["ls"]}`); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	expect("remove_words", 2)
	batch := out["batch"].(map[string]any)["id"].(float64) // 撤回這次刪除
	if code, out, _ = do(t, ts, "POST", "/v1/snippets", tok, `{"snippets":["台北市"]}`); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	expect("add_snippets", 3)
	id := out["snippets"].([]any)[0].(map[string]any)["id"].(float64)
	if code, out, _ = do(t, ts, "POST", "/v1/snippets/remove", tok, `{"ids":[`+jsonNum(id)+`]}`); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	expect("remove_snippets", 4)
	if code, out, _ = do(t, ts, "POST", "/v1/batches/undo", tok, `{"batch":`+jsonNum(batch)+`}`); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	expect("undo_batch", 5)
	code, out, _ = do(t, ts, "POST", "/mcp", tok, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add_words","arguments":{"words":[{"word":"kubectl"}]}}}`)
	if code != 200 || out["result"].(map[string]any)["isError"] == true {
		t.Fatalf("%d %v", code, out)
	}
	expect("MCP add_words", 6)

	saveErr = errors.New("磁碟已滿")
	code, out, _ = do(t, ts, "POST", "/v1/words", tok, `{"words":[{"word":"git"}]}`)
	if code != 500 || out["error"].(map[string]any)["code"] != CodeNotSaved {
		t.Fatalf("存檔失敗要回錯誤：%d %v", code, out)
	}
	code, out, _ = do(t, ts, "POST", "/mcp", tok, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add_words","arguments":{"words":[{"word":"npm"}]}}}`)
	if res := out["result"].(map[string]any); code != 200 || res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), CodeNotSaved) {
		t.Fatalf("MCP 存檔失敗要回錯誤：%d %v", code, out)
	}
}
