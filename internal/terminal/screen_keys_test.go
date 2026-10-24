package terminal

import "testing"

func TestScreenKeyDecoder(t *testing.T) {
	cases := []struct {
		input, name string
		character   rune
	}{
		{"\x1b[A", "up", 0}, {"\x1bOB", "down", 0}, {"\x1b[C", "right", 0}, {"\x1b[D", "left", 0},
		{"\x1b[5~", "pageup", 0}, {"\x1b[6~", "pagedown", 0}, {"\x1b[H", "home", 0}, {"\x1b[4~", "end", 0},
		{"\x1b[3~", "delete", 0}, {"\x1b[Z", "backtab", 0}, {"\r", "enter", 0}, {"\n", "enter", 0},
		{"\t", "tab", 0}, {"\x7f", "backspace", 0}, {"\x08", "backspace", 0}, {"\x03", "ctrl-c", 0},
		{"\x04", "eof", 0}, {"\x15", "ctrl-u", 0}, {" ", "space", ' '}, {"j", "rune", 'j'}, {"7", "rune", '7'}, {"ש", "rune", 'ש'},
		{"📚", "rune", '📚'}, {"\x1b", "esc", 0}, {"\x1b[99~", "unknown", 0}, {"\x00", "unknown", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name+tc.input, func(t *testing.T) {
			key, n, complete := decodeKey([]byte(tc.input), true)
			if !complete || n != len(tc.input) || key.Name != tc.name || key.Rune != tc.character {
				t.Fatalf("got %#v, %d, %t", key, n, complete)
			}
		})
	}
}
func TestEscapeAndUnicodeWaitForTheirWholeSequence(t *testing.T) {
	for _, value := range []string{"\x1b[A", "\x1b[6~", "📚"} {
		bytes := []byte(value)
		for n := 1; n < len(bytes); n++ {
			if _, _, ready := decodeKey(bytes[:n], false); ready {
				t.Fatalf("decoded incomplete %q", bytes[:n])
			}
		}
	}
	if key, n, ready := decodeKey([]byte("\x1b"), true); !ready || key.Name != "esc" || n != 1 {
		t.Fatal(key, n, ready)
	}
}
func TestUnknownControlsAreConsumedWithoutBecomingText(t *testing.T) {
	key, n, ready := decodeKey([]byte("\x1b[200~hello"), false)
	if !ready || key.Name != "unknown" || key.Rune != 0 || n != 6 {
		t.Fatal(key, n, ready)
	}
	if key, n, ready = decodeKey([]byte{0xff}, true); !ready || key.Name != "unknown" || n != 1 {
		t.Fatal(key, n, ready)
	}
	key, n, ready = decodeKey([]byte("\x1b[1234567890123456789012345678901234567890"), false)
	if !ready || key.Name != "unknown" || n < 32 {
		t.Fatal(key, n, ready)
	}
}
