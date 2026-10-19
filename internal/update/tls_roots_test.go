package update

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 本機測試發布服務的憑證只在 e2etest 建置被信任；正式建置必須拒絕它。
func TestTestCATrustedOnlyInE2EBuild(t *testing.T) {
	cert, err := tls.LoadX509KeyPair("../../testdata/e2e/feed-server.pem", "../../testdata/e2e/feed-server.key")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	defer srv.Close()
	res, err := get(context.Background(), Client(), srv.URL)
	if E2EBuild {
		if err != nil {
			t.Fatalf("e2etest build must trust the test CA: %v", err)
		}
		res.Body.Close()
	} else if err == nil {
		res.Body.Close()
		t.Fatal("release build must not trust the test CA")
	}
}
