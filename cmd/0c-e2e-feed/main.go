// 0c-e2e-feed 是更新 E2E 的本機 HTTPS 測試發布服務（僅測試，docs/update-e2e.md）：提供已簽章索引與
// 套件，並可由本機控制端點注入故障（錯誤、慢速、斷線、截斷、竄改）。憑證由測試 CA 簽發，只有
// -tags e2etest 建置的 0c-update 信任；索引以測試發行金鑰簽署（0c-release feed -key testdata/e2e/release-test.key）。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type fault struct {
	Kind  string        `json:"kind"`  // none、error、slow、drop、truncate、tamper
	Match string        `json:"match"` // 路徑包含此字串才套用；空值套用全部
	Delay time.Duration `json:"delay"` // slow：每 64 KiB 的延遲
	Count int           `json:"count"` // 套用次數；0 為不限
}

type server struct {
	root string
	mu   sync.Mutex
	f    fault
	hits map[string]int
}

func loopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

func (s *server) control(w http.ResponseWriter, r *http.Request) {
	if !loopback(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/_control/fault":
		q := r.URL.Query()
		f := fault{Kind: q.Get("kind"), Match: q.Get("match")}
		if d := q.Get("delay"); d != "" {
			f.Delay, _ = time.ParseDuration(d)
		}
		f.Count, _ = strconv.Atoi(q.Get("count"))
		if f.Kind == "" {
			f.Kind = "none"
		}
		s.f = f
		json.NewEncoder(w).Encode(s.f)
	case "/_control/hits":
		json.NewEncoder(w).Encode(s.hits)
		if r.URL.Query().Get("reset") == "1" {
			s.hits = map[string]int{}
		}
	default:
		http.NotFound(w, r)
	}
}

// take 回傳本次請求要套用的故障，並扣除次數。
func (s *server) take(path string) fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits[path]++
	f := s.f
	if f.Kind == "" || f.Kind == "none" || (f.Match != "" && !strings.Contains(path, f.Match)) {
		return fault{Kind: "none"}
	}
	if f.Count > 0 {
		s.f.Count--
		if s.f.Count == 0 {
			s.f = fault{Kind: "none"}
		}
	}
	return f
}

func (s *server) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/_control/") {
		s.control(w, r)
		return
	}
	name := filepath.Join(s.root, filepath.FromSlash(filepath.Clean("/"+r.URL.Path)))
	f := s.take(r.URL.Path)
	log.Printf("%s %s fault=%s", r.RemoteAddr, r.URL.Path, f.Kind)
	body, err := os.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch f.Kind {
	case "error":
		http.Error(w, "injected failure", http.StatusServiceUnavailable)
		return
	case "tamper":
		if len(body) > 0 {
			body = append([]byte(nil), body...)
			body[len(body)/2] ^= 0xff
		}
	case "drop", "truncate":
		// 宣告完整長度但只送一半：drop 直接斷線，truncate 正常結束回應。
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		w.Write(body[:len(body)/2])
		if f.Kind == "drop" {
			if hj, ok := w.(http.Hijacker); ok {
				if c, _, err := hj.Hijack(); err == nil {
					c.Close()
				}
			}
		}
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if f.Kind == "slow" {
		for i := 0; i < len(body); i += 64 << 10 {
			end := min(i+64<<10, len(body))
			w.Write(body[i:end])
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			time.Sleep(f.Delay)
		}
		return
	}
	io.Copy(w, strings.NewReader(string(body)))
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8443", "監聽位址")
	root := flag.String("root", ".", "發布目錄（feeds/、releases/）")
	cert := flag.String("cert", "testdata/e2e/feed-server.pem", "測試 CA 簽發的伺服器憑證")
	key := flag.String("key", "testdata/e2e/feed-server.key", "伺服器私鑰")
	flag.Parse()
	s := &server{root: *root, hits: map[string]int{}, f: fault{Kind: "none"}}
	fmt.Printf("0c-e2e-feed：https://%s/ 服務 %s\n", *addr, *root)
	srv := &http.Server{Addr: *addr, Handler: http.HandlerFunc(s.serve), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServeTLS(*cert, *key))
}
