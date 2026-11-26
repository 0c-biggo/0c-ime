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
	// Kind：core 只更新引擎、字典與神經模型，外殼不變；shell 需要更換外殼（DLL／addon／app）；
	// updater（只出現在 Rules，#295）只換排程用的穩定更新器，套件仍是完整的 core 或 shell 套件。
	Kind string `json:"kind"`
	// Version 是這個資產的套件版本；只用在 Rules（空字串為規則或索引的版本）。舊格式的 Assets 不寫。
	Version string `json:"version,omitempty"`
}

// 資產種類。舊格式的 Assets 只能是 core 或 shell：0.2.14 以前的更新器看到別的種類會拒絕整份索引。
const (
	KindCore    = "core"
	KindShell   = "shell"
	KindUpdater = "updater"
)

// FeedRule（#295）依已安裝的外殼版本與更新器版本，給一個平台不同的資產。客戶端由上往下找第一條符合的規則，
// 沒有符合的才用舊格式的 Assets。0.2.14 以前的更新器不認得 rules（JSON 忽略未知欄位），只看 Assets，所以：
//   - Assets 永遠要是舊更新器能接受的內容（每個平台一個、種類只有 core／shell）；
//   - 規則只能加欄位、不能改既有欄位的意義；客戶端讀不懂的規則（版本格式不對、網址不安全）整條略過，讀不懂的資產種類
//     略過該資產，不拒絕整份索引——之後新增的種類與欄位不會讓這一版的更新器停擺。
//
// 版本範圍：min 含、below 不含，空字串表示不限。installed 是完整安裝（外殼）的版本，讀不到（例如 zuin 舊版）時
// 視為比任何版本都舊；updater 是執行查詢的 0c-update 版本，開發建置視為比任何版本都舊。
type FeedRule struct {
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	InstalledMin   string `json:"installed_min,omitempty"`
	InstalledBelow string `json:"installed_below,omitempty"`
	UpdaterMin     string `json:"updater_min,omitempty"`
	UpdaterBelow   string `json:"updater_below,omitempty"`
	// Version 是這條規則提供的版本；空字串為索引的 version。
	Version string `json:"version,omitempty"`
	// Assets 最多一個 core 或 shell（要裝的版本）加一個 updater；沒有 core／shell 表示這個條件下不提供更新。
	Assets []SignedAsset `json:"assets"`
	// Comment 是給人看的說明，客戶端不看。
	Comment string `json:"comment,omitempty"`

	// unknown 是這一版不認得的鍵：一律當成新的條件，這一版判斷不了，所以整條規則當成不符合（往下找）。
	// 之後加條件（例如 #294 的 TSF 註冊指紋）就不會讓舊客戶端忽略條件、拿到不該拿的資產。
	unknown []string
}

var ruleKeys = map[string]bool{"os": true, "arch": true, "installed_min": true, "installed_below": true, "updater_min": true,
	"updater_below": true, "version": true, "assets": true, "comment": true}

// UnmarshalJSON 記下這一版不認得的鍵（見 unknown）。
func (r *FeedRule) UnmarshalJSON(b []byte) error {
	type plain FeedRule
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	*r = FeedRule(p)
	r.unknown = nil
	for k := range keys {
		if !ruleKeys[k] {
			r.unknown = append(r.unknown, k)
		}
	}
	return nil
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
	// Rules（#295）：依版本給資產的規則，見 FeedRule。0.2.15 起的更新器才看。
	Rules []FeedRule `json:"rules,omitempty"`
}

// TrustedKeys 回傳本建置信任的發行公鑰（正式金鑰；e2etest 建置另加測試金鑰）。
func TrustedKeys() []ed25519.PublicKey { return trustedKeys() }

// SignFeed 簽署索引，回傳索引位元組與 base64 簽章（發布端使用）。規則在這裡嚴格檢查：客戶端只略過讀不懂的規則。
func SignFeed(f *SignedFeed, key ed25519.PrivateKey) ([]byte, []byte, error) {
	if err := f.check(); err != nil {
		return nil, nil, err
	}
	if err := f.checkRules(); err != nil {
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
		if seen[key] || (a.Kind != KindCore && a.Kind != KindShell) || !hashPattern.MatchString(a.SHA256) || a.Size <= 0 || a.Size > MaxArchive {
			return fmt.Errorf("invalid or duplicate asset %s", key)
		}
		if err := secureURL(a.URL); err != nil {
			return err
		}
		seen[key] = true
	}
	return nil
}

// checkRules 是發布端的嚴格檢查：每條規則都要讀得懂、每個資產都要是已知種類，否則不簽。
func (f *SignedFeed) checkRules() error {
	for i, r := range f.Rules {
		if err := r.problem(f.Version, true); err != nil {
			return fmt.Errorf("rule %d (%s/%s): %w", i, r.OS, r.Arch, err)
		}
	}
	return nil
}

func validAsset(a SignedAsset) error {
	if !hashPattern.MatchString(a.SHA256) || a.Size <= 0 || a.Size > MaxArchive {
		return fmt.Errorf("invalid asset hash or size")
	}
	if a.Version != "" && !ValidVersion(a.Version) {
		return fmt.Errorf("invalid asset version")
	}
	return secureURL(a.URL)
}

// problem 回報規則讀不懂的地方。strict（發布端）時未知的資產種類也算錯；客戶端只略過那個資產。
func (r FeedRule) problem(feedVersion string, strict bool) error {
	if r.OS == "" || r.Arch == "" {
		return fmt.Errorf("missing os/arch")
	}
	if len(r.unknown) > 0 {
		return fmt.Errorf("unknown rule conditions %v", r.unknown)
	}
	for _, v := range []string{r.InstalledMin, r.InstalledBelow, r.UpdaterMin, r.UpdaterBelow, r.Version} {
		if v != "" && !ValidVersion(v) {
			return fmt.Errorf("invalid version %q", v)
		}
	}
	if emptyRange(r.InstalledMin, r.InstalledBelow) || emptyRange(r.UpdaterMin, r.UpdaterBelow) {
		return fmt.Errorf("empty version range")
	}
	main, updater := 0, 0
	for _, a := range r.Assets {
		switch a.Kind {
		case KindCore, KindShell:
			main++
		case KindUpdater:
			updater++
		default:
			if strict {
				return fmt.Errorf("unknown asset kind %q", a.Kind)
			}
			continue
		}
		if a.OS != "" && (a.OS != r.OS || a.Arch != r.Arch) {
			return fmt.Errorf("asset for %s/%s in a rule for %s/%s", a.OS, a.Arch, r.OS, r.Arch)
		}
		if err := validAsset(a); err != nil {
			return err
		}
		if strict && a.Kind != KindUpdater && a.Version != "" && a.Version != r.version(feedVersion) {
			return fmt.Errorf("asset version %s differs from rule version %s", a.Version, r.version(feedVersion))
		}
	}
	if main > 1 || updater > 1 {
		return fmt.Errorf("at most one core/shell asset and one updater asset")
	}
	return nil
}

func emptyRange(min, below string) bool {
	if min == "" || below == "" {
		return false
	}
	order, err := CompareVersion(min, below)
	return err != nil || order >= 0
}

func (r FeedRule) version(feedVersion string) string {
	if r.Version != "" {
		return r.Version
	}
	return feedVersion
}

// inRange：v 是否在 [min, below) 裡。v 讀不懂（空字串、開發建置）時視為比任何版本都舊。
func inRange(v, min, below string) bool {
	known := ValidVersion(v)
	if min != "" {
		if !known {
			return false
		}
		if order, err := CompareVersion(v, min); err != nil || order < 0 {
			return false
		}
	}
	if below != "" && known {
		if order, err := CompareVersion(v, below); err != nil || order >= 0 {
			return false
		}
	}
	return true
}

// Offer 是索引給這台電腦的內容（Resolve）。
type Offer struct {
	Version string       // 要更新到的版本
	Asset   *SignedAsset // 要裝的套件（core 或 shell）；nil 表示這個條件下沒有
	Updater *SignedAsset // 只換更新器的套件（Version 已填）；nil 表示沒有
	Rule    int          // 符合的規則；-1 是舊格式的 Assets
}

// Resolve 依規則選出這台電腦的資產（#295）：installed 是完整安裝（外殼）的版本，updater 是執行查詢的 0c-update 版本。
// 沒有符合的規則時照舊以 Assets 選（與 0.2.14 以前的更新器相同）。沒有資產的規則表示「這個條件下不提供更新」；
// 有資產但全是這一版不認得的種類時，這條規則略過。
func (f *SignedFeed) Resolve(goos, arch, installed, updater string) Offer {
	for i, r := range f.Rules {
		if r.OS != goos || r.Arch != arch || r.problem(f.Version, false) != nil ||
			!inRange(installed, r.InstalledMin, r.InstalledBelow) || !inRange(updater, r.UpdaterMin, r.UpdaterBelow) {
			continue
		}
		o := Offer{Version: r.version(f.Version), Rule: i}
		for j := range r.Assets {
			a := r.Assets[j]
			a.OS, a.Arch = goos, arch
			switch {
			case (a.Kind == KindCore || a.Kind == KindShell) && o.Asset == nil:
				a.Version = o.Version
				o.Asset = &a
			case a.Kind == KindUpdater && o.Updater == nil:
				if a.Version == "" {
					a.Version = o.Version
				}
				o.Updater = &a
			}
		}
		if len(r.Assets) > 0 && o.Asset == nil && o.Updater == nil {
			continue // 只有這一版不認得的種類：這條規則不是給這一版的，往下找
		}
		return o
	}
	o := Offer{Version: f.Version, Rule: -1}
	if a, err := f.Select(goos, arch); err == nil {
		c := *a
		c.Version = f.Version
		o.Asset = &c
	}
	return o
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
