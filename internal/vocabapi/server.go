package vocabapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"git.biggo.com/0c/ime/core/vocab"
)

// maxBody 是請求內容的上限：500 個詞或 200 則片語都遠小於它。
const maxBody = 1 << 20

// Server 是本機 API。Handler 可以直接交給 http.Server；Doer 是引擎。
type Server struct {
	Doer    Doer
	Dir     string // 個人資料目錄：token 檔在這裡
	Version string // 引擎版本，放進 MCP serverInfo
	// Unavailable 不為 nil 而且回傳非空字串時，資料端點回 503（例如引擎正在交接），帶 Retry-After。
	Unavailable func() string

	lastUsed atomic.Int64
}

// LastUsed 回傳最後一次通過驗證的請求時間（Unix 秒，0 表示還沒用過）。
func (s *Server) LastUsed() int64 { return s.lastUsed.Load() }

// currentToken 每個請求都重讀 token 檔（很小、在本機）：重新產生後，下一個請求就用新的。
// 不用修改時間快取，因為有些檔案系統的時間只到秒，同一秒內重新產生會被當成沒變。
func (s *Server) currentToken() (string, error) {
	t, err := LoadToken(s.Dir)
	return t.Value, err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": vocab.Error{Code: code, Item: -1, Msg: msg}})
}

// localHost 檢查 Host：只接受 127.0.0.1 與 localhost（防 DNS rebinding 的第二道；第一道是只綁 127.0.0.1）。
func localHost(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == "127.0.0.1" || host == "localhost"
}

// localOrigin：沒有 Origin（命令列工具、AI 用戶端）或來自本機網址才接受；網頁的跨來源請求一律拒絕。
func localOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	for _, prefix := range []string{"http://127.0.0.1", "http://localhost", "https://127.0.0.1", "https://localhost"} {
		if rest, ok := strings.CutPrefix(o, prefix); ok && (rest == "" || rest[0] == ':') {
			return true
		}
	}
	return false
}

func (s *Server) authorized(r *http.Request) bool {
	want, err := s.currentToken()
	if err != nil || want == "" {
		return false
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(want)) != 1 {
		return false
	}
	s.lastUsed.Store(time.Now().Unix())
	return true
}

// Handler 回傳所有路由。不設任何 CORS 標頭：網頁讀不到回應。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, OpenAPI("http://"+r.Host))
	})
	mux.HandleFunc("GET /docs", s.docs)
	mux.HandleFunc("POST /mcp", s.data(s.mcp))
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "POST")
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "MCP 只接受 POST（這個服務不推送伺服器事件）")
	})
	for _, op := range Ops {
		mux.HandleFunc(op.Method+" "+op.Path, s.data(func(w http.ResponseWriter, r *http.Request) {
			var in json.RawMessage
			if op.Method == "GET" {
				in = queryInput(r.URL.Query())
			} else {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					fail(w, http.StatusRequestEntityTooLarge, "too_large", "請求內容太大")
					return
				}
				in = b
			}
			out, verr := op.call(s.Doer, in)
			if verr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": verr})
				return
			}
			writeJSON(w, http.StatusOK, out)
		}))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r) || !localOrigin(r) {
			fail(w, http.StatusForbidden, "forbidden", "只接受來自這台電腦的請求")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// data 包住資料端點：驗證 token、檢查內容格式與大小、引擎交接中回 503。
func (s *Server) data(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="0c"`)
			fail(w, http.StatusUnauthorized, "unauthorized", "缺少或錯誤的 token；請到靈犀注音設定「我的詞彙」複製最新的設定")
			return
		}
		if s.Unavailable != nil {
			if why := s.Unavailable(); why != "" {
				w.Header().Set("Retry-After", "2")
				fail(w, http.StatusServiceUnavailable, "unavailable", why)
				return
			}
		}
		if r.Method != "GET" {
			// 要求 JSON：瀏覽器的跨來源「簡單請求」不能帶這個類型，必須先預檢，而我們不回 CORS 標頭。
			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				fail(w, http.StatusUnsupportedMediaType, "content_type", "內容要是 application/json")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		}
		next(w, r)
	}
}

// docs 是給人讀的說明頁：全部內嵌，不從網路載入任何資源。
func (s *Server) docs(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="zh-Hant"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>靈犀注音 個人詞庫 API</title>
<style>body{font:15px/1.6 system-ui,sans-serif;max-width:860px;margin:2em auto;padding:0 16px;color:#222;background:#fff}code,pre{font:13px ui-monospace,monospace;background:#f4f4f4;border-radius:4px}pre{padding:.8em;overflow:auto}h2{margin-top:2em;border-top:1px solid #ddd;padding-top:1em}.m{font-weight:bold;color:#06c}@media(prefers-color-scheme:dark){body{background:#1b1b1b;color:#ddd}code,pre{background:#2a2a2a}h2{border-color:#444}}</style></head><body>`)
	b.WriteString(`<h1>靈犀注音 個人詞庫 API</h1><p>在這台電腦上管理自訂詞與「我的片語」。資料端點要加標頭 <code>Authorization: Bearer &lt;token&gt;</code>，token 在靈犀注音設定「我的詞彙」頁取得，不要分享給別人。機器可讀的文件：<a href="/openapi.json">/openapi.json</a>；MCP 端點：<code>/mcp</code>（Streamable HTTP，工具名稱同下）。</p>`)
	for _, op := range Ops {
		fmt.Fprintf(&b, `<h2 id="%s">%s <small><code>%s</code></small></h2><p><span class="m">%s</span> <code>%s</code></p><p>%s</p>`,
			op.Name, html.EscapeString(op.Summary), op.Name, op.Method, op.Path, html.EscapeString(op.Description))
		if op.Input != nil {
			in, _ := json.MarshalIndent(inputSchema(op), "", "  ")
			fmt.Fprintf(&b, "<p>輸入：</p><pre>%s</pre>", html.EscapeString(string(in)))
		}
		out, _ := json.MarshalIndent(outputSchema(op), "", "  ")
		fmt.Fprintf(&b, "<p>輸出：</p><pre>%s</pre>", html.EscapeString(string(out)))
	}
	b.WriteString(`</body></html>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	io.WriteString(w, b.String())
}

// ErrDisabled 表示 API 沒有開啟。
var ErrDisabled = errors.New("本機 API 沒有開啟")
