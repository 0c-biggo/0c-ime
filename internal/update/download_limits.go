package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

var ErrInsufficientSpace = errors.New("更新所需磁碟空間不足")

// BackgroundBytesPerSecond caps package downloads without limiting interactive
// manual downloads. A 1 GiB maximum archive still fits the 15 minute HTTP limit.
const BackgroundBytesPerSecond = int64(2 << 20)
const downloadSpaceReserve = int64(64 << 20)

type DownloadProgress struct {
	Phase           string
	Received, Total int64
}

type DownloadOptions struct {
	BytesPerSecond int64
	Rate           func() int64 // zero removes the background cap for an interactive request
	Progress       func(DownloadProgress)
}

func verifiedDownloadCache(path string, asset Asset) (bool, error) {
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("download cache entry is not a regular file")
	}
	actual, err := Hash(path)
	if err != nil {
		return false, err
	}
	if actual != asset.SHA256 || st.Size() != asset.Size {
		return false, fmt.Errorf("existing download cache is damaged")
	}
	return true, nil
}

func checkDownloadSpace(directory string, size int64) error {
	available, err := availableSpace(directory)
	if err != nil {
		return fmt.Errorf("無法確認更新磁碟空間：%w", err)
	}
	return requireDownloadSpace(available, size)
}

func requireDownloadSpace(available uint64, size int64) error {
	if size <= 0 || size > MaxArchive {
		return fmt.Errorf("invalid download size")
	}
	required := uint64(size + downloadSpaceReserve)
	if available < required {
		return fmt.Errorf("%w：需至少 %d MiB（含下載保留空間），目前 %d MiB", ErrInsufficientSpace, (required+(1<<20)-1)>>20, available>>20)
	}
	return nil
}

type downloadPacer struct {
	rate       int64
	rateSource func() int64
}
type pacedReader struct {
	ctx    context.Context
	source io.Reader
	pacer  *downloadPacer
}

func (r pacedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	// Bound the amount consumed before waiting; sleeping is cancellable and
	// each read is paced independently, so server stalls cannot build credit.
	if len(p) > 32<<10 {
		p = p[:32<<10]
	}
	n, err := r.source.Read(p)
	rate := r.pacer.rate
	if r.pacer.rateSource != nil {
		rate = r.pacer.rateSource()
	}
	if rate < 0 {
		return n, fmt.Errorf("invalid download rate")
	}
	if n == 0 || rate == 0 {
		return n, err
	}
	delay := time.Duration(int64(n) * int64(time.Second) / rate)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-r.ctx.Done():
		return n, r.ctx.Err()
	case <-timer.C:
		return n, err
	}
}
