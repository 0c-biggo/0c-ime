package autoupdate

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// randomDuration 回傳 [0, max) 的隨機時間，分散同時到期的查詢。
func randomDuration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	return time.Duration(binary.BigEndian.Uint64(b[:]) % uint64(max))
}
