//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveLegacyAgentDoesNotBootoutAppService(t *testing.T) {
	for _, kind := range []string{"absent-plist", "stale-plist-app-service", "loaded-legacy"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			bin := filepath.Join(home, "bin")
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			calls := filepath.Join(home, "calls")
			detail := filepath.Join(home, "detail")
			t.Setenv("ZC_AGENT_CALLS", calls)
			t.Setenv("ZC_AGENT_DETAIL", detail)
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$ZC_AGENT_CALLS\"\nif [ \"$1\" = print ]; then /bin/cat \"$ZC_AGENT_DETAIL\"; fi\n"
			if err := os.WriteFile(filepath.Join(bin, "launchctl"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			job := "managed_by = com.apple.xpc.ServiceManagement\nprogram identifier = Contents/MacOS/0c-update (mode: 2)\n"
			if kind != "absent-plist" {
				if err := os.MkdirAll(filepath.Dir(agentPath()), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(agentPath(), []byte("legacy plist"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "loaded-legacy" {
				job = "path = " + agentPath() + "\nprogram = /old/zuin-update\n"
			}
			if err := os.WriteFile(detail, []byte(job), 0600); err != nil {
				t.Fatal(err)
			}
			removeLegacyAgent()
			data, _ := os.ReadFile(calls)
			boots := strings.Count(string(data), "bootout ")
			if (kind == "loaded-legacy" && boots != 1) || (kind != "loaded-legacy" && boots != 0) {
				t.Fatalf("must only bootout the loaded legacy plist, kind=%s calls=%s", kind, data)
			}
			if _, err := os.Stat(agentPath()); !os.IsNotExist(err) {
				t.Fatalf("legacy plist remains: %v", err)
			}
		})
	}
}
