// 0c-mcp 把只支援 stdio 的 MCP 用戶端（例如桌面版 AI 助手的設定檔）轉接到靈犀注音的本機 API（#126）：
// 從標準輸入逐行讀 JSON-RPC 訊息，轉送到 http://127.0.0.1:<埠>/mcp，把回應寫到標準輸出。
//
// token 每次請求都從使用者目錄的 token 檔讀，所以設定裡不用放 token，重新產生後也不必重設；
// 設定程式會填入不隨版本變動的執行檔路徑。
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"git.biggo.com/0c/ime/internal/vocabapi"
)

func main() {
	dir := flag.String("dir", defaultDir(), "個人資料目錄（user.json、api.json、api-token.json 所在）")
	flag.Parse()
	client := &http.Client{Timeout: 60 * time.Second}
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	for {
		line, err := in.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			if resp := forward(client, *dir, line); resp != nil {
				out.Write(resp)
				out.WriteByte('\n')
				out.Flush()
			}
		}
		if err != nil {
			return // 用戶端關閉標準輸入：結束
		}
	}
}

// forward 轉送一則訊息；通知（沒有 id）不回應。轉送失敗時回 JSON-RPC 錯誤，讓用戶端看得到原因。
func forward(client *http.Client, dir string, msg []byte) []byte {
	var head struct {
		ID json.RawMessage `json:"id"`
	}
	json.Unmarshal(msg, &head)
	fail := func(text string) []byte {
		if len(head.ID) == 0 {
			fmt.Fprintln(os.Stderr, "0c-mcp:", text)
			return nil
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": head.ID, "error": map[string]any{"code": -32000, "message": text}})
		return b
	}
	cfg, err := vocabapi.LoadConfig(dir)
	if err != nil || !cfg.Enabled {
		return fail("靈犀注音的「讓 AI 幫你加詞」沒有開啟；請到設定程式「我的詞彙」頁開啟")
	}
	tok, err := vocabapi.LoadToken(dir)
	if err != nil {
		return fail("讀不到 token：" + err.Error())
	}
	req, _ := http.NewRequest("POST", "http://"+cfg.Addr()+"/mcp", bytes.NewReader(msg))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tok.Value)
	resp, err := client.Do(req)
	if err != nil {
		return fail("連不到靈犀注音（輸入法引擎可能沒有在執行；切換到靈犀注音打一個字後再試）：" + err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusAccepted:
		return nil
	case resp.StatusCode != http.StatusOK:
		return fail(fmt.Sprintf("靈犀注音回應 HTTP %d：%s", resp.StatusCode, bytes.TrimSpace(body)))
	}
	return bytes.TrimSpace(body)
}
