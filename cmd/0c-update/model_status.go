package main

import (
	"fmt"
	"io"

	"git.biggo.com/0c/ime/internal/autoupdate"
)

// modelStatus 只查詢已執行的引擎，不為了查看狀態啟動引擎或模型。
func modelStatus(dial autoupdate.Dialer) map[string]any {
	info, running, err := autoupdate.Health(dial)
	if err != nil {
		return map[string]any{"state": "unavailable"}
	}
	if !running {
		return map[string]any{"state": "not_running"}
	}
	state := map[string]any{"state": "running", "version": info["version"]}
	for _, key := range []string{"selection_state", "punctuation_state"} {
		value := info[key]
		if value == "" {
			value = "unknown" // 舊引擎只有啟動配置，不能據此推論模型仍可用。
		}
		state[key] = value
	}
	for _, key := range []string{"selection_reason", "punctuation_reason"} {
		if info[key] != "" {
			state[key] = info[key]
		}
	}
	return state
}

func printModelStatus(w io.Writer, state map[string]any) {
	switch state["state"] {
	case "not_running":
		fmt.Fprintln(w, "引擎目前未執行；模型運作狀態尚無法確認。")
	case "unavailable":
		fmt.Fprintln(w, "暫時無法查詢模型運作狀態。")
	default:
		fmt.Fprintf(w, "模型運作狀態（引擎 %s）：\n神經選字：%s\n補標點：%s\n",
			state["version"], modelStateLabel(state["selection_state"], "統計引擎")+reasonLabel(state["selection_reason"]),
			modelStateLabel(state["punctuation_state"], "原文提交")+reasonLabel(state["punctuation_reason"]))
	}
}

func modelStateLabel(value any, fallback string) string {
	switch value {
	case "ready":
		return "已就緒"
	case "loading":
		return "載入中，暫用" + fallback
	case "fallback":
		return "模型無法使用，已退回" + fallback
	case "disabled_by_setting":
		return "依設定關閉"
	case "not_installed":
		return "未附模型，使用" + fallback
	case "closed":
		return "已停止，使用" + fallback
	default:
		return "未知（引擎未提供即時狀態）"
	}
}

// reasonLabel 說明模型退回的原因（引擎的 *_reason 代碼）；沒有原因時為空字串。
func reasonLabel(value any) string {
	switch value {
	case "missing_runtime":
		return "（缺少系統元件：Visual C++ 執行階段）"
	case "timeout":
		return "（模型載入逾時）"
	case "failed":
		return "（模型程式無法啟動）"
	}
	return ""
}
