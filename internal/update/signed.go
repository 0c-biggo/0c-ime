package update

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SignedFeedSchema 是自動更新使用的已簽章索引。索引位元組以發行金鑰（ed25519）簽章，
// 簽章存在同網址加 ".sig"（base64）。沒有程式碼簽章憑證，發行者信任由這把金鑰承擔。
const SignedFeedSchema = 2

// 允許的本機時差：本機時鐘略慢時，不讓剛發布的索引因「尚未生效」被拒；過期仍嚴格判斷。
const clockSkew = 10 * time.Minute

type SignedAsset struct {
	Asset
	// Kind：core 只更新引擎、字典與神經模型，外殼不變；shell 需要更換外殼（DLL／addon／app）。
	Kind string `json:"kind"`
}

type SignedFeed struct {
	Schema    int           `json:"schema"`
	Product   string        `json:"product"`
	Serial    uint64        `json:"serial"`    // 單調遞增；小於已見過的最大值即拒絕（防倒退）
	Issued    time.Time     `json:"issued"`    // 簽署時間
	Expires   time.Time     `json:"expires"`   // 過期即拒絕，防止攔截者重放舊索引
	Channel   string        `json:"channel"`   // public-beta 或 internal-preview
	Version   string        `json:"version"`   // 本索引推薦的版本
	Rollout   int           `json:"rollout"`   // 0–100：分批比例，依安裝識別碼決定資格
	Withdrawn []string      `json:"withdrawn"` // 已撤回的版本：未開始套用的工作要停止
	Assets    []SignedAsset `json:"assets"`
}

// TrustedKeys 回傳本建置信任的發行公鑰（正式金鑰；e2etest 建置另加測試金鑰）。
func TrustedKeys() []ed25519.PublicKey { return trustedKeys() }

// SignFeed 簽署索引，回傳索引位元組與 base64 簽章（發布端使用）。
func SignFeed(f *SignedFeed, key ed25519.PrivateKey) ([]byte, []byte, error) {
	if err := f.check(); err != nil {
		return nil, nil, err
	}
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	body = append(body, '\n')
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, body))
	return body, []byte(sig + "\n"), nil
}

// VerifySignedFeed 核對簽章、到期與序號後才解析；任何一項不符都拒絕，不使用索引中的內容。
func VerifySignedFeed(body, sig []byte, keys []ed25519.PublicKey, now time.Time, lastSerial uint64) (*SignedFeed, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return nil, fmt.Errorf("invalid feed signature encoding")
	}
	trusted := false
	for _, k := range keys {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, body, raw) {
			trusted = true
			break
		}
	}
	if !trusted {
		return nil, fmt.Errorf("feed signature is not from a trusted release key")
	}
	var f SignedFeed
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, err
	}
	if err := f.check(); err != nil {
		return nil, err
	}
	if now.After(f.Expires) {
		return nil, fmt.Errorf("feed expired at %s", f.Expires.Format(time.RFC3339))
	}
	if f.Issued.After(now.Add(clockSkew)) {
		return nil, fmt.Errorf("feed issued in the future")
	}
	if f.Serial < lastSerial {
		return nil, fmt.Errorf("feed serial %d is older than %d already seen", f.Serial, lastSerial)
	}
	return &f, nil
}

func (f *SignedFeed) check() error {
	if f.Schema != SignedFeedSchema || f.Product != "0c" || f.Serial == 0 || !ValidVersion(f.Version) {
		return fmt.Errorf("unsupported signed feed")
	}
	if f.Channel != "public-beta" && f.Channel != "internal-preview" {
		return fmt.Errorf("unknown release channel")
	}
	if f.Rollout < 0 || f.Rollout > 100 || !f.Expires.After(f.Issued) {
		return fmt.Errorf("invalid rollout or validity window")
	}
	for _, v := range f.Withdrawn {
		if !ValidVersion(v) {
			return fmt.Errorf("invalid withdrawn version")
		}
	}
	seen := map[string]bool{}
	for _, a := range f.Assets {
		key := a.OS + "/" + a.Arch
		if seen[key] || (a.Kind != "core" && a.Kind != "shell") || !hashPattern.MatchString(a.SHA256) || a.Size <= 0 || a.Size > MaxArchive {
			return fmt.Errorf("invalid or duplicate asset %s", key)
		}
		if err := secureURL(a.URL); err != nil {
			return err
		}
		seen[key] = true
	}
	return nil
}

// Select 回傳本平台的資產。
func (f *SignedFeed) Select(goos, arch string) (*SignedAsset, error) {
	for i := range f.Assets {
		if f.Assets[i].OS == goos && f.Assets[i].Arch == arch {
			return &f.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("no update for %s/%s", goos, arch)
}

// IsWithdrawn 回報版本是否已被撤回。
func (f *SignedFeed) IsWithdrawn(version string) bool {
	for _, v := range f.Withdrawn {
		if v == version {
			return true
		}
	}
	return false
}

// ReadSignedFeed 下載索引與簽章並核對；簽章與索引各有大小上限。
func ReadSignedFeed(ctx context.Context, client *http.Client, address string, keys []ed25519.PublicKey, now time.Time, lastSerial uint64) (*SignedFeed, []byte, error) {
	fetch := func(u string, limit int64) ([]byte, error) {
		response, err := get(ctx, client, u)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		b, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > limit {
			return nil, fmt.Errorf("feed response too large")
		}
		return b, nil
	}
	body, err := fetch(address, 1<<20)
	if err != nil {
		return nil, nil, err
	}
	sig, err := fetch(address+".sig", 1024)
	if err != nil {
		return nil, nil, err
	}
	f, err := VerifySignedFeed(body, sig, keys, now, lastSerial)
	return f, body, err
}
