package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestCommandExes(t *testing.T) {
	cases := []struct {
		cmd  string
		main string
		all  []string
	}{
		{"cd x && git status", "git", []string{"git"}},
		{"FOO=1 bun test", "bun", []string{"bun"}},
		{"sudo -E pacman -S go", "pacman", []string{"pacman"}},
		{"timeout 5 curl -s https://example.com", "curl", []string{"curl"}},
		{"/usr/bin/env python3 x.py", "python3", []string{"python3"}},
		{"./link-bin --check", "link-bin", []string{"link-bin"}},
		{"a | b; c", "a", []string{"a", "b", "c"}},
		{"nice -n 10 make build 2>&1 | tee log", "make", []string{"make", "tee"}},
		{"sudo -u root env A=1 B=2 ls", "ls", []string{"ls"}},
		{"echo 'a | b' && grep \"x;y\" f", "echo", []string{"echo", "grep"}},
		{"cat <<'EOF' > f\nnot a command | nor this\nEOF\ngo test ./...", "cat", []string{"cat", "go"}},
		{"ls > /dev/null 2>&1 && uv run x.py", "ls", []string{"ls", "uv"}},
		{"(cd sub && just) || exit 1", "just", []string{"just", "exit"}},
		{"git log --oneline # | not this", "git", []string{"git"}},
		{"export A=1 B=2 && make test", "make", []string{"make"}},
		{"", "", nil},
	}
	for _, c := range cases {
		main, all := commandExes(c.cmd)
		if main != c.main || !reflect.DeepEqual(all, c.all) {
			t.Errorf("%q: got %q %q, want %q %q", c.cmd, main, all, c.main, c.all)
		}
	}
}

func TestGroupKeySubcommand(t *testing.T) {
	sub := []string{"agent-manager"}
	if k := groupKey("Bash", "agent-manager", "agent-manager wait abc --timeout 5m", sub); k != "bash:agent-manager wait" {
		t.Fatal(k)
	}
	if k := groupKey("Bash", "git", "git status", sub); k != "bash:git" {
		t.Fatal(k)
	}
	if k := groupKey("Read", "", "/x", sub); k != "Read" {
		t.Fatal(k)
	}
}

// Synthetic secrets only: none of these are real credentials.
var secrets = []struct{ in, secret string }{
	{"aws s3 ls --profile x AKIAABCDEFGHIJKLMNOP", "AKIAABCDEFGHIJKLMNOP"}, // gitleaks:allow
	{"git clone https://ghp_abcdefghijklmnopqrstuvwxyz0123@github.com/x/y", "ghp_abcdefghijklmnopqrstuvwxyz0123"},
	{"echo github_pat_11ABCDEFG0123456789_abcdefghijklmnop", "github_pat_11ABCDEFG0123456789_abcdefghijklmnop"},
	{"curl -d key=sk-ant-api03-abcdefghijklmnop-qrstuv x", "sk-ant-api03-abcdefghijklmnop-qrstuv"},
	{"openai --api sk-proj0123456789abcdefXYZ", "sk-proj0123456789abcdefXYZ"},
	{"slack post xoxb-123456789012-abcdefghij", "xoxb-123456789012-abcdefghij"},
	{"curl -H 'X: y' --oauth Bearer abc.def-ghi_jkl123 x", "abc.def-ghi_jkl123"},
	{"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJlLXZhbHVl", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJlLXZhbHVl"},
	{"psql postgres://admin:hunter2pass@db.local:5432/app", "hunter2pass"},
	{"GITHUB_TOKEN=plaintoken123 gh pr list", "plaintoken123"}, // gitleaks:allow
	{"export MY_SECRET='s3cr3t value'", "s3cr3t value"},
	{"DB_PASSWORD=\"pw-123-xyz\" ./run", "pw-123-xyz"},
	{"STRIPE_KEY=rk_live_abc123 node app", "rk_live_abc123"}, // gitleaks:allow
	{"mysql --password hunter3pw -u x", "hunter3pw"},
	{"tool --password=hunter4pw", "hunter4pw"},
	{`curl -H "Authorization: Basic dXNlcjpwYXNz" https://x`, "dXNlcjpwYXNz"}, // gitleaks:allow
	{"curl -u bob:bobspassword https://x", "bobspassword"}, // gitleaks:allow
}

func TestRedact(t *testing.T) {
	for _, s := range secrets {
		got := redact(s.in)
		if strings.Contains(got, s.secret) {
			t.Errorf("%q: secret survived in %q", s.in, got)
		}
		if !strings.Contains(got, redacted) {
			t.Errorf("%q: no redaction marker in %q", s.in, got)
		}
	}
	benign := []string{
		"git status --short",
		"cd /tmp/demo && go test ./...",
		"grep -rn 'func main' scripts/",
		"curl -s https://example.com/api?page=2",
		"ls -la ~/.ssh",
		"docker run --rm -e MODE=dev image",
		"rg --sort path pattern",
	}
	for _, b := range benign {
		if got := redact(b); got != b {
			t.Errorf("benign %q changed to %q", b, got)
		}
	}
}

// Every stored column, for every tool, never holds a secret.
func TestRedactAllColumns(t *testing.T) {
	_, dbp := env(t, "")
	for i, s := range secrets {
		id := "toolu_s" + string(rune('a'+i))
		hook(t, payload(t, map[string]any{
			"tool_use_id":     id + "1",
			"tool_input":      map[string]any{"command": s.in},
			"hook_event_name": "PostToolUseFailure",
			"error":           "Exit code 1\n" + s.in,
		}))
		hook(t, payload(t, map[string]any{"tool_use_id": id + "2", "tool_name": "WebSearch",
			"tool_input": map[string]any{"query": s.in}}))
		hook(t, payload(t, map[string]any{"tool_use_id": id + "3", "tool_name": "mcp__x__y",
			"tool_input": map[string]any{"q": s.in}}))
		hook(t, payload(t, map[string]any{"tool_use_id": id + "4", "tool_name": "Agent",
			"tool_input": map[string]any{"subagent_type": "Explore", "description": s.in}}))
		hook(t, payload(t, map[string]any{"tool_use_id": id + "5", "tool_name": "WebFetch",
			"tool_input": map[string]any{"url": "https://u:" + s.secret + "@example.com/p"}}))
	}
	db := openTest(t, dbp)
	if n := count(t, db, ""); n != 5*len(secrets) {
		t.Fatalf("rows %d", n)
	}
	text := allText(t, db)
	for _, s := range secrets {
		if strings.Contains(text, s.secret) {
			t.Errorf("secret %q stored", s.secret)
		}
	}
}
