package text

import "testing"

func TestValidHandle(t *testing.T) {
	valid := []string{"abc", "wodan", "x0-_z", "abcdefghijklmnop"}
	for _, h := range valid {
		if !ValidHandle(h) {
			t.Errorf("ValidHandle(%q) = false, want true", h)
		}
	}
	invalid := []string{
		"", "ab", "abcdefghijklmnopq", // length
		"Wodan", "wo dan", "wodan!", "wo.dan", // charset
		"wo\x1bdan", "wo\ndan", // control chars
		"ẘodan", // unicode lookalike
	}
	for _, h := range invalid {
		if ValidHandle(h) {
			t.Errorf("ValidHandle(%q) = true, want false", h)
		}
	}
}

func TestCleanStripsANSIInjection(t *testing.T) {
	cases := map[string]string{
		"hello":                  "hello",
		"h\x1b[31mred\x1b[0m":    "h[31mred[0m", // ESC gone, payload inert
		"\x1b]0;title\x07":       "]0;title",    // OSC title set
		"a\x9bZb":                "aZb",         // C1 CSI
		"bell\x07":               "bell",
		"back\x08space":          "backspace",
		"keep\nnewline\tand tab": "keep\nnewline\tand tab",
		"\x1b[2J\x1b[H wipe":     "[2J[H wipe", // clear-screen attempt
		"nul\x00byte":            "nulbyte",
		"del\x7fchar":            "delchar",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanLine(t *testing.T) {
	cases := map[string]string{
		"  hello  ":          "hello",
		"multi\nline\tinput": "multi line input",
		"\x1b[31mx\x1b[0m":   "[31mx[0m",
	}
	for in, want := range cases {
		if got := CleanLine(in); got != want {
			t.Errorf("CleanLine(%q) = %q, want %q", in, got, want)
		}
	}
}
