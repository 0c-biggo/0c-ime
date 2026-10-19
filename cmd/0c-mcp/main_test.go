package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/engine"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/internal/vocabapi"
)

func TestForwardToLocalAPI(t *testing.T) {
	dir := t.TempDir()
	client := &http.Client{}
	if got := string(forward(client, dir, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))); !strings.Contains(got, "沒有開啟") {
		t.Fatalf("沒開啟時回錯誤：%s", got)
	}
	if _, err := vocabapi.EnsureToken(dir); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer((&vocabapi.Server{Doer: engine.New(lexicon.NewMem(), lm.NewMem()), Dir: dir}).Handler())
	defer ts.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	n, _ := strconv.Atoi(port)
	if err := vocabapi.SaveConfig(dir, vocabapi.Config{Enabled: true, Port: n}); err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	json.Unmarshal(forward(client, dir, []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"add_words","arguments":{"words":[{"word":"陳怡君","readings":["ㄔㄣˊ ㄧˊ ㄐㄩㄣ"]}]}}}`)), &resp)
	if resp["id"].(float64) != 7 || resp["result"].(map[string]any)["structuredContent"].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("%v", resp)
	}
	if out := forward(client, dir, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); out != nil {
		t.Fatalf("通知不回應：%s", out)
	}
	// 重新產生 token 後不必重設：每次都重讀 token 檔。
	if _, err := vocabapi.RegenToken(dir); err != nil {
		t.Fatal(err)
	}
	if got := string(forward(client, dir, []byte(`{"jsonrpc":"2.0","id":8,"method":"ping"}`))); !strings.Contains(got, `"result"`) {
		t.Fatalf("%s", got)
	}
}
