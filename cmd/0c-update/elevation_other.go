//go:build !windows

package main

import "fmt"

func validateElevationOrigin(_, sid, base string) error {
	if sid != "" || base != "" {
		return fmt.Errorf("此平台不支援 UAC 更新交接")
	}
	return nil
}
