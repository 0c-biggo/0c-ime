package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// 索引與下載送出 0c-update 設定的 User-Agent（只有版本與平台）；沒設定時沿用 Go 的預設值。
func TestUserAgentOnFeedAndDownload(t *testing.T) {
	payload := bytes.Repeat([]byte("artificial"), 64)
	sum := sha256.Sum256(payload)
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.UserAgent()
		mu.Unlock()
		if r.URL.Path == "/feed.json" {
			fmt.Fprint(w, `{"schema":1,"product":"0c","version":"0.2.12","channel":"public-beta","assets":[]}`)
			return
		}
		w.Write(payload)
	}))
	defer srv.Close()

	defer func(old string) { UserAgent = old }(UserAgent)
	UserAgent = UpdaterUserAgent("0.2.12", "windows", "amd64")
	if UserAgent != "0c-update/0.2.12 (windows; amd64)" {
		t.Fatalf("User-Agent 格式：%q", UserAgent)
	}
	if _, err := ReadFeed(context.Background(), srv.Client(), srv.URL+"/feed.json", "public-beta"); err != nil {
		t.Fatal(err)
	}
	asset := Asset{OS: "windows", Arch: "amd64", URL: srv.URL + "/archive", SHA256: fmt.Sprintf("%x", sum), Size: int64(len(payload))}
	if _, err := Download(context.Background(), srv.Client(), asset, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/feed.json", "/archive"} {
		if seen[path] != UserAgent {
			t.Errorf("%s 的 User-Agent 是 %q，應為 %q", path, seen[path], UserAgent)
		}
	}

	UserAgent = ""
	if _, err := ReadFeed(context.Background(), srv.Client(), srv.URL+"/feed.json", "public-beta"); err != nil {
		t.Fatal(err)
	}
	if got := seen["/feed.json"]; got != "Go-http-client/1.1" && got != "Go-http-client/2.0" {
		t.Errorf("沒設定時應為 Go 的預設值：%q", got)
	}
}
