package session

import (
	"testing"
	"time"
)

func TestMatchPrompt(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"Continue? (y/n)", true},
		{"Overwrite file [Y/N]", true},
		{"Proceed (y/N) ", true},
		{"Do you want to allow? ", true},
		{"Which branch should I use?", true},
		{"› ", true},
		{"$ ", false},
		{"Compiling package main", false},
		{"? for shortcuts", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := MatchPrompt(tc.line); got != tc.want {
			t.Errorf("MatchPrompt(%q)=%v want %v", tc.line, got, tc.want)
		}
	}
}

func TestLastLine(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"hello\r\nworld\r\n", "world"},
		{"a\n\x1b[32mgreen? \x1b[0m\n\n", "green?"},
		{"\x1b]0;title\x07x\r\n  › \x1b[?25h", "›"},
		{"", ""},
		{"\n\n", ""},
	}
	for _, tc := range tests {
		if got := LastLine([]byte(tc.in)); got != tc.want {
			t.Errorf("LastLine(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestEvaluate(t *testing.T) {
	t0 := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	type step struct {
		at   time.Duration
		in   HeurInput
		want VerdictKind
	}
	base := HeurInput{Tool: "grok", LastOutput: t0, LastLine: "Apply changes? (y/n)"}
	tests := []struct {
		name  string
		steps []step
	}{
		{"shell never", []step{{20 * time.Minute, HeurInput{Tool: "shell", LastOutput: t0, LastLine: "(y/n)"}, VerdictNone}}},
		{"fresh structured signal suppresses", []step{
			{6 * time.Minute, HeurInput{Tool: "claude", LastOutput: t0, LastStructured: t0.Add(3 * time.Minute), LastLine: "(y/n)"}, VerdictNone},
			{7 * time.Minute, HeurInput{Tool: "claude", LastOutput: t0, LastStructured: t0.Add(3 * time.Minute), LastLine: "(y/n)"}, VerdictNone},
		}},
		{"suspect after debounce then cleared", []step{
			{4 * time.Minute, base, VerdictNone},                // silence too short
			{5 * time.Minute, base, VerdictNone},                // candidate
			{5*time.Minute + 10*time.Second, base, VerdictNone}, // still debouncing
			{5*time.Minute + 16*time.Second, base, VerdictSuspect},
			{6 * time.Minute, base, VerdictNone}, // no repeat
			{7 * time.Minute, HeurInput{Tool: "grok", LastOutput: t0.Add(6*time.Minute + 30*time.Second), LastLine: "working"}, VerdictCleared},
		}},
		{"unknown when silent without prompt, resumed on output", []step{
			{9 * time.Minute, HeurInput{Tool: "codex", LastOutput: t0, LastLine: "thinking"}, VerdictNone},
			{10 * time.Minute, HeurInput{Tool: "codex", LastOutput: t0, LastLine: "thinking"}, VerdictUnknown},
			{11 * time.Minute, HeurInput{Tool: "codex", LastOutput: t0, LastLine: "thinking"}, VerdictNone},
			{12 * time.Minute, HeurInput{Tool: "codex", LastOutput: t0.Add(11*time.Minute + 30*time.Second), LastLine: "x"}, VerdictResumed},
		}},
		{"prompt wins over unknown", []step{
			{10 * time.Minute, base, VerdictNone}, // candidate first (prompt matched), not unknown
			{10*time.Minute + 15*time.Second, base, VerdictSuspect},
		}},
		{"candidate resets when prompt disappears", []step{
			{5 * time.Minute, base, VerdictNone},
			{5*time.Minute + 5*time.Second, HeurInput{Tool: "grok", LastOutput: t0, LastLine: "plain"}, VerdictNone},
			{5*time.Minute + 20*time.Second, base, VerdictNone}, // new candidate
			{5*time.Minute + 36*time.Second, base, VerdictSuspect},
		}},
	}
	for _, tc := range tests {
		var st HeurState
		for i, s := range tc.steps {
			got := Evaluate(s.in, &st, t0.Add(s.at))
			if got.Kind != s.want {
				t.Errorf("%s step %d (@%v): got %v want %v", tc.name, i, s.at, got.Kind, s.want)
			}
			if got.Kind == VerdictSuspect && got.Line != s.in.LastLine {
				t.Errorf("%s step %d: line %q", tc.name, i, got.Line)
			}
		}
	}
}
