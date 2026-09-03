package session

import (
	"regexp"
	"strings"
	"time"
)

// Heuristic thresholds (see architecture doc §3.4 / §6.5).
const (
	SilenceForSuspect  = 5 * time.Minute
	SilenceForUnknown  = 10 * time.Minute
	SuspectDebounce    = 15 * time.Second
	StructuredFreshFor = 5 * time.Minute
)

// promptRe matches the last visible line of a session that looks like an
// interactive prompt waiting for the user.
var promptRe = regexp.MustCompile(`(?i)\(y/n\)|\[y/n\]|\(y/N\)|allow\?|\?\s*$|›\s*$`)

// MatchPrompt reports whether a (ANSI-stripped) line looks like a prompt.
func MatchPrompt(line string) bool { return promptRe.MatchString(strings.TrimRight(line, " \t")) }

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][A-Za-z0-9]|\x1b[=>78]|[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)

// StripANSI removes escape sequences and control characters.
func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// LastLine returns the last non-empty line of the buffer tail, ANSI-stripped.
func LastLine(tail []byte) string {
	clean := StripANSI(string(tail))
	clean = strings.ReplaceAll(clean, "\r\n", "\n")
	clean = strings.ReplaceAll(clean, "\r", "\n")
	lines := strings.Split(clean, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// HeurInput is the snapshot of a session the heuristic evaluates.
type HeurInput struct {
	Tool           string
	LastOutput     time.Time
	LastStructured time.Time // zero = never
	LastLine       string
}

// HeurState is the per-session heuristic memory.
type HeurState struct {
	candidateSince time.Time
	suspectAt      time.Time // non-zero while a suspect verdict is active
	unknownAt      time.Time // non-zero while an unknown verdict is active
}

// VerdictKind is what the heuristic concluded.
type VerdictKind int

const (
	VerdictNone    VerdictKind = iota
	VerdictSuspect             // raise needs_input (low confidence) with the prompt line
	VerdictCleared             // output resumed after a suspect verdict
	VerdictUnknown             // no structured signal and no output for a long time
	VerdictResumed             // output resumed after an unknown verdict
)

// Verdict is the heuristic result.
type Verdict struct {
	Kind VerdictKind
	Line string
}

// Evaluate runs the PTY heuristic. It is pure apart from st.
func Evaluate(in HeurInput, st *HeurState, now time.Time) Verdict {
	if in.Tool == "" || in.Tool == "shell" {
		return Verdict{}
	}
	silence := now.Sub(in.LastOutput)
	structFresh := !in.LastStructured.IsZero() && now.Sub(in.LastStructured) < StructuredFreshFor
	structStale := in.LastStructured.IsZero() || now.Sub(in.LastStructured) >= SilenceForUnknown

	if !st.suspectAt.IsZero() {
		if in.LastOutput.After(st.suspectAt) || structFresh {
			st.suspectAt = time.Time{}
			st.candidateSince = time.Time{}
			return Verdict{Kind: VerdictCleared}
		}
		return Verdict{}
	}
	if !st.unknownAt.IsZero() {
		if in.LastOutput.After(st.unknownAt) || structFresh {
			st.unknownAt = time.Time{}
			return Verdict{Kind: VerdictResumed}
		}
		// Fall through: an unknown session may still become a suspect.
	}
	if structFresh {
		st.candidateSince = time.Time{}
		return Verdict{}
	}
	if silence >= SilenceForSuspect && MatchPrompt(in.LastLine) {
		if st.candidateSince.IsZero() {
			st.candidateSince = now
			return Verdict{}
		}
		if now.Sub(st.candidateSince) >= SuspectDebounce {
			st.suspectAt = now
			st.unknownAt = time.Time{}
			st.candidateSince = time.Time{}
			return Verdict{Kind: VerdictSuspect, Line: in.LastLine}
		}
		return Verdict{}
	}
	st.candidateSince = time.Time{}
	if silence >= SilenceForUnknown && structStale && st.unknownAt.IsZero() {
		st.unknownAt = now
		return Verdict{Kind: VerdictUnknown}
	}
	return Verdict{}
}
