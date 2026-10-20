//go:build darwin

package autoupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

type volumePlatform struct {
	*fakePlatform
	client    *http.Client
	directory string
}

func (p *volumePlatform) Download(ctx context.Context, target Target, _ string) (string, error) {
	return update.DownloadWithOptions(ctx, p.client, update.Asset{OS: "linux", Arch: "amd64", URL: target.URL, SHA256: target.SHA256, Size: target.Size}, p.directory,
		update.DownloadOptions{BytesPerSecond: update.BackgroundBytesPerSecond})
}

// Explicit, supervised acceptance on an owned 96 MiB mounted disk image. This
// never fills the host volume; normal go test skips it without the fixture.
func TestAcceptanceRealResourceVolume(t *testing.T) {
	volume := os.Getenv("ZC_ACCEPTANCE_RESOURCE_VOLUME")
	if volume == "" {
		t.Skip("requires an explicitly prepared isolated acceptance disk image")
	}
	if !strings.HasPrefix(volume, "/private/tmp/0c-") || filepath.Clean(volume) != volume {
		t.Fatal("invalid artificial volume path")
	}
	marker, err := os.ReadFile(filepath.Join(volume, "0c-artificial-volume"))
	if err != nil || string(marker) != "artificial acceptance volume only\n" {
		t.Fatal("missing volume ownership marker")
	}
	var mount, parent syscall.Stat_t
	if syscall.Stat(volume, &mount) != nil || syscall.Stat(filepath.Dir(volume), &parent) != nil || mount.Dev == parent.Dev {
		t.Fatal("path is not a separate mounted filesystem")
	}
	space := func() uint64 {
		var s syscall.Statfs_t
		if err := syscall.Statfs(volume, &s); err != nil {
			t.Fatal(err)
		}
		return uint64(s.Bavail) * uint64(s.Bsize)
	}
	before := space()
	if before < 70<<20 || before > 128<<20 {
		t.Fatal("requires a small empty isolated volume", before)
	}
	filler := filepath.Join(volume, "artificial-pressure.bin")
	directory := filepath.Join(volume, "artificial-download")
	defer os.Remove(filler)
	defer os.RemoveAll(directory)
	f, err := os.OpenFile(filler, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	remaining := int64(before - (32 << 20))
	chunk := make([]byte, 1<<20)
	for remaining > 0 {
		n := min(remaining, int64(len(chunk)))
		if _, err = f.Write(chunk[:n]); err != nil {
			f.Close()
			t.Fatal(err)
		}
		remaining -= n
	}
	if err = f.Sync(); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	pressured := space()
	payload := bytes.Repeat([]byte("0c artificial archive\n"), 200000)
	sum := sha256.Sum256(payload)
	var calls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write(payload) }))
	defer server.Close()
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "core", 100)}
	p.feed.Assets[0].URL, p.feed.Assets[0].SHA256, p.feed.Assets[0].Size = server.URL+"/artificial", fmt.Sprintf("%x", sum), int64(len(payload))
	co, _ := newCoordinator(t, p)
	co.Platform = &volumePlatform{fakePlatform: p, client: server.Client(), directory: directory}
	s, _ := Load(co.Base)
	s.Mode = ModeAsk
	if err = Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
	s = run(t, co)
	if s.Stage != StageFound || s.Blocked == nil || s.Blocked.Reason != "disk_full" || calls.Load() != 0 {
		t.Fatal("real low-space gate did not preserve a recoverable pending update", s, calls.Load())
	}
	if err = os.Remove(filler); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	s = run(t, co)
	elapsed := time.Since(start)
	if s.Stage != StagePrepared || s.Blocked == nil || s.Blocked.Reason != "needs_consent" || calls.Load() != 1 {
		t.Fatal("freeing space did not recover the verified download", s, calls.Load())
	}
	minimum := time.Duration(int64(len(payload)) * int64(time.Second) / update.BackgroundBytesPerSecond)
	if elapsed < minimum*9/10 {
		t.Fatal("real background HTTP download bypassed the 2 MiB/s cap", elapsed, minimum)
	}
	if hash, err := update.Hash(s.Download); err != nil || hash != fmt.Sprintf("%x", sum) {
		t.Fatal("real download integrity failed", hash, err)
	}
	asset := p.feed.Assets[0].Asset
	if _, err = update.DownloadWithOptions(context.Background(), server.Client(), asset, directory, update.DownloadOptions{BytesPerSecond: update.BackgroundBytesPerSecond}); err != nil || calls.Load() != 1 {
		t.Fatal("verified cache made another network request", err)
	}
	t.Logf("acceptance_resource_json={\"result\":\"pass\",\"free_before\":%d,\"free_pressured\":%d,\"payload_bytes\":%d,\"elapsed_seconds\":%.3f,\"requests\":%d,\"scope\":\"real filesystem and TLS download; artificial feed/platform; no installation\"}", before, pressured, len(payload), elapsed.Seconds(), calls.Load())
}
