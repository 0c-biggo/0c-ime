package update

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Asset struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Feed struct {
	Schema  int     `json:"schema"`
	Product string  `json:"product"`
	Version string  `json:"version"`
	Channel string  `json:"channel"`
	Assets  []Asset `json:"assets"`
}

func secureURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("update URL must be HTTPS without credentials or fragments")
	}
	return nil
}
func Client() *http.Client {
	var transport http.RoundTripper
	if roots := tlsRoots(); roots != nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		transport = t
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return secureURL(req.URL.String())
	}}
}
func get(ctx context.Context, client *http.Client, address string) (*http.Response, error) {
	if err := secureURL(address); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("update server returned HTTP %d", response.StatusCode)
	}
	return response, nil
}
func ReadFeed(ctx context.Context, client *http.Client, address, channel string) (*Feed, error) {
	response, err := get(ctx, client, address)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	b, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 1<<20 {
		return nil, fmt.Errorf("update feed too large")
	}
	var feed Feed
	if err = json.Unmarshal(b, &feed); err != nil {
		return nil, err
	}
	if feed.Schema != 1 || feed.Product != "0c" || !ValidVersion(feed.Version) || feed.Channel != channel {
		return nil, fmt.Errorf("unsupported feed or channel mismatch")
	}
	if channel != "public-beta" && channel != "internal-preview" {
		return nil, fmt.Errorf("unknown channel")
	}
	seen := map[string]bool{}
	for _, asset := range feed.Assets {
		key := asset.OS + "/" + asset.Arch
		if seen[key] || asset.Size <= 0 || asset.Size > MaxArchive || !hashPattern.MatchString(asset.SHA256) {
			return nil, fmt.Errorf("invalid or duplicate asset: %s", key)
		}
		seen[key] = true
		if err = secureURL(asset.URL); err != nil {
			return nil, err
		}
	}
	return &feed, nil
}
func (f *Feed) Select(targetOS, targetArch string) (Asset, error) {
	for _, a := range f.Assets {
		if a.OS == targetOS && a.Arch == targetArch {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("no release for %s/%s", targetOS, targetArch)
}

// 下載中斷（連線被切斷、逾時、伺服器暫時錯誤）時重試，並以 Range 從已收到的位置接續。
// 2026-09-30 s3.biggo.com 以 HTTP/2 下載大檔時偶發約 1 秒即斷線（unexpected EOF），curl 同時正常。
// 連續沒有進度的嘗試達 downloadAttempts 次（退避 1、2、4…30 秒，約 90 秒）才放棄；最後仍以大小與 SHA-256 核對整個檔案。
var (
	downloadAttempts   = 8
	downloadRetryDelay = time.Second
)

// permanentError 是重試也不會改變的錯誤（HTTP 4xx、內容與索引不符）。
type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

func Download(ctx context.Context, client *http.Client, asset Asset, directory string) (string, error) {
	return DownloadWithOptions(ctx, client, asset, directory, DownloadOptions{})
}

func DownloadWithOptions(ctx context.Context, client *http.Client, asset Asset, directory string, options DownloadOptions) (string, error) {
	if asset.Size <= 0 || asset.Size > MaxArchive || !hashPattern.MatchString(asset.SHA256) {
		return "", fmt.Errorf("invalid download metadata")
	}
	if err := secureURL(asset.URL); err != nil {
		return "", err
	}
	suffix := ".tar.gz"
	if asset.OS == "windows" {
		suffix = ".zip"
	}
	output := filepath.Join(directory, "0c-"+asset.SHA256+suffix)
	if options.BytesPerSecond < 0 {
		return "", fmt.Errorf("invalid download rate")
	}
	if cached, err := verifiedDownloadCache(output, asset); err != nil {
		return "", err
	} else if cached {
		return output, nil
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	if err := checkDownloadSpace(directory, asset.Size); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(directory, ".0c-download-*")
	if err != nil {
		return "", err
	}
	temp := f.Name()
	defer os.Remove(temp)
	var n int64
	var etag string
	delay := downloadRetryDelay
	pacer := &downloadPacer{rate: options.BytesPerSecond, rateSource: options.Rate}
	report := func(phase string, received int64) {
		if options.Progress != nil {
			options.Progress(DownloadProgress{phase, received, asset.Size})
		}
	}
	report("downloading", 0)
	for failures := 0; ; {
		before := n
		n, etag, err = downloadFrom(ctx, client, asset, f, n, etag, pacer, report)
		if err == nil {
			break
		}
		var permanent permanentError
		var localFileError *os.PathError
		if errors.As(err, &permanent) || errors.As(err, &localFileError) || ctx.Err() != nil {
			f.Close()
			return "", err
		}
		if n > before {
			failures, delay = 0, downloadRetryDelay
		}
		if failures++; failures >= downloadAttempts {
			f.Close()
			return "", err
		}
		report("retrying", n)
		select {
		case <-ctx.Done():
			f.Close()
			return "", ctx.Err()
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if n != asset.Size {
		return "", fmt.Errorf("download size mismatch")
	}
	report("verifying", n)
	sum, err := Hash(temp)
	if err != nil {
		return "", err
	}
	if sum != asset.SHA256 {
		return "", fmt.Errorf("download SHA-256 mismatch")
	}
	// A checksum-named cache entry may already exist. Never reuse it without checking.
	if cached, err := verifiedDownloadCache(output, asset); err != nil {
		return "", err
	} else if cached {
		return output, nil
	}
	if err = os.Rename(temp, output); err != nil {
		return "", err
	}
	return output, nil
}

// CompareVersion uses semantic version precedence (including numeric prerelease components).
func CompareVersion(a, b string) (int, error) {
	if !ValidVersion(a) || !ValidVersion(b) {
		return 0, fmt.Errorf("invalid semantic version")
	}
	aa := strings.SplitN(a, "-", 2)
	bb := strings.SplitN(b, "-", 2)
	for i, x := range strings.Split(aa[0], ".") {
		y := strings.Split(bb[0], ".")[i]
		if n := numberCompare(x, y); n != 0 {
			return n, nil
		}
	}
	if len(aa) != len(bb) {
		if len(aa) == 1 {
			return 1, nil
		}
		return -1, nil
	}
	if len(aa) == 1 {
		return 0, nil
	}
	ap, bp := strings.Split(aa[1], "."), strings.Split(bb[1], ".")
	for i, x := range ap {
		if i >= len(bp) {
			return 1, nil
		}
		y := bp[i]
		if x == y {
			continue
		}
		xn, yn := digitsPattern.MatchString(x), digitsPattern.MatchString(y)
		switch {
		case xn && yn:
			return numberCompare(x, y), nil
		case xn:
			return -1, nil
		case yn:
			return 1, nil
		default:
			if x < y {
				return -1, nil
			}
			return 1, nil
		}
	}
	if len(ap) < len(bp) {
		return -1, nil
	}
	return 0, nil
}
func numberCompare(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// downloadFrom 從 offset 起寫入 f，回傳目前已寫入的位元組數。伺服器不支援 Range（回 200）時從頭寫。
func downloadFrom(ctx context.Context, client *http.Client, asset Asset, f *os.File, offset int64, etag string, pacer *downloadPacer, report func(string, int64)) (int64, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return offset, etag, permanentError{err}
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		if etag != "" {
			request.Header.Set("If-Range", etag)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return offset, etag, err
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusOK:
		if response.ContentLength >= 0 && response.ContentLength != asset.Size {
			return offset, etag, permanentError{fmt.Errorf("download size differs from feed")}
		}
		offset = 0
	case response.StatusCode == http.StatusPartialContent && offset > 0:
		if want := fmt.Sprintf("bytes %d-%d/%d", offset, asset.Size-1, asset.Size); response.Header.Get("Content-Range") != want {
			return offset, etag, permanentError{fmt.Errorf("unexpected Content-Range %q", response.Header.Get("Content-Range"))}
		}
	case response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusRequestTimeout:
		return offset, etag, fmt.Errorf("update server returned HTTP %d", response.StatusCode)
	default:
		return offset, etag, permanentError{fmt.Errorf("update server returned HTTP %d", response.StatusCode)}
	}
	if e := response.Header.Get("ETag"); e != "" && !strings.HasPrefix(e, "W/") {
		etag = e
	}
	if err = f.Truncate(offset); err != nil {
		return offset, etag, permanentError{err}
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return offset, etag, permanentError{err}
	}
	report("downloading", offset)
	writer := &progressWriter{file: f, offset: offset, report: report}
	written, err := io.Copy(writer, io.LimitReader(pacedReader{ctx: ctx, source: response.Body, pacer: pacer}, asset.Size-offset+1))
	offset += written
	if err != nil {
		return offset, etag, err
	}
	if offset > asset.Size {
		return offset, etag, permanentError{fmt.Errorf("download size mismatch")}
	}
	if offset < asset.Size {
		return offset, etag, io.ErrUnexpectedEOF
	}
	return offset, etag, nil
}

type progressWriter struct {
	file   *os.File
	offset int64
	report func(string, int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	w.offset += int64(n)
	w.report("downloading", w.offset)
	return n, err
}
