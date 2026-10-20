package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/internal/vocabapi"
)

// startVocabAPI 在「讓 AI 幫你加詞」開啟時（api.json，預設關閉）於 127.0.0.1 監聽本機 API（#126）。
// 回傳停止函式與給 health 的狀態；沒有開啟時狀態是 off。埠被占用時照常輸入，只回報 port_in_use。
func startVocabAPI(dir string, eng *engine.Engine, version string, lg *log.Logger) (stop func(), info func() map[string]string) {
	state := func(s string) func() map[string]string {
		return func() map[string]string { return map[string]string{"api_state": s} }
	}
	cfg, err := vocabapi.LoadConfig(dir)
	if err != nil {
		lg.Printf("本機 API 設定無法讀取，不啟動：%v", err)
		return func() {}, state("config_error")
	}
	if !cfg.Enabled {
		return func() {}, state("off")
	}
	if _, err := vocabapi.EnsureToken(dir); err != nil {
		lg.Printf("本機 API 的 token 無法建立，不啟動：%v", err)
		return func() {}, state("token_error")
	}
	l, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		lg.Printf("本機 API 無法監聽 %s：%v", cfg.Addr(), err)
		if addrInUse(err) {
			return func() {}, state("port_in_use")
		}
		return func() {}, state("listen_error")
	}
	api := &vocabapi.Server{Doer: eng, Dir: dir, Version: version}
	hs := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, ErrorLog: lg}
	go hs.Serve(l)
	lg.Printf("本機 API 在 %s 監聽", cfg.Addr())
	return func() { hs.Close() }, func() map[string]string {
		out := map[string]string{"api_state": "listening", "api_addr": cfg.Addr()}
		if t := api.LastUsed(); t > 0 {
			out["api_last_used"] = fmt.Sprint(t)
		}
		return out
	}
}
