package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func ruleAsset(kind, version, letter string) SignedAsset {
	return SignedAsset{Asset: Asset{OS: "windows", Arch: "amd64", URL: "https://example.org/" + letter + ".zip", SHA256: strings.Repeat(letter, 64), Size: 10}, Kind: kind, Version: version}
}

// 規則依已安裝（外殼）與更新器版本選資產；第一條符合的生效，沒有符合的照舊用 Assets（#295）。
func TestResolveRulesByInstalledAndUpdaterVersion(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f := testFeed(now)
	f.Version = "0.2.16"
	f.Assets[0].Kind = "shell"
	f.Rules = []FeedRule{
		// 0.2.15 以後的外殼：只換核心，另外附只換更新器的資產
		{OS: "windows", Arch: "amd64", InstalledMin: "0.2.15", Assets: []SignedAsset{ruleAsset("core", "", "b"), ruleAsset("updater", "0.2.16", "c")}},
		// 更新器有問題的 0.2.15：只換更新器，不給完整安裝
		{OS: "windows", Arch: "amd64", UpdaterMin: "0.2.15", UpdaterBelow: "0.2.16-0", Assets: []SignedAsset{ruleAsset("updater", "0.2.16", "d")}},
		{OS: "linux", Arch: "amd64", Version: "0.2.15", Assets: []SignedAsset{{Asset: Asset{OS: "linux", Arch: "amd64", URL: "https://example.org/l.tgz", SHA256: strings.Repeat("e", 64), Size: 10}, Kind: "shell"}}},
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if _, _, err := SignFeed(f, priv); err != nil {
		t.Fatal(err)
	}
	letter := func(a *SignedAsset) string {
		if a == nil {
			return ""
		}
		return a.SHA256[:1]
	}
	cases := []struct {
		goos, installed, updater string
		version, kind, sha       string
		updaterSHA               string
		rule                     int
	}{
		{"windows", "0.2.15", "0.2.15", "0.2.16", "core", "b", "c", 0},
		{"windows", "0.2.17", "dev", "0.2.16", "core", "b", "c", 0},
		{"windows", "0.2.14", "0.2.15", "0.2.16", "", "", "d", 1},
		{"windows", "0.2.14", "0.2.14", "0.2.16", "shell", "a", "", -1},
		{"windows", "", "", "0.2.16", "shell", "a", "", -1}, // 讀不到的版本比任何版本都舊
		{"linux", "0.2.10", "0.2.10", "0.2.15", "shell", "e", "", 2},
		{"darwin", "0.2.10", "0.2.10", "0.2.16", "", "", "", -1},
	}
	for _, c := range cases {
		o := f.Resolve(c.goos, "amd64", c.installed, c.updater)
		kind := ""
		if o.Asset != nil {
			kind = o.Asset.Kind
			if o.Asset.Version != o.Version || o.Asset.OS != c.goos {
				t.Errorf("%+v: asset %+v", c, o.Asset)
			}
		}
		if o.Version != c.version || kind != c.kind || letter(o.Asset) != c.sha || letter(o.Updater) != c.updaterSHA || o.Rule != c.rule {
			t.Errorf("%+v: got %+v", c, o)
		}
	}
	if o := f.Resolve("windows", "amd64", "0.2.15", "0.2.15"); o.Updater == nil || o.Updater.Version != "0.2.16" {
		t.Fatalf("updater version: %+v", o.Updater)
	}
}

// 發布端嚴格：讀不懂的規則不簽。客戶端寬鬆：讀不懂的規則或資產種類略過，不拒絕整份索引，也不影響其他規則。
func TestRulesAreStrictWhenSigningAndLenientWhenReading(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	for name, rule := range map[string]FeedRule{
		"unknown kind":  {OS: "windows", Arch: "amd64", Assets: []SignedAsset{ruleAsset("delta", "", "b")}},
		"bad version":   {OS: "windows", Arch: "amd64", InstalledMin: "latest", Assets: []SignedAsset{ruleAsset("core", "", "b")}},
		"empty range":   {OS: "windows", Arch: "amd64", InstalledMin: "0.2.15", InstalledBelow: "0.2.15", Assets: []SignedAsset{ruleAsset("core", "", "b")}},
		"two main":      {OS: "windows", Arch: "amd64", Assets: []SignedAsset{ruleAsset("core", "", "b"), ruleAsset("shell", "", "c")}},
		"http":          {OS: "windows", Arch: "amd64", Assets: []SignedAsset{{Asset: Asset{URL: "http://example.org/x", SHA256: strings.Repeat("b", 64), Size: 1}, Kind: "core"}}},
		"other os":      {OS: "windows", Arch: "amd64", Assets: []SignedAsset{{Asset: Asset{OS: "linux", Arch: "amd64", URL: "https://example.org/x", SHA256: strings.Repeat("b", 64), Size: 1}, Kind: "core"}}},
		"version mixup": {OS: "windows", Arch: "amd64", Version: "0.2.16", Assets: []SignedAsset{ruleAsset("core", "0.2.15", "b")}},
	} {
		f := testFeed(now)
		f.Rules = []FeedRule{rule}
		if _, _, err := SignFeed(f, priv); err == nil {
			t.Errorf("%s: signed", name)
		}
	}
	// 未來的索引：舊格式 Assets 照舊；規則裡有這一版不認得的資產種類與資產欄位、頂層有新欄位。
	future := []byte(`{"schema":2,"product":"0c","serial":3,"issued":"` + now.Format(time.RFC3339) + `","expires":"` + now.Add(time.Hour).Format(time.RFC3339) +
		`","channel":"public-beta","version":"0.2.20","rollout":100,"withdrawn":null,` +
		`"assets":[{"os":"windows","arch":"amd64","url":"https://example.org/a.zip","sha256":"` + strings.Repeat("a", 64) + `","size":10,"kind":"shell"}],` +
		`"rules":[{"os":"windows","arch":"amd64","installed_min":"not-a-version","assets":[{"kind":"core","url":"https://example.org/x","sha256":"` + strings.Repeat("b", 64) + `","size":1}]},` +
		`{"os":"windows","arch":"amd64","installed_min":"0.2.15","comment":"新種類與欄位","assets":[{"kind":"delta","url":"https://example.org/d","sha256":"` + strings.Repeat("d", 64) + `","size":1,"from":"0.2.19"},` +
		`{"kind":"core","url":"https://example.org/c.zip","sha256":"` + strings.Repeat("c", 64) + `","size":10}]}],"mirrors":["https://mirror.example.org/"]}` + "\n")
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, future)))
	f, err := VerifySignedFeed(future, sig, []ed25519.PublicKey{pub}, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if o := f.Resolve("windows", "amd64", "0.2.16", "0.2.16"); o.Rule != 1 || o.Asset == nil || o.Asset.Kind != "core" || o.Asset.SHA256[:1] != "c" {
		t.Fatalf("future rule: %+v", o)
	}
	if o := f.Resolve("windows", "amd64", "0.2.10", "0.2.16"); o.Rule != -1 || o.Asset == nil || o.Asset.Kind != "shell" {
		t.Fatalf("legacy fallback: %+v", o)
	}
	// 只有不認得的種類的規則略過，往下找；沒有資產的規則是「不提供更新」。
	f.Rules = append([]FeedRule{{OS: "windows", Arch: "amd64", Assets: []SignedAsset{{Kind: "delta", Asset: Asset{URL: "https://example.org/d", SHA256: strings.Repeat("d", 64), Size: 1}}}}}, f.Rules...)
	if o := f.Resolve("windows", "amd64", "0.2.16", "0.2.16"); o.Rule != 2 || o.Asset == nil || o.Asset.SHA256[:1] != "c" {
		t.Fatalf("unknown-only rule not skipped: %+v", o)
	}
	// 這一版不認得的條件（新的鍵）：判斷不了，整條規則當成不符合，不能忽略條件就給資產。
	var conditioned FeedRule
	if err := json.Unmarshal([]byte(`{"os":"windows","arch":"amd64","tsf_registration":"abc","comment":"x","assets":[{"kind":"core","url":"https://example.org/z","sha256":"`+strings.Repeat("e", 64)+`","size":1}]}`), &conditioned); err != nil {
		t.Fatal(err)
	}
	f.Rules = append([]FeedRule{conditioned}, f.Rules...)
	if o := f.Resolve("windows", "amd64", "0.2.16", "0.2.16"); o.Rule != 3 || o.Asset.SHA256[:1] != "c" {
		t.Fatalf("unknown condition must not match: %+v", o)
	}
	f.Rules = append([]FeedRule{{OS: "windows", Arch: "amd64", InstalledBelow: "0.2.12"}}, f.Rules...)
	if o := f.Resolve("windows", "amd64", "0.2.11", "0.2.16"); o.Rule != 0 || o.Asset != nil || o.Updater != nil {
		t.Fatalf("empty rule must withhold the update: %+v", o)
	}
	// 舊格式 Assets 出現新種類時，連 0.2.14 以前的更新器都會拒絕整份索引：發布端必須擋。
	f2 := testFeed(now)
	f2.Assets[0].Kind = "updater"
	if _, _, err := SignFeed(f2, priv); err == nil {
		t.Fatal("updater kind in legacy assets")
	}
}

// 沒有規則的索引與 0.2.14 以前的格式相同（不出現 rules、資產沒有 version）。
func TestFeedWithoutRulesKeepsLegacyShape(t *testing.T) {
	now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	body, _, err := SignFeed(testFeed(now), priv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "rules") || strings.Count(string(body), `"version"`) != 1 {
		t.Fatalf("%s", body)
	}
}
