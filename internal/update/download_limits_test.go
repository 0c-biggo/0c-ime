package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimitedDownloadVerifiesAndReusesCacheWithoutNetwork(t *testing.T) {
	payload := bytes.Repeat([]byte("artificial"), 1024)
	sum := sha256.Sum256(payload)
	var calls atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write(payload) }))
	defer srv.Close()
	asset := Asset{OS: "linux", Arch: "amd64", URL: srv.URL + "/archive", SHA256: fmt.Sprintf("%x", sum), Size: int64(len(payload))}
	dir := t.TempDir()
	start := time.Now()
	file, err := DownloadWithOptions(context.Background(), srv.Client(), asset, dir, DownloadOptions{BytesPerSecond: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("background download was not paced")
	}
	data, _ := os.ReadFile(file)
	if !bytes.Equal(data, payload) {
		t.Fatal("limited download changed bytes")
	}
	cached, err := DownloadWithOptions(context.Background(), srv.Client(), asset, dir, DownloadOptions{BytesPerSecond: 64 << 10})
	if err != nil || cached != file || calls.Load() != 1 {
		t.Fatalf("verified cache should avoid network: %s %v calls=%d", cached, err, calls.Load())
	}
	os.WriteFile(file, []byte("corrupt artificial cache"), 0600)
	if _, err := Download(context.Background(), srv.Client(), asset, dir); err == nil || calls.Load() != 1 {
		t.Fatal("damaged cache was trusted or silently replaced")
	}
}

func TestLimitedDownloadCancellationRemovesPartialFile(t *testing.T) {
	payload := bytes.Repeat([]byte("artificial"), 1024)
	sum := sha256.Sum256(payload)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer srv.Close()
	asset := Asset{OS: "linux", Arch: "amd64", URL: srv.URL + "/archive", SHA256: fmt.Sprintf("%x", sum), Size: int64(len(payload))}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := DownloadWithOptions(ctx, srv.Client(), asset, dir, DownloadOptions{BytesPerSecond: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pacing did not cancel: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("cancelled download left partial/cache data")
	}
}

func TestDownloadSpaceReserveRejectsInsufficientAndInvalidSizes(t *testing.T) {
	if err := requireDownloadSpace(64<<20, 10); !errors.Is(err, ErrInsufficientSpace) {
		t.Fatal(err)
	}
	if err := requireDownloadSpace(64<<20+10, 10); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int64{0, -1, MaxArchive + 1} {
		if requireDownloadSpace(^uint64(0), size) == nil {
			t.Fatal("invalid size accepted", size)
		}
	}
	if _, err := availableSpace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := requireDownloadSpace(0, 10); err == nil || !strings.Contains(err.Error(), "目前") {
		t.Fatal("missing actionable space reason", err)
	}
}

func TestDownloadReportsActualBytesThenVerificationAndAllowsAcceleration(t *testing.T) {
	payload := bytes.Repeat([]byte("progress-fixture"), 8192)
	sum := sha256.Sum256(payload)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer srv.Close()
	asset := Asset{OS: "windows", Arch: "amd64", URL: srv.URL + "/archive", SHA256: fmt.Sprintf("%x", sum), Size: int64(len(payload))}
	var progress []DownloadProgress
	reads := 0
	_, err := DownloadWithOptions(context.Background(), srv.Client(), asset, t.TempDir(), DownloadOptions{
		BytesPerSecond: 64 << 10,
		Rate: func() int64 {
			reads++
			if reads == 1 {
				return 64 << 10
			}
			return 0
		},
		Progress: func(p DownloadProgress) { progress = append(progress, p) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if reads < 2 || len(progress) < 3 {
		t.Fatalf("rate/progress were not live: %d %+v", reads, progress)
	}
	var last int64
	partial := false
	for _, p := range progress {
		if p.Received < last || p.Received > asset.Size || p.Total != asset.Size {
			t.Fatalf("invalid actual bytes: %+v", p)
		}
		partial = partial || (p.Received > 0 && p.Received < asset.Size)
		last = p.Received
	}
	end := progress[len(progress)-1]
	if !partial || end.Phase != "verifying" || end.Received != asset.Size {
		t.Fatalf("verification must follow real partial progress: %+v", progress)
	}
}

func TestFailedHashNeverReportsInstallationReady(t *testing.T) {
	payload := bytes.Repeat([]byte("bad-hash"), 1024)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer srv.Close()
	asset := Asset{OS: "linux", Arch: "amd64", URL: srv.URL, SHA256: strings.Repeat("a", 64), Size: int64(len(payload))}
	dir := t.TempDir()
	var phases []string
	if _, err := DownloadWithOptions(context.Background(), srv.Client(), asset, dir, DownloadOptions{Progress: func(p DownloadProgress) { phases = append(phases, p.Phase) }}); err == nil {
		t.Fatal("bad hash accepted")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("bad hash left cache")
	}
	for _, phase := range phases {
		if phase != "downloading" && phase != "verifying" {
			t.Fatal(phase)
		}
	}
}
