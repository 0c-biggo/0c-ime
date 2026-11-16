package main

import (
	"context"
	"errors"
	"net"
	"net/url"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/updatew"
)

// exitCodeFor 是 0c-update 結束時的結束碼。啟動器 0c-updatew 執行的三種呼叫（auto、engine、protocol）遇到預期中的
// 暫時失敗時以 updatew.ExitTemporary（75）結束：啟動器不把它算成更新器異常，不會因為網路斷線、另一個更新正在執行、
// 使用者取消 UAC 就改用上一版（契約見 internal/updatew）。其他錯誤照舊是 1。
func exitCodeFor(args []string, err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errOtherAccount):
		return otherAccountExit // 安裝程式靠這個結束碼擋下安裝（#199）
	case len(args) > 0 && updatew.Verb(args[0]) && temporaryFailure(err):
		return updatew.ExitTemporary
	}
	return 1
}

// temporaryFailure：不是更新器本身的問題、下一次喚起就可能成功的失敗。
func temporaryFailure(err error) bool {
	if errors.Is(err, autoupdate.ErrLocked) || errors.Is(err, autoupdate.ErrUpdateInProgress) ||
		errors.Is(err, autoupdate.ErrTargetChanged) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) || userCancelled(err) {
		return true
	}
	var netErr net.Error
	var urlErr *url.Error
	return errors.As(err, &netErr) || errors.As(err, &urlErr)
}
