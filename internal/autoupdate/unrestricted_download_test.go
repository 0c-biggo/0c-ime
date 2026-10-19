package autoupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"git.biggo.com/0c/ime/internal/update"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The real downloader and coordinator run here; prepare/apply are artificial.
type unrestrictedNetworkPlatform struct {
	*fakePlatform
	client *http.Client
}

func (p *unrestrictedNetworkPlatform) Download(ctx context.Context, target Target, dir string) (string, error) {
	p.calls = append(p.calls, "download")
	goos, arch := p.Target()
	return update.DownloadWithOptions(ctx, p.client, update.Asset{OS: goos, Arch: arch, URL: target.URL, SHA256: target.SHA256, Size: target.Size}, dir, update.DownloadOptions{BytesPerSecond: update.BackgroundBytesPerSecond})
}
func TestUnrestrictedNetworkDownloadClearsLegacyMeteredPending(t *testing.T) {
	payload := bytes.Repeat([]byte("artificial archive bytes"), 4096)
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write(payload) }))
	defer server.Close()
	for _, metered := range []bool{false, true} {
		t.Run(fmt.Sprintf("metered=%v", metered), func(t *testing.T) {
			fake := &fakePlatform{installed: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "core", 100), cond: Conditions{Metered: metered, IdleKnown: true, Idle: time.Hour}}
			asset := &fake.feed.Assets[0]
			asset.URL, asset.SHA256, asset.Size = server.URL+"/archive", sum, int64(len(payload))
			co, _ := newCoordinator(t, fake)
			co.Platform = &unrestrictedNetworkPlatform{fakePlatform: fake, client: server.Client()}
			s, err := Load(co.Base)
			if err != nil {
				t.Fatal(err)
			}
			s.Stage = StageFound
			s.Target = &Target{Version: fake.feed.Version, Kind: "core", URL: asset.URL, SHA256: sum, Size: asset.Size}
			s.LastCheck = co.Now()
			s.NextCheck = co.Now().Add(time.Hour)
			s.block(co.Now(), "metered_network", "legacy network gate")
			if err := Save(co.Base, s); err != nil {
				t.Fatal(err)
			}
			before := requests.Load()
			state := run(t, co)
			if state.Stage != StageActive || state.Blocked != nil || requests.Load() != before+1 || !strings.Contains(strings.Join(fake.calls, " "), "download") {
				t.Fatal("cost blocked the real download or stale state survived", state, fake.calls, requests.Load())
			}
			data, err := os.ReadFile(filepath.Join(co.Base, "update", "downloads", "0c-"+sum+".tar.gz"))
			if err != nil || !bytes.Equal(data, payload) {
				t.Fatal("downloaded archive did not match", err)
			}
			t.Logf("metered=%v request_delta=1 verified_bytes=%d legacy_block_cleared=true stage=%s", metered, len(data), state.Stage)
		})
	}
}
