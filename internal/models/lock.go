// Package models 管理個人目錄裡從 Hugging Face 下載的神經模型（#180）：兩份固定清單（models.lock.json、
// pairing.lock.json）與等價雜湊（equivalent.json）、Go 版配對、下載與核對、升級後重新配對，以及沒有模型時的提醒排程。
//
// 個人目錄（~/.local/share/zuin/models）的布局：
//
//	weights/<role>/<revision>/          上游檔案，原樣保存（依 models.lock 核對）
//	<字典 manifest SHA-256>/<role>/      配對後的模型：model.onnx、LICENSE、README.md 硬連結到 weights，
//	                                    manifest.json、vocab.json 是本機配對的結果（依 pairing.lock 核對）
//	<字典 manifest SHA-256>/paired.json  這份配對用的 models.lock 雜湊；models.lock 換了就要重新下載
//	remind.json                         沒有模型時的提醒紀錄
//	state.json                          上一次下載或配對的錯誤
//	operation.json                      進行中的下載（設定程式輪詢進度）
//
// 引擎（internal/neural.Find）只找 <字典 SHA-256>/select|punct；這一層整個目錄以一次 rename 放上去，
// 下載中斷、核對不符或取消時不會留下引擎找得到的半套檔案。換字典但模型沒換時用 weights 重新配對，不重新下載。
package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// 兩個模型的角色，依這個順序下載與顯示。
const (
	RoleSelect = "select"
	RolePunct  = "punct"
)

// Roles 是兩個模型的角色。
var Roles = []string{RoleSelect, RolePunct}

// Label 是角色的中文名稱。
func Label(role string) string {
	if role == RolePunct {
		return "補標點"
	}
	return "選字"
}

// File 是一個檔案的大小與 SHA-256。
type File struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Model 是 models.lock 裡的一個上游模型。
type Model struct {
	Repository string          `json:"repository"`
	Revision   string          `json:"revision"`
	Version    string          `json:"version"`
	License    string          `json:"license"`
	Files      map[string]File `json:"files"`
}

// FileNames 依名稱排序回傳檔案。
func (m Model) FileNames() []string {
	names := make([]string, 0, len(m.Files))
	for n := range m.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Size 是所有檔案的大小合計。
func (m Model) Size() int64 {
	var n int64
	for _, f := range m.Files {
		n += f.Size
	}
	return n
}

// URL 是 Hugging Face 上這個 revision 的頁面（設定程式顯示的連結）。
func (m Model) URL() string {
	return "https://huggingface.co/" + m.Repository + "/tree/" + m.Revision
}

// Lock 是 models.lock.json。SHA256 是原始檔案的雜湊（pairing.lock 的 models_lock_sha256）。
type Lock struct {
	Schema int              `json:"schema"`
	Models map[string]Model `json:"models"`
	SHA256 string           `json:"-"`
}

// Size 是兩個模型要下載的總位元組。
func (l Lock) Size() int64 {
	var n int64
	for _, m := range l.Models {
		n += m.Size()
	}
	return n
}

// Pairing 是 pairing.lock.json：一份出貨字典與 models.lock 的配對結果。
type Pairing struct {
	Schema     int                        `json:"schema"`
	Dictionary string                     `json:"dictionary_manifest_sha256"`
	ModelsLock string                     `json:"models_lock_sha256"`
	Packages   map[string]map[string]File `json:"packages"`
}

// 配對只改這兩個檔案。
var pairedFiles = []string{"manifest.json", "vocab.json"}

func validHex(s string) bool {
	_, err := hex.DecodeString(s)
	return len(s) == 64 && err == nil && s == strings.ToLower(s)
}

// safeName：檔名只能是單純的名稱（models.lock 來自編進程式的清單，這裡仍不讓它指到別的目錄）。
func safeName(n string) bool {
	if n == "" || n == "." || n == ".." {
		return false
	}
	for _, c := range n {
		if c == '/' || c == '\\' || c == 0 {
			return false
		}
	}
	return true
}

// ParseLock 讀 models.lock.json，並核對兩個模型都在、欄位齊全。
func ParseLock(b []byte) (Lock, error) {
	var l Lock
	if err := json.Unmarshal(b, &l); err != nil {
		return Lock{}, fmt.Errorf("models.lock.json：%w", err)
	}
	if l.Schema != 1 || len(l.Models) != len(Roles) {
		return Lock{}, errors.New("models.lock.json：格式不符")
	}
	for _, role := range Roles {
		m, ok := l.Models[role]
		if !ok || m.Repository == "" || !validRevision(m.Revision) {
			return Lock{}, fmt.Errorf("models.lock.json：缺少 %s", role)
		}
		for _, n := range append([]string{"model.onnx"}, pairedFiles...) {
			if _, ok := m.Files[n]; !ok {
				return Lock{}, fmt.Errorf("models.lock.json：%s 缺少 %s", role, n)
			}
		}
		for n, f := range m.Files {
			if !safeName(n) || !validHex(f.SHA256) || f.Size < 0 {
				return Lock{}, fmt.Errorf("models.lock.json：%s/%s 不正確", role, n)
			}
		}
	}
	sum := sha256.Sum256(b)
	l.SHA256 = hex.EncodeToString(sum[:])
	return l, nil
}

// validRevision：Hugging Face 的 revision 是提交的雜湊（小寫十六進位）。
func validRevision(r string) bool {
	if len(r) < 7 || len(r) > 64 {
		return false
	}
	for _, c := range r {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ParsePairing 讀 pairing.lock.json。
func ParsePairing(b []byte) (Pairing, error) {
	var p Pairing
	if err := json.Unmarshal(b, &p); err != nil {
		return Pairing{}, fmt.Errorf("pairing.lock.json：%w", err)
	}
	if p.Schema != 1 || !validHex(p.Dictionary) || !validHex(p.ModelsLock) {
		return Pairing{}, errors.New("pairing.lock.json：格式不符")
	}
	for _, role := range Roles {
		files := p.Packages[role]
		for _, n := range pairedFiles {
			if f, ok := files[n]; !ok || !validHex(f.SHA256) {
				return Pairing{}, fmt.Errorf("pairing.lock.json：%s 缺少 %s", role, n)
			}
		}
	}
	return p, nil
}

// 本機 model.onnx 與 models.lock 的關係（Lock.Match）。
const (
	MatchNone       = ""
	MatchUpstream   = "upstream"   // 就是 models.lock 那一份
	MatchEquivalent = "equivalent" // equivalent.json 記的等價匯出：權重相同、只差匯出資訊
)

// Equivalents 是 models/equivalent.json：與 models.lock 某個 revision 的 model.onnx 等價的其他雜湊。
type Equivalents struct {
	Schema int                        `json:"schema"`
	Models map[string]EquivalentModel `json:"models"`
}

// EquivalentModel 只在 models.lock 的 repository、revision 與 model.onnx 都跟這裡一樣時成立。
type EquivalentModel struct {
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Model      string `json:"model.onnx"`
	Equivalent []File `json:"equivalent"`
}

// ParseEquivalents 讀 equivalent.json。
func ParseEquivalents(b []byte) (Equivalents, error) {
	var e Equivalents
	if err := json.Unmarshal(b, &e); err != nil {
		return Equivalents{}, fmt.Errorf("equivalent.json：%w", err)
	}
	if e.Schema != 1 {
		return Equivalents{}, errors.New("equivalent.json：格式不符")
	}
	for role, m := range e.Models {
		if !validHex(m.Model) || !validRevision(m.Revision) || m.Repository == "" {
			return Equivalents{}, fmt.Errorf("equivalent.json：%s 不正確", role)
		}
		for _, f := range m.Equivalent {
			if !validHex(f.SHA256) || f.Size <= 0 {
				return Equivalents{}, fmt.Errorf("equivalent.json：%s 的等價雜湊不正確", role)
			}
		}
	}
	return e, nil
}

// Match 回報 sha（本機模型 manifest 記的 model.onnx 雜湊）是不是 models.lock 裡 role 的上游模型。
func (l Lock) Match(role, sha string, eq Equivalents) string {
	m, ok := l.Models[role]
	if !ok || sha == "" {
		return MatchNone
	}
	want := m.Files["model.onnx"].SHA256
	if sha == want {
		return MatchUpstream
	}
	e, ok := eq.Models[role]
	if !ok || e.Repository != m.Repository || e.Revision != m.Revision || e.Model != want {
		return MatchNone
	}
	for _, f := range e.Equivalent {
		if f.SHA256 == sha {
			return MatchEquivalent
		}
	}
	return MatchNone
}

// Matches 回報這份配對是否適用目前的 models.lock 與字典。
func (p Pairing) Matches(l Lock, dictionary string) bool {
	return p.ModelsLock == l.SHA256 && p.Dictionary == dictionary
}
