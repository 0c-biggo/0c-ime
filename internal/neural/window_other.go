//go:build !windows

package neural

import "os/exec"

func hideWindow(*exec.Cmd) {}
