// Package emoji provides an authored, offline selection palette. It has no
// keyword dictionary or language-model relationship.
package emoji

import "slices"

const MaxRecent = 64

// EqualsID is reserved outside the catalog so replay cannot confuse a literal
// equals sign with an emoji after recent ordering or the option changes.
const EqualsID = 1 << 20

// These are manually selected Unicode strings, not imported names, annotations,
// images or third-party kaomoji collections. Preserve all sequence characters.
var faces = []string{
	"😀", "😃", "😄", "😁", "😆", "😅", "😂", "🤣", "😊", "🙂",
	"🙃", "😉", "😍", "🥰", "😘", "😋", "😎", "🤩", "🥳", "🤔",
	"🤗", "🫡", "🤭", "🫢", "😐", "😑", "😶", "🙄", "😏", "😬",
	"😮", "😯", "😲", "😳", "🥺", "🥹", "😢", "😭", "😤", "😠",
	"😡", "🤯", "😱", "😰", "😥", "😓", "😴", "🥱", "😷", "🤒",
	"👍", "👎", "👌", "✌️", "🤞", "🤟", "🤘", "🤙", "👏", "🙌",
	"🙏", "👋", "💪", "🤝", "🫶", "👍🏻", "👍🏼", "👍🏽", "👍🏾", "👍🏿",
	"❤️", "🧡", "💛", "💚", "💙", "💜", "🖤", "🤍", "💕", "💔",
	"💯", "✅", "❌", "❓", "❗", "🔥", "✨", "🎉", "🎊", "🎁",
	"☀️", "🌈", "🌧️", "⭐", "🌙", "🌸", "🍀", "🐱", "🐶", "🐼",
	"☕", "🍵", "🍺", "🍰", "🍕", "🍜", "🚀", "🎮", "💻", "📱",
	"👨‍👩‍👧‍👦", "👩‍💻", "👨‍💻", "🇹🇼", "🇯🇵", "🇺🇸", "1️⃣", "2️⃣", "3️⃣", "🏳️‍🌈",
}

var kaomoji = []string{
	"(^_^)", "(≧▽≦)", "(＾▽＾)", "(・∀・)", "(⌒▽⌒)", "(o^▽^o)",
	"(T_T)", "(;_;)", "(╥﹏╥)", "(>_<)", "(；ω；)", "(ಥ_ಥ)",
	"(・_・)", "(・・?)", "(°_°)", "(O_O)", "(￣▽￣)", "(￣ー￣)",
	"(－_－)", "(¬_¬)", "(｀Д´)", "(╯°□°)╯︵ ┻━┻", "┬─┬ノ(・_・ノ)", "¯\\_(ツ)_/¯",
	"(づ｡◕‿‿◕｡)づ", "(｡♥‿♥｡)", "(♡˙︶˙♡)", "(=^･ω･^=)", "(•ᴥ•)", "(•̀ᴗ•́)و",
}

func Contains(text string) bool {
	return slices.Contains(faces, text) || slices.Contains(kaomoji, text)
}

// Stable IDs are independent of recent ordering, including during replay.
func ID(text string) int {
	if text == "=" {
		return EqualsID
	}
	if id := slices.Index(faces, text); id >= 0 {
		return id
	}
	if id := slices.Index(kaomoji, text); id >= 0 {
		return len(faces) + id
	}
	return -1
}

// CleanRecent rejects unknown values and duplicates without sorting or storing
// arbitrary user text. Returned slices never alias the catalog or input.
func CleanRecent(recent []string) []string {
	var out []string
	for _, text := range recent {
		if Contains(text) && !slices.Contains(out, text) {
			out = append(out, text)
			if len(out) == MaxRecent {
				break
			}
		}
	}
	return out
}

func Palette(kind string, recent []string) []string {
	base := faces
	if kind == "kaomoji" {
		base = kaomoji
	}
	var out []string
	for _, text := range CleanRecent(recent) {
		if slices.Contains(base, text) {
			out = append(out, text)
		}
	}
	for _, text := range base {
		if !slices.Contains(out, text) {
			out = append(out, text)
		}
	}
	return out
}
