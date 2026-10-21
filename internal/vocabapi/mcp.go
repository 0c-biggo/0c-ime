package vocabapi

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
)

// MCP（Model Context Protocol）的 Streamable HTTP 傳輸：用戶端以 POST 送 JSON-RPC 訊息，
// 這裡一律以單一 JSON 回應（不開 SSE 串流，也不推送伺服器事件），不需要 session。
// 工具與 REST 操作同一份定義（Ops）。

// mcpVersions 是支援的協定版本，新的在前；用戶端要求的版本在清單裡就照它，否則回最新的。
var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const mcpInstructions = "管理使用者在靈犀注音（注音輸入法）親手加入的自訂詞與「我的片語」。" +
	"加詞前先用 suggest_readings 查注音；破音字（chars 裡 ambiguous 為 true）或查不到的字（unknown），依語境挑不出來就問使用者，確認後再用 add_words 一次加入，最後告訴使用者加了哪些。" +
	"每次修改是一個批次，可用 undo_batch 撤回。看不到自動學到的詞與輸入紀錄。"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "too_large", "請求內容太大")
		return
	}
	var batch []rpcRequest
	single := len(body) > 0 && body[0] != '['
	if single {
		var one rpcRequest
		if err := json.Unmarshal(body, &one); err != nil {
			writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "JSON 格式錯誤"}})
			return
		}
		batch = []rpcRequest{one}
	} else if err := json.Unmarshal(body, &batch); err != nil {
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "JSON 格式錯誤"}})
		return
	}
	var out []rpcResponse
	for _, req := range batch {
		if len(req.ID) == 0 { // 通知（例如 notifications/initialized）：不回應
			continue
		}
		out = append(out, s.rpc(req))
	}
	switch {
	case len(out) == 0:
		w.WriteHeader(http.StatusAccepted)
	case single:
		writeJSON(w, http.StatusOK, out[0])
	default:
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) rpc(req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		version := mcpVersions[0]
		if slices.Contains(mcpVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "0c-vocab", "title": "靈犀注音個人詞庫", "version": s.Version},
			"instructions":    mcpInstructions,
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		tools := make([]any, 0, len(Ops))
		for _, op := range Ops {
			tools = append(tools, map[string]any{
				"name":         op.Name,
				"title":        op.Summary,
				"description":  op.Description,
				"inputSchema":  inputSchema(op),
				"outputSchema": outputSchema(op),
				"annotations": map[string]any{
					"title":           op.Summary,
					"readOnlyHint":    !op.Write,
					"destructiveHint": op.Write && op.Name != "add_words" && op.Name != "add_snippets",
					"idempotentHint":  !op.Write,
					"openWorldHint":   false,
				},
			})
		}
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{-32602, "參數格式錯誤"}
			break
		}
		i := slices.IndexFunc(Ops, func(op Op) bool { return op.Name == p.Name })
		if i < 0 {
			resp.Error = &rpcError{-32602, "沒有這個工具：" + p.Name}
			break
		}
		out, verr := Ops[i].call(s.doer(), p.Arguments)
		if verr != nil { // 工具執行失敗以結果回報，讓模型讀得到原因並轉告使用者
			b, _ := json.Marshal(map[string]any{"error": verr})
			resp.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "isError": true}
			break
		}
		b, _ := json.Marshal(out)
		var structured any
		json.Unmarshal(b, &structured)
		resp.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "structuredContent": structured}
	default:
		resp.Error = &rpcError{-32601, "不支援的方法：" + req.Method}
	}
	return resp
}
