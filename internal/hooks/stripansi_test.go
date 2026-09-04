package hooks_test

import (
	"strings"
	"testing"
)

// stripANSI backs the assertions of the manual claude tests, which only run
// with TX_REAL_CLAUDE=1; this keeps it honest in every normal test run.
func TestStripANSI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"\x1b[37m ? for shortcuts \x1b[39m", " ? for shortcuts "},
		{"\x0eqqq\x0fkeep", "qqqkeep"},
		{"\x1b(Bmid\x1b)0end", "midend"},
		// An OSC ended by ST used to swallow everything after it, which is
		// exactly what tmux emits.
		{"a\x1b]0;title\x1b\\b", "ab"},
		{"a\x1b]0;title\x07b", "ab"},
		{"a\x1bP+q544e\x1b\\b", "ab"},
		{"a\x1b[?25lb\x1b[43;1Hc", "abc"},
	}
	for _, c := range cases {
		if got := stripANSI(c.in); got != c.want {
			t.Errorf("stripANSI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := stripANSI("\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\ tail"); !strings.Contains(got, "link") {
		t.Errorf("hyperlink text lost: %q", got)
	}
}
