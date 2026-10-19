package update

import (
	"fmt"
	"path/filepath"
)

// Releases through 0.1.0-dev.15 installed the original zuin-* program names. The updater keeps
// recognizing them so it can upgrade from, clean up after, and roll back to those releases.

// LinuxLegacyServer marks a Linux bundle that still uses the zuin-* names.
const LinuxLegacyServer = "bin/zuin-server"

// WindowsLayout names the program files of one naming generation.
type WindowsLayout struct{ DLL, Server, UI string }

var (
	windowsCurrent = WindowsLayout{DLL: "0c_tsf.dll", Server: "0c-server.exe", UI: "0c-ui.exe"}
	windowsLegacy  = WindowsLayout{DLL: "zuin_tsf.dll", Server: "zuin-server.exe", UI: "zuin-ui.exe"}
)

// WindowsDLL32 is the i686 TSF DLL that 32-bit applications load (#112). Only 0.1.0-dev.62 and later
// bundles have it, so it is optional; the engine and candidate window stay x64 in the version directory.
const WindowsDLL32 = "x86/0c_tsf.dll"

// WindowsDLL32Of is where the 32-bit DLL sits for the x64 DLL at dll.
func WindowsDLL32Of(dll string) string {
	return filepath.Join(filepath.Dir(dll), filepath.FromSlash(WindowsDLL32))
}

func (l WindowsLayout) names() []string { return []string{l.DLL, l.Server, l.UI} }

// WindowsLayoutOf reports which generation a file list belongs to; mixed or partial lists are rejected.
func WindowsLayoutOf(files map[string]string) (WindowsLayout, error) {
	for _, layout := range []WindowsLayout{windowsCurrent, windowsLegacy} {
		present := 0
		for _, name := range layout.names() {
			if _, ok := files[name]; ok {
				present++
			}
		}
		if present == 3 {
			return layout, nil
		}
		if present > 0 {
			return WindowsLayout{}, fmt.Errorf("incomplete program files for %s", layout.DLL)
		}
	}
	return WindowsLayout{}, fmt.Errorf("missing program files")
}
