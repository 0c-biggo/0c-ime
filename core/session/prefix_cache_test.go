package session

import "testing"

// Options.PrefixCacheKeys（#307）在解碼時交給解碼器；只影響速度與記憶體，打出來的字跟預設相同。
func TestPrefixCacheKeysReachDecoder(t *testing.T) {
	want := typeReadingLocal(newShemeSession(t), "ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙ ㄕˋ").Text()
	for _, keys := range []int{512, 1, -1} {
		s := newShemeSession(t)
		s.Opt.PrefixCacheKeys = keys
		if got := typeReadingLocal(s, "ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙ ㄕˋ").Text(); got != want {
			t.Fatalf("PrefixCacheKeys=%d：%q，預設 %q", keys, got, want)
		}
		if s.dec.PrefixCacheKeys != keys {
			t.Fatalf("解碼器的快取上限 %d，應為 %d", s.dec.PrefixCacheKeys, keys)
		}
	}
}
