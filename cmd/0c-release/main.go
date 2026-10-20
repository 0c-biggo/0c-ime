// 0c-release 是發布端工具：產生發行金鑰、由實際套件建立並簽署自動更新索引（schema 2）、
// 續簽線上索引（renew），以及用客戶端相同的規則核對已上架的索引與套件（verify）。不上傳；私鑰不得提交到版本庫。
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("用法：0c-release keygen|feed|renew|verify [選項]"))
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "feed":
		err = feed(os.Args[2:])
	case "renew":
		err = renew(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		err = fmt.Errorf("未知命令：%s", os.Args[1])
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "0c-release:", err)
	os.Exit(1)
}

func keygen(args []string) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	private := flags.String("private", "", "私鑰輸出檔（權限 0600；不可已存在）")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *private == "" {
		return fmt.Errorf("需要 -private")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*private, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintln(f, base64.StdEncoding.EncodeToString(priv)); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("私鑰格式錯誤")
	}
	return ed25519.PrivateKey(raw), nil
}

type assetFlags []string

func (a *assetFlags) String() string     { return strings.Join(*a, ",") }
func (a *assetFlags) Set(v string) error { *a = append(*a, v); return nil }

func feed(args []string) error {
	flags := flag.NewFlagSet("feed", flag.ContinueOnError)
	key := flags.String("key", "", "發行私鑰檔")
	serial := flags.Uint64("serial", 0, "索引序號（須大於前一份）")
	valid := flags.Duration("valid", 14*24*time.Hour, "有效期間")
	rollout := flags.Int("rollout", 100, "分批比例 0–100")
	withdrawn := flags.String("withdrawn", "", "已撤回版本（逗號分隔）")
	kind := flags.String("kind", "core", "core（只換引擎／字典／模型）或 shell（需換外殼）")
	var kindFor assetFlags
	flags.Var(&kindFor, "kind-for", "個別平台改用的種類，格式 OS=core|shell（例如 linux=core；可重複）。"+
		"舊更新器裝不上某平台的外殼時，該平台先發 core 換更新器（docs/releasing.md）")
	out := flags.String("out", "", "輸出索引檔；簽章寫入同名 .sig")
	var assets assetFlags
	flags.Var(&assets, "asset", "套件路徑=HTTPS 網址（可重複；每平台一個）")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *key == "" || *out == "" || *serial == 0 || len(assets) == 0 {
		return fmt.Errorf("需要 -key、-out、-serial 與至少一個 -asset")
	}
	kinds, used := map[string]string{}, map[string]bool{}
	for _, spec := range kindFor {
		os, k, ok := strings.Cut(spec, "=")
		if !ok || (k != "core" && k != "shell") || kinds[os] != "" {
			return fmt.Errorf("-kind-for 格式為 OS=core|shell，每個 OS 一次：%q", spec)
		}
		kinds[os] = k
	}
	priv, err := loadKey(*key)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	f := &update.SignedFeed{Schema: update.SignedFeedSchema, Product: "0c", Serial: *serial, Issued: now,
		Expires: now.Add(*valid), Rollout: *rollout}
	if *withdrawn != "" {
		f.Withdrawn = strings.Split(*withdrawn, ",")
	}
	for _, spec := range assets {
		path, url, ok := strings.Cut(spec, "=")
		if !ok {
			return fmt.Errorf("-asset 格式為 路徑=網址")
		}
		r, err := releaseOf(path)
		if err != nil {
			return fmt.Errorf("%s：%w", path, err)
		}
		if f.Version == "" {
			f.Version, f.Channel = r.Version, r.Channel
		}
		if r.Version != f.Version || r.Channel != f.Channel {
			return fmt.Errorf("同一索引的套件版本與通道必須一致")
		}
		sum, err := update.Hash(path)
		if err != nil {
			return err
		}
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		k := *kind
		if v, ok := kinds[r.OS]; ok {
			k = v // 同一 OS 的每個架構都套用
			used[r.OS] = true
		}
		f.Assets = append(f.Assets, update.SignedAsset{Asset: update.Asset{OS: r.OS, Arch: r.Arch, URL: url, SHA256: sum, Size: st.Size()}, Kind: k})
	}
	for os := range kinds {
		if !used[os] {
			return fmt.Errorf("-kind-for %s 沒有對應的 -asset", os)
		}
	}
	body, sig, err := update.SignFeed(f, priv)
	if err != nil {
		return err
	}
	if err = os.WriteFile(*out, body, 0o644); err != nil {
		return err
	}
	return os.WriteFile(*out+".sig", sig, 0o644)
}

// releaseOf 從壓縮包讀出 release.json 的版本、通道與平台（不解壓其他檔案）。
func releaseOf(path string) (*update.Release, error) {
	var data []byte
	match := func(name string) bool {
		return filepath.Base(name) == "release.json" && strings.Count(strings.Trim(name, "/"), "/") == 1
	}
	if strings.HasSuffix(path, ".zip") {
		z, err := zip.OpenReader(path)
		if err != nil {
			return nil, err
		}
		defer z.Close()
		for _, f := range z.File {
			if match(f.Name) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				data, err = io.ReadAll(io.LimitReader(rc, 1<<20))
				rc.Close()
				if err != nil {
					return nil, err
				}
			}
		}
	} else {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		gz, err := gzip.NewReader(file)
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if match(h.Name) {
				if data, err = io.ReadAll(io.LimitReader(tr, 1<<20)); err != nil {
					return nil, err
				}
			}
		}
	}
	if data == nil {
		return nil, fmt.Errorf("找不到 release.json")
	}
	var r update.Release
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.OS == "" && r.Machine != "" {
		r.OS, r.Arch = "linux", map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[r.Machine]
	}
	if r.Product != "0c" || !update.ValidVersion(r.Version) || r.OS == "" || r.Arch == "" {
		return nil, fmt.Errorf("不支援的套件")
	}
	return &r, nil
}

// verify 以客戶端相同的信任根與規則讀取線上索引，並下載每個資產核對大小與 SHA-256。
func verify(args []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	address := flags.String("url", "", "線上索引 HTTPS 網址")
	assets := flags.Bool("assets", true, "一併下載並核對每個資產")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *address == "" {
		return fmt.Errorf("需要 -url")
	}
	ctx := context.Background()
	f, _, err := update.ReadSignedFeed(ctx, update.Client(), *address, update.TrustedKeys(), time.Now(), 0)
	if err != nil {
		return err
	}
	fmt.Printf("索引有效：%s %s serial=%d rollout=%d%% 到期 %s\n", f.Channel, f.Version, f.Serial, f.Rollout, f.Expires.Format(time.RFC3339))
	for _, a := range f.Assets {
		fmt.Printf("  %s/%s %s %d bytes %s %s\n", a.OS, a.Arch, a.Kind, a.Size, a.SHA256, a.URL)
		if !*assets {
			continue
		}
		dir, err := os.MkdirTemp("", "0c-release-verify-")
		if err != nil {
			return err
		}
		_, err = update.Download(ctx, update.Client(), a.Asset, dir)
		os.RemoveAll(dir)
		if err != nil {
			return fmt.Errorf("%s/%s：%w", a.OS, a.Arch, err)
		}
		fmt.Println("    已下載並核對")
	}
	return nil
}

// renew 續簽線上索引：以信任的發行公鑰核對目前的索引與簽章（已過期也可以），內容不變，
// 只把序號加一、重新計算簽發與到期時間。剩餘有效期超過 -min-left 時不動，以 -needed 檔回報是否寫出。
func renew(args []string) error {
	flags := flag.NewFlagSet("renew", flag.ContinueOnError)
	address := flags.String("url", "", "線上索引 HTTPS 網址")
	key := flags.String("key", "", "發行私鑰檔")
	valid := flags.Duration("valid", 14*24*time.Hour, "新的有效期間")
	minLeft := flags.Duration("min-left", 7*24*time.Hour, "剩餘有效期不少於此值時不續簽")
	out := flags.String("out", "", "輸出索引檔；簽章寫入同名 .sig")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *address == "" || *key == "" || *out == "" {
		return fmt.Errorf("需要 -url、-key 與 -out")
	}
	priv, err := loadKey(*key)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	body, sig, err := fetchFeed(ctx, *address)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	f, previous, err := renewFeed(body, sig, update.TrustedKeys(), now, *minLeft)
	if err != nil {
		return err
	}
	if f == nil {
		fmt.Printf("不需續簽：到期 %s（剩 %s）\n", previous.Format(time.RFC3339), previous.Sub(now).Round(time.Hour))
		return nil
	}
	f.Expires = now.Add(*valid)
	body, sig, err = update.SignFeed(f, priv)
	if err != nil {
		return err
	}
	if err = os.WriteFile(*out, body, 0o644); err != nil {
		return err
	}
	if err = os.WriteFile(*out+".sig", sig, 0o644); err != nil {
		return err
	}
	fmt.Printf("已續簽：%s %s serial=%d 到期 %s（原到期 %s）\n", f.Channel, f.Version, f.Serial, f.Expires.Format(time.RFC3339), previous.Format(time.RFC3339))
	return nil
}

// renewFeed 核對目前的索引（以其簽發時間，過期仍可續簽），剩餘有效期少於 minLeft 時回傳序號加一、
// 簽發時間為 now 的新內容（到期時間由呼叫端設定）；不需續簽時回傳 nil 與目前的到期時間。
func renewFeed(body, sig []byte, keys []ed25519.PublicKey, now time.Time, minLeft time.Duration) (*update.SignedFeed, time.Time, error) {
	var current update.SignedFeed
	if err := json.Unmarshal(body, &current); err != nil {
		return nil, time.Time{}, err
	}
	f, err := update.VerifySignedFeed(body, sig, keys, current.Issued, 0)
	if err != nil {
		return nil, time.Time{}, err
	}
	if f.Expires.Sub(now) >= minLeft {
		return nil, f.Expires, nil
	}
	previous := f.Expires
	f.Serial++
	f.Issued = now
	return f, previous, nil
}

func fetchFeed(ctx context.Context, address string) ([]byte, []byte, error) {
	get := func(u string, limit int64) ([]byte, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Cache-Control", "no-cache")
		response, err := update.Client().Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s：HTTP %d", u, response.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if err == nil && int64(len(b)) > limit {
			err = fmt.Errorf("%s：回應過大", u)
		}
		return b, err
	}
	if !strings.HasPrefix(address, "https://") {
		return nil, nil, fmt.Errorf("索引網址必須是 HTTPS")
	}
	body, err := get(address, 1<<20)
	if err != nil {
		return nil, nil, err
	}
	sig, err := get(address+".sig", 1024)
	return body, sig, err
}
