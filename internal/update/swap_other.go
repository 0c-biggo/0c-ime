//go:build !darwin && !linux

package update

import "errors"

// swapPaths：其他平台沒有原子交換，macOS 安裝流程退回先移走再放入。
func swapPaths(a, b string) error { return errors.ErrUnsupported }
