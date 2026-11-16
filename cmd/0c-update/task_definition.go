package main

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Windows 的自動更新與登入排程只在定義真的變了才重寫（#289 的後續）。
//
// 以前每次完整安裝都以 schtasks.exe /Create /F 覆寫；一般權限的 schedule repair 也曾每次嘗試。端點防護把「反覆建立、
// 改寫排程」看成常駐手法（Sigma 的排程更新規則、事件 4702；未簽章的測試建置在 .14 被 Defender 判
// Behavior:Win32/Execution.A!ml）。所以在定義的 RegistrationInfo 記下內容的雜湊，讀回比對：相同就不寫。

// taskStampPrefix 是寫在排程 RegistrationInfo/Documentation 的標記前綴，後面接定義內容的 SHA-256 前 16 碼。
const taskStampPrefix = "0c-definition:"

var taskStampPattern = regexp.MustCompile(`<Documentation>` + regexp.QuoteMeta(taskStampPrefix) + `([0-9a-f]+)</Documentation>`)

// stampTaskDefinition 在定義（含 </Description>）加上自己內容的雜湊，回傳加好的 XML 與雜湊。
// 雜湊只看我們產生的定義；工作排程器讀回的 XML 會被正規化，不能直接比對全文。
func stampTaskDefinition(definition string) (string, string) {
	sum := sha256.Sum256([]byte(definition))
	stamp := hex.EncodeToString(sum[:8])
	return strings.Replace(definition, "</Description>", "</Description><Documentation>"+taskStampPrefix+stamp+"</Documentation>", 1), stamp
}

// taskDefinitionStamp 取出工作排程器讀回的定義裡的雜湊；舊版建立的排程沒有標記，回傳空字串。
func taskDefinitionStamp(registered string) string {
	if m := taskStampPattern.FindStringSubmatch(registered); m != nil {
		return m[1]
	}
	return ""
}

type taskAction int

const (
	taskKeep     taskAction = iota // 已是目前的定義（提權時也已開放給本人）：不寫
	taskWrite                      // 建立或改寫
	taskReadOnly                   // 定義不同，但只有提權才改得了：一般權限不嘗試，留給下一次提權的完整安裝
)

// planTask 決定一個排程要不要寫：
//   - 不存在：建立（一般權限建立的排程擁有者是本人，之後一般權限也能改）。
//   - 提權：定義相同而且本人已有完全控制才不寫；否則以目前的定義重寫，並把安全描述元開放給本人（TaskOpenSDDL），
//     之後的定義變更一般權限的 repair 就能換掉，不必再等 UAC。
//   - 一般權限：定義相同不寫；不同時本人有完全控制才寫，沒有就不嘗試（每次都會被拒絕，只留下一串存取被拒的紀錄）。
func planTask(exists bool, stamp, want string, writable, elevated bool) taskAction {
	switch {
	case !exists:
		return taskWrite
	case stamp == want && (writable || !elevated):
		return taskKeep
	case elevated || writable:
		return taskWrite
	}
	return taskReadOnly
}
