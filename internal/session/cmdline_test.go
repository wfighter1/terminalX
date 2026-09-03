package session

import "testing"

func TestToolCommand(t *testing.T) {
	tests := []struct {
		name   string
		spec   ToolSpec
		resume bool
		want   string
	}{
		{"shell", ToolSpec{Tool: "shell"}, false, ""},
		{"claude basic", ToolSpec{Tool: "claude", Name: "tx", PermissionMode: "acceptEdits", SettingsPath: "/h/s.json", ShellKind: "sh"}, false,
			"claude --name tx --permission-mode acceptEdits --settings /h/s.json"},
		{"claude resume open", ToolSpec{Tool: "claude", Resume: "abc", ShellKind: "sh"}, false, "claude --resume abc"},
		{"claude continue open", ToolSpec{Tool: "claude", Resume: "last", ShellKind: "sh"}, false, "claude --continue"},
		{"claude kill resume named", ToolSpec{Tool: "claude", Name: "my sess", ShellKind: "sh"}, true, "claude --name 'my sess' --resume 'my sess'"},
		{"claude kill resume unnamed", ToolSpec{Tool: "claude", ShellKind: "sh"}, true, "claude --continue"},
		{"claude pwsh quoting", ToolSpec{Tool: "claude", SettingsPath: `C:\Users\x y\s.json`, ShellKind: "pwsh"}, false, `claude --settings 'C:\Users\x y\s.json'`},
		{"claude cmd quoting", ToolSpec{Tool: "claude", SettingsPath: `C:\Users\x y\s.json`, ShellKind: "cmd"}, false, `claude --settings "C:\Users\x y\s.json"`},
		{"codex notify sh", ToolSpec{Tool: "codex", CodexNotify: []string{"/opt/tx-agent", "notify", "--sid", "5"}, ShellKind: "sh"}, false,
			`codex -c 'notify=["/opt/tx-agent","notify","--sid","5"]'`},
		{"codex notify pwsh backslashes", ToolSpec{Tool: "codex", CodexNotify: []string{`C:\tx\tx-agent.exe`, "notify"}, ShellKind: "pwsh"}, false,
			`codex -c 'notify=["C:\\tx\\tx-agent.exe","notify"]'`},
		{"codex kill resume", ToolSpec{Tool: "codex", ShellKind: "sh"}, true, "codex resume --last"},
		{"codex resume id", ToolSpec{Tool: "codex", Resume: "th-1", ShellKind: "sh"}, false, "codex resume th-1"},
		{"grok extra", ToolSpec{Tool: "grok", Extra: []string{"--model", "a b"}, ShellKind: "sh"}, false, "grok --model 'a b'"},
	}
	for _, tc := range tests {
		if got := tc.spec.Command(tc.resume); got != tc.want {
			t.Errorf("%s:\n got  %s\n want %s", tc.name, got, tc.want)
		}
	}
}

func TestQuoteArg(t *testing.T) {
	tests := []struct{ kind, in, want string }{
		{"sh", "plain", "plain"},
		{"sh", "it's", `'it'\''s'`},
		{"pwsh", "it's", "'it''s'"},
		{"cmd", `say "hi"`, `"say \"hi\""`},
		{"sh", "", "''"},
	}
	for _, tc := range tests {
		if got := QuoteArg(tc.kind, tc.in); got != tc.want {
			t.Errorf("QuoteArg(%s,%q)=%s want %s", tc.kind, tc.in, got, tc.want)
		}
	}
}
