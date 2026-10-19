package english

import "strings"

// Prototype is an authored starter vocabulary for the first mixed-input slice.
// Ordering supplies engineering priors, not corpus frequency. It is deliberately
// separate from the future licensed spelling/frequency pack.
func Prototype() *Dictionary { return prototype }

var prototype = newPrototype()

func newPrototype() *Dictionary {
	var words []Word
	list := strings.Fields(`the and for with from this that have will hello world meeting receive input method english chinese test code file name time today tomorrow thanks thank please work project update settings help user users server client computer keyboard window windows system application data model language suggestion correct completion autocomplete hello meeting message email document write writing text type typing search make build release version go api python docker github redis linux macos chatgpt openai kubernetes javascript typescript function string boolean integer database network website browser terminal error request response cancel accept learning personal dictionary word words download install uninstall package path url https http foo bar`)
	for i, w := range list {
		words = append(words, Word{Text: w, Weight: float64(len(list) - i)})
	}
	return New(words)
}
