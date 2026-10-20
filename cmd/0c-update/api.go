package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/vocabapi"
)

// api 指令（#126）：設定程式「讓 AI 幫你加詞」的開關、token 與測試連線都經由這裡，WebView 不自己連線。

const apiUsage = "用法：0c-update api status|enable [-port 埠號]|disable|token -regen|test [-json]"

type apiView struct {
	Schema       int        `json:"schema"`
	OK           bool       `json:"ok"`
	Enabled      bool       `json:"enabled"`
	Addr         string     `json:"addr"`
	State        string     `json:"state"` // 引擎回報：off、listening、port_in_use…；引擎沒執行時 engine_not_running
	Token        string     `json:"token,omitempty"`
	TokenCreated int64      `json:"token_created,omitempty"`
	LastUsed     int64      `json:"last_used,omitempty"`
	MCPURL       string     `json:"mcp_url"`
	OpenAPIURL   string     `json:"openapi_url"`
	Apply        *applyView `json:"apply,omitempty"`
	Test         *apiTest   `json:"test,omitempty"`
	Error        string     `json:"error,omitempty"`
}

type apiTest struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"` // off、port_in_use、unauthorized、unreachable、engine_not_running
	Detail string `json:"detail,omitempty"`
	Count  int    `json:"count,omitempty"` // 測試成功時的自訂詞數
}

func apiCommand(args []string) error {
	asJSON, regen, port := false, false, 0
	var rest []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-json", "--json":
			asJSON = true
		case "-regen", "--regen":
			regen = true
		case "-port", "--port":
			if i+1 >= len(args) {
				return usageError(apiUsage)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1024 || n > 65535 {
				return usageError("埠號要在 1024～65535 之間")
			}
			port = n
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) != 1 {
		return usageError(apiUsage)
	}
	dir := basePath()
	cfg, err := vocabapi.LoadConfig(dir)
	if err != nil {
		return apiFail(asJSON, "設定檔無法讀取："+err.Error())
	}
	var apply *applyView
	switch rest[0] {
	case "status", "test":
	case "enable", "disable":
		cfg.Enabled = rest[0] == "enable"
		if port != 0 {
			cfg.Port = port
		}
		if cfg.Enabled {
			if _, err := vocabapi.EnsureToken(dir); err != nil {
				return apiFail(asJSON, err.Error())
			}
		}
		if err := vocabapi.SaveConfig(dir, cfg); err != nil {
			return apiFail(asJSON, err.Error())
		}
		v := applySettings(dialEngine, func(string) {}) // 引擎啟動時才讀設定：請它在沒有組字時交接
		apply = &v
	case "token":
		if !regen {
			return usageError(apiUsage)
		}
		if _, err := vocabapi.RegenToken(dir); err != nil {
			return apiFail(asJSON, err.Error())
		}
	default:
		return usageError(apiUsage)
	}
	view := apiStatus(dir, cfg, dialEngine)
	view.Apply = apply
	if rest[0] == "test" {
		view.Test = apiTestCall(view)
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(view)
	}
	printAPI(os.Stdout, rest[0], view)
	if view.Test != nil && !view.Test.OK {
		return errors.New("測試連線失敗")
	}
	return nil
}

func apiFail(asJSON bool, msg string) error {
	if !asJSON {
		return errors.New(msg)
	}
	json.NewEncoder(os.Stdout).Encode(apiView{Schema: contractSchema, Error: msg})
	return errReported
}

func apiStatus(dir string, cfg vocabapi.Config, dial autoupdate.Dialer) apiView {
	addr := cfg.Addr()
	v := apiView{Schema: contractSchema, OK: true, Enabled: cfg.Enabled, Addr: addr,
		MCPURL: "http://" + addr + "/mcp", OpenAPIURL: "http://" + addr + "/openapi.json", State: "engine_not_running"}
	if t, err := vocabapi.LoadToken(dir); err == nil {
		v.Token, v.TokenCreated = t.Value, t.Created
	}
	if info, up, _ := autoupdate.Health(dial); up {
		v.State = info["api_state"]
		if v.State == "" {
			v.State = "unsupported" // 舊版引擎沒有本機 API
		}
		if n, err := strconv.ParseInt(info["api_last_used"], 10, 64); err == nil {
			v.LastUsed = n
		}
	}
	return v
}

// apiTestCall 用設定程式顯示的同一組網址與 token 呼叫一次 status，說明失敗原因。
func apiTestCall(v apiView) *apiTest {
	switch {
	case !v.Enabled:
		return &apiTest{Reason: "off", Detail: "還沒開啟「讓 AI 幫你加詞」"}
	case v.State == "engine_not_running":
		return &apiTest{Reason: "engine_not_running", Detail: "輸入法引擎沒有在執行；切換到靈犀注音打一個字後再試"}
	case v.State == "port_in_use":
		return &apiTest{Reason: "port_in_use", Detail: v.Addr + " 被其他程式占用，請換一個埠號"}
	}
	req, _ := http.NewRequest("GET", "http://"+v.Addr+"/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+v.Token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return &apiTest{Reason: "unreachable", Detail: err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	switch resp.StatusCode {
	case http.StatusOK:
		var st struct {
			Count int `json:"count"`
		}
		json.Unmarshal(body, &st)
		return &apiTest{OK: true, Count: st.Count}
	case http.StatusUnauthorized:
		return &apiTest{Reason: "unauthorized", Detail: "token 不符；如果剛重新產生，請等引擎重新整理或重新貼上設定"}
	}
	return &apiTest{Reason: "error", Detail: fmt.Sprintf("HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(body)))}
}

func printAPI(w io.Writer, op string, v apiView) {
	state := map[string]string{"listening": "執行中", "off": "關閉", "port_in_use": "埠被占用", "engine_not_running": "引擎沒有執行", "unsupported": "引擎版本不支援"}[v.State]
	if state == "" {
		state = v.State
	}
	enabled := "關閉"
	if v.Enabled {
		enabled = "開啟"
	}
	fmt.Fprintf(w, "讓 AI 幫你加詞：%s（引擎：%s）\n網址：http://%s（MCP：%s）\n", enabled, state, v.Addr, v.MCPURL)
	if v.Token != "" {
		masked := v.Token[:min(len(v.Token), 7)] + strings.Repeat("•", 8)
		fmt.Fprintf(w, "token：%s（建立於 %s；完整內容用 -json 查看，不要分享給別人）\n", masked, time.Unix(v.TokenCreated, 0).Format("2006-01-02 15:04"))
	}
	if v.LastUsed > 0 {
		fmt.Fprintf(w, "最後使用：%s\n", time.Unix(v.LastUsed, 0).Format("2006-01-02 15:04"))
	}
	if op == "token" {
		fmt.Fprintln(w, "已重新產生 token：舊的立即失效，已設定的 AI 工具要重新貼上設定。")
	}
	if v.Apply != nil && v.Apply.State != applyApplied && v.Apply.State != applyEngineStopped {
		fmt.Fprintln(w, "設定已儲存，完成組字後生效。")
	}
	if t := v.Test; t != nil {
		if t.OK {
			fmt.Fprintf(w, "測試連線成功：自訂詞 %d 個。\n", t.Count)
		} else {
			fmt.Fprintf(w, "測試連線失敗：%s\n", t.Detail)
		}
	}
}
