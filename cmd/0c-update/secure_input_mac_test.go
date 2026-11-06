package main

import (
	"slices"
	"testing"
)

func TestSecureInputPIDs(t *testing.T) {
	// 節錄自 Intel macOS 15.8.2 VM：背景程式持有安全輸入時的 ioreg -l -d 1 -w 0 -k IOConsoleUsers。
	held := `+-o Root  <class IORegistryEntry, id 0x100000100, retain 31>
    {
      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes,"kSCSecuritySessionID"=100009,"kCGSSessionSecureInputPID"=358,"kCGSSessionUserIDKey"=501,"kCGSessionLoginDoneKey"=Yes,"kCGSSessionUserNameKey"="zc"})
      "kCGSSessionSecureInputPID"=358
    }`
	released := `+-o Root  <class IORegistryEntry, id 0x100000100, retain 31>
    {
      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes,"kSCSecuritySessionID"=100009,"kCGSSessionUserIDKey"=501,"kCGSessionLoginDoneKey"=Yes,"kCGSSessionUserNameKey"="zc"})
    }`
	for _, tc := range []struct {
		name  string
		input string
		want  []int
	}{
		{"held", held, []int{358}},
		{"released", released, nil},
		{"two sessions", `"kCGSSessionSecureInputPID"=12 "kCGSSessionSecureInputPID"=0 "kCGSSessionSecureInputPID"=34 "kCGSSessionSecureInputPID"=12`, []int{12, 34}},
		{"empty", "", nil},
	} {
		if got := secureInputPIDs(tc.input); !slices.Equal(got, tc.want) {
			t.Errorf("%s: secureInputPIDs = %v, want %v", tc.name, got, tc.want)
		}
	}
}
