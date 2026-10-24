package terminal

import "unicode/utf8"

// Key is a decoded terminal key. Printable input uses Name="rune"; controls
// never become display text. Idle reads return "tick" so callers can resize.
type Key struct {
	Name string
	Rune rune
}

// decodeKey consumes one key, or waits for more bytes. final resolves a lone
// Escape/incomplete sequence after the short ambiguity timeout. Unknown control
// sequences are swallowed as one "unknown" key, never echoed to the screen.
func decodeKey(data []byte, final bool) (Key, int, bool) {
	if len(data) == 0 {
		return Key{}, 0, false
	}
	switch data[0] {
	case 3:
		return Key{Name: "ctrl-c"}, 1, true
	case 4:
		return Key{Name: "eof"}, 1, true
	case 21:
		return Key{Name: "ctrl-u"}, 1, true
	case '\r', '\n':
		return Key{Name: "enter"}, 1, true
	case '\t':
		return Key{Name: "tab"}, 1, true
	case 8, 127:
		return Key{Name: "backspace"}, 1, true
	case ' ':
		return Key{Name: "space", Rune: ' '}, 1, true
	case 27:
		if len(data) == 1 {
			if final {
				return Key{Name: "esc"}, 1, true
			}
			return Key{}, 0, false
		}
		if data[1] != '[' && data[1] != 'O' {
			return Key{Name: "unknown"}, 2, true
		}
		// CSI parameter bytes precede a final byte in the range @ through ~.
		for i := 2; i < len(data); i++ {
			if data[i] >= 0x40 && data[i] <= 0x7e {
				sequence := string(data[1 : i+1])
				name := "unknown"
				switch sequence {
				case "[A", "OA":
					name = "up"
				case "[B", "OB":
					name = "down"
				case "[C", "OC":
					name = "right"
				case "[D", "OD":
					name = "left"
				case "[H", "OH", "[1~", "[7~":
					name = "home"
				case "[F", "OF", "[4~", "[8~":
					name = "end"
				case "[5~":
					name = "pageup"
				case "[6~":
					name = "pagedown"
				case "[3~":
					name = "delete"
				case "[Z":
					name = "backtab"
				}
				return Key{Name: name}, i + 1, true
			}
		}
		if final || len(data) >= 32 {
			return Key{Name: "unknown"}, len(data), true
		}
		return Key{}, 0, false
	default:
		if data[0] < 32 {
			return Key{Name: "unknown"}, 1, true
		}
		if !utf8.FullRune(data) && !final {
			return Key{}, 0, false
		}
		r, n := utf8.DecodeRune(data)
		if r == utf8.RuneError && n == 1 {
			return Key{Name: "unknown"}, 1, true
		}
		return Key{Name: "rune", Rune: r}, n, true
	}
}
