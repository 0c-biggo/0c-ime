package datadir

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/internal/englishpack"
)

func toyWithEnglish(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"lexicon.tsv", "lm.tsv"} {
		b, err := os.ReadFile(filepath.Join("../../testdata/toy", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := englishpack.Compile("../../data/english", filepath.Join(dir, "english")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFullEnglishPackMixedInputAndValidSpellings(t *testing.T) {
	dir := toyWithEnglish(t)
	e, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e.English.Len() < 80000 || e.EnglishPack == "" {
		t.Fatal("full English pack not loaded")
	}
	for _, word := range []string{"color", "colour", "organize", "organise", "notification", "configuration", "zoology"} {
		if !e.English.Has(word) || len(e.English.Corrections(word, 3)) > 0 {
			t.Fatalf("valid spelling %s unrecognized", word)
		}
	}
	e.MixedInput = true
	for _, tc := range []struct{ keys, want string }{{"ji3hellot ", "我hello吃"}, {"helloji3", "hello我"}, {"aji3", "a我"}, {"Iji3", "I我"}, {"a ", "a "}, {"I ", "I "}, {"t ", "吃"}, {"https://example.com/a?q=1", "https://example.com/a?q=1"}, {"foo_bar123", "foo_bar123"}} {
		id := e.OpenSession()
		for _, k := range tc.keys {
			if _, err := e.Do(id, proto.Request{Type: proto.Key, Key: string(k)}); err != nil {
				t.Fatal(err)
			}
		}
		snap, err := e.Do(id, proto.Request{Type: proto.Commit})
		if err != nil || snap.Commit != tc.want {
			t.Fatalf("%q => %+v, %v; want %s", tc.keys, snap, err, tc.want)
		}
		e.CloseSession(id)
	}
	for _, tc := range []struct{ typed, want string }{{"recieve", "receive"}, {"configuraton", "configuration"}, {"notifcation", "notification"}, {"zoolgoy", "zoology"}} {
		got := e.English.Corrections(tc.typed, 3)
		if len(got) == 0 || got[0].Text != tc.want {
			t.Fatalf("%s: %+v", tc.typed, got)
		}
		id := e.OpenSession()
		for _, k := range tc.typed {
			e.Do(id, proto.Request{Type: proto.Key, Key: string(k)})
		}
		accepted, _ := e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyTab})
		if accepted.Text() != tc.want {
			t.Fatalf("full pack suggestion: %+v", accepted)
		}
		restored, _ := e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyBackspace})
		if restored.Text() != tc.typed {
			t.Fatalf("full pack undo: %+v", restored)
		}
		committed, _ := e.Do(id, proto.Request{Type: proto.Commit})
		if committed.Commit != tc.typed {
			t.Fatalf("raw spelling lost: %+v", committed)
		}
		if e.Model.Store.Uni[tc.typed] != 0 || e.Model.Store.Uni[tc.want] != 0 {
			t.Fatal("English contaminated Chinese learning")
		}
		e.CloseSession(id)
	}
	if e.MixedInput = false; e.Session(e.OpenSession()).MixedInput {
		t.Fatal("off switch ignored with full pack")
	}
}

func TestFullEnglishPackKeepsFirstToneChinese(t *testing.T) {
	e, err := Open(toyWithEnglish(t))
	if err != nil {
		t.Fatal(err)
	}
	if !e.English.Has("up") {
		t.Fatal("regression requires the shipped English word up")
	}
	for _, mixed := range []bool{false, true} {
		e.MixedInput = mixed
		id := e.OpenSession()
		do := func(r proto.Request) proto.Snapshot {
			t.Helper()
			snap, err := e.Do(id, r)
			if err != nil {
				t.Fatal(err)
			}
			return snap
		}
		do(proto.Request{Type: proto.Key, Key: "u"})
		if snap := do(proto.Request{Type: proto.Key, Key: "p"}); snap.Text() != "ㄧㄣ" || snap.SuggestionEdit != nil {
			t.Fatalf("mixed=%v pending: %+v", mixed, snap)
		}
		do(proto.Request{Type: proto.Key, Key: " "})
		snap := do(proto.Request{Type: proto.Candidates, Length: 1})
		found := false
		if snap.Candidates != nil {
			for _, item := range snap.Candidates.Items {
				if item.Text == "音" {
					found = true
					do(proto.Request{Type: proto.Select, Index: item.ID})
					break
				}
			}
		}
		if !found {
			t.Fatalf("mixed=%v missing 音: %+v", mixed, snap)
		}
		if snap = do(proto.Request{Type: proto.Commit}); snap.Commit != "音" {
			t.Fatalf("mixed=%v commit: %+v", mixed, snap)
		}
		// A user-selected English spelling still resolves the identical keys.
		if mixed {
			do(proto.Request{Type: proto.Key, Key: "u"})
			do(proto.Request{Type: proto.Key, Key: "p"})
			snap = do(proto.Request{Type: proto.Candidates})
			if snap.Candidates == nil || snap.Candidates.Items[0].Text != "up" {
				t.Fatalf("missing raw English: %+v", snap)
			}
			do(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[0].ID})
			do(proto.Request{Type: proto.Key, Key: " "})
			if snap = do(proto.Request{Type: proto.Commit}); snap.Commit != "up " {
				t.Fatalf("explicit English commit: %+v", snap)
			}
		}
		e.CloseSession(id)
	}
}

func TestFullEnglishPackEnglishModeTabCompletionAndCorrection(t *testing.T) {
	e, err := Open(toyWithEnglish(t))
	if err != nil {
		t.Fatal(err)
	}
	e.EnglishAutocomplete = true
	e.MixedInput = true
	for _, tc := range []struct{ typed, want string }{
		{"hel", "hello"}, {"recieve", "receive"}, {"teh", "the"},
		{"configuraton", "configuration"}, {"notifcation", "notification"},
		{"RECIEVE", "RECEIVE"}, {"Recieve", "Receive"},
	} {
		t.Run(tc.typed, func(t *testing.T) {
			id := e.OpenSession()
			defer e.CloseSession(id)
			do := func(r proto.Request) proto.Snapshot {
				t.Helper()
				snap, err := e.Do(id, r)
				if err != nil {
					t.Fatal(err)
				}
				return snap
			}
			do(proto.Request{Type: proto.Mode, On: true})
			var snap proto.Snapshot
			for _, k := range tc.typed {
				snap = do(proto.Request{Type: proto.Key, Key: string(k)})
			}
			if snap.Text() != tc.typed || snap.Candidates == nil || len(snap.Candidates.Items) > 6 {
				t.Fatalf("raw word or bounded candidates lost: %+v", snap)
			}
			// Some real-pack prefixes have valid longer completions. Move to the
			// correction with the same arrow-key protocol used by platform shells.
			index := -1
			for i, c := range snap.Candidates.Items {
				if c.Text == tc.want {
					index = i
					break
				}
			}
			if index < 0 {
				t.Fatalf("shipped pack has no requested suggestion %q: %+v", tc.want, snap.Candidates)
			}
			for range index {
				do(proto.Request{Type: proto.Key, Key: proto.KeyDown})
			}
			if snap = do(proto.Request{Type: proto.Key, Key: proto.KeyTab}); snap.Text() != tc.want || !snap.Handled {
				t.Fatalf("Tab acceptance: %+v", snap)
			}
			if snap = do(proto.Request{Type: proto.Key, Key: proto.KeyEscape}); snap.Text() != tc.typed {
				t.Fatalf("Escape undo: %+v", snap)
			}
			if snap = do(proto.Request{Type: proto.Key, Key: proto.KeyEnter}); snap.Commit != tc.typed || !snap.Handled {
				t.Fatalf("literal commit; Enter stays in the IME (#235): %+v", snap)
			}
		})
	}
}

// #59: a complete English word keeps its space even when its last keys spell
// a first-tone syllable after a shorter English word (hell+o → ㄟ).
func TestFullEnglishPackKeepsCompleteWordSpace(t *testing.T) {
	dir := toyWithEnglish(t)
	f, err := os.OpenFile(filepath.Join(dir, "lexicon.tsv"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"欸\tㄟ\t1", "婀\tㄜ\t1", "斯\tㄙ\t1", "一\tㄧ\t1", "窩\tㄨㄛ\t1"} {
		if _, err = f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	e, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	e.MixedInput = true
	for _, tc := range []struct{ keys, text, want string }{
		{"hello ", "hello ", "hello "}, {"thank you ", "thank you ", "thank you "},
		{"open the door ", "open the door ", "open the door "}, {"see you ", "see you ", "see you "},
		{"up ", "因", "因"}, {"helloji ", "hello窩", "hello窩"}, {"ji3helloji3", "我hello我", "我hello我"},
	} {
		t.Run(tc.keys, func(t *testing.T) {
			id := e.OpenSession()
			defer e.CloseSession(id)
			var snap proto.Snapshot
			for _, k := range tc.keys {
				if snap, err = e.Do(id, proto.Request{Type: proto.Key, Key: string(k)}); err != nil {
					t.Fatal(err)
				}
			}
			if snap.Text() != tc.text {
				t.Fatalf("composition: %q want %q", snap.Text(), tc.text)
			}
			if snap, err = e.Do(id, proto.Request{Type: proto.Key, Key: proto.KeyEnter}); err != nil || snap.Commit != tc.want {
				t.Fatalf("Enter: %+v, %v want %q", snap, err, tc.want)
			}
		})
	}
}

func TestEnglishPackIntegrityAndSealedLoading(t *testing.T) {
	dir := toyWithEnglish(t)
	manifest, _ := os.ReadFile(filepath.Join(dir, "english/manifest.json"))
	plain, _ := os.ReadFile(filepath.Join(dir, "english/words.bin"))
	sealed, err := Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "english/words.bin"), sealed, 0600)
	e, err := Open(dir)
	if err != nil || !e.English.Has("configuration") {
		t.Fatalf("sealed English load: %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "english/manifest.json")); !bytes.Equal(manifest, after) {
		t.Fatal("English manifest changed at runtime")
	}
	sealed[len(sealed)-1] ^= 1
	os.WriteFile(filepath.Join(dir, "english/words.bin"), sealed, 0600)
	if _, err := Open(dir); err == nil {
		t.Fatal("corrupt English pack silently accepted")
	}
	os.RemoveAll(filepath.Join(dir, "english"))
	e, err = Open(dir)
	if err != nil || e.English.Len() != 104 || e.EnglishPack != "" {
		t.Fatalf("legacy pack fallback: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "english"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatalf("empty directory after rollback must be compatible: %v", err)
	}
}
