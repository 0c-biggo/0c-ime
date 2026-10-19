//go:build windows

package main

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
)

// Windows 用 named pipe（docs/19）：TSF 的 DLL 跑在每個應用程式裡，包含 AppContainer 沙箱，
// 管道的存取控制必須讓同一位使用者的所有應用程式套件都連得上，且不能被別的使用者搶註或讀取。
func defaultPaths() (data, user, sock string) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, "Zuin", "data"), filepath.Join(base, "Zuin", "user.json"), defaultPipe()
}

const transportName = "named pipe 名稱"

// defaultPipe 以使用者 SID 命名，每位使用者一條；名稱不含使用者可控的字串。
func defaultPipe() string { return userPipe("0c") }

// legacyName 是 zuin-server 時期的管道；升級當下仍載入的舊 TSF DLL 會連這裡。
func legacyName() string { return userPipe("zuin") }

func userPipe(prefix string) string {
	if u, err := user.Current(); err == nil && u.Uid != "" {
		return `\\.\pipe\` + prefix + "-" + strings.ToLower(u.Uid)
	}
	return `\\.\pipe\` + prefix
}

func serving(sock string) bool {
	timeout := 500 * time.Millisecond
	if c, err := winio.DialPipe(sock, &timeout); err == nil {
		c.Close()
		return true
	}
	return false
}

// alreadyRunning：管道沒有殘留檔案要清，與 serving 相同。
func alreadyRunning(sock string) bool { return serving(sock) }

// endpoint 是本引擎建立的管道。named pipe 不會被別人移除或取代（第一個實例旗標），lost 恆為 false。
type endpoint struct{ net.Listener }

func (*endpoint) lost() bool { return false }

func listen(sock string) (*endpoint, error) {
	l, err := listenPipe(sock)
	if err != nil {
		return nil, err
	}
	return &endpoint{l}, nil
}

func listenPipe(sock string) (net.Listener, error) {
	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("取得使用者 SID：%w", err)
	}
	// D:P 不繼承；本人完全存取；ALL APPLICATION PACKAGES（S-1-15-2-1）與
	// ALL RESTRICTED APPLICATION PACKAGES（S-1-15-2-2）可讀寫；S:低完整性等級的標籤讓
	// 低完整性的宿主（受保護模式）也能開啟。其他使用者沒有任何 ACE，因此連不上。
	sddl := fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GRGW;;;S-1-15-2-1)(A;;GRGW;;;S-1-15-2-2)S:(ML;;NW;;;LW)", u.Uid)
	return winio.ListenPipe(sock, &winio.PipeConfig{SecurityDescriptor: sddl, InputBufferSize: 1 << 16, OutputBufferSize: 1 << 16})
}
