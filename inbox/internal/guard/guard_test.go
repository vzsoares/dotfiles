package guard

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

type proc struct {
	ppid int
	comm string
}

// tree is tmux → -zsh → zen-inbox's parent chain, extended per test.
func tree(extra map[int]proc) Parent {
	procs := map[int]proc{40: {30, "-zsh"}, 30: {20, "/opt/homebrew/bin/tmux"}, 20: {1, "launchd"}}
	for k, v := range extra {
		procs[k] = v
	}
	return func(pid int) (int, string, error) {
		p, ok := procs[pid]
		if !ok {
			return 0, "", fmt.Errorf("no pid %d", pid)
		}
		return p.ppid, p.comm, nil
	}
}

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestCleanChainPasses(t *testing.T) {
	if ok, reason := Check(env(nil), tree(nil), 40); !ok || reason != "" {
		t.Fatalf("%v %q", ok, reason)
	}
}

func TestEnvVarRefuses(t *testing.T) {
	for _, v := range EnvVars {
		ok, reason := Check(env(map[string]string{v: "1"}), tree(nil), 40)
		if ok || reason != v+" is set" {
			t.Fatalf("%s: %v %q", v, ok, reason)
		}
	}
}

func TestAgentAncestor(t *testing.T) {
	cases := []struct {
		name  string
		args  string
		agent string // empty: the chain must pass
	}{
		{"plain claude", "claude", "claude"},
		{"claude with flags", "claude --resume", "claude"},
		{"codex path", "/usr/local/bin/codex", "codex"},
		{"native versioned", "/Users/x/.local/share/claude/versions/2.1.50", "claude"},
		{"native versioned with args", "/Users/x/.local/share/claude/versions/2.1.50 --foo", "claude"},
		{"node-run", "node /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/cli.js", "claude-code"},
		{"node-run after flag", "/usr/bin/node --no-warnings /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js", "claude-code"},
		{"login shell", "-zsh", ""},
		{"tmux", "tmux", ""},
		{"coincidental name", "/usr/bin/claude-notes-viewer", ""},
		{"agent name as file argument", "vim claude.txt", ""},
		{"agent dir as plain argument", "vim /home/x/claude/notes.md", ""},
		{"node running unrelated script", "node /srv/app/server.js", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parent := tree(map[int]proc{40: {35, "-zsh"}, 35: {30, c.args}})
			ok, reason := Check(env(nil), parent, 40)
			if c.agent == "" {
				if !ok || reason != "" {
					t.Fatalf("%v %q", ok, reason)
				}
				return
			}
			if want := fmt.Sprintf("inside %s (pid 35)", c.agent); ok || reason != want {
				t.Fatalf("%v %q, want %q", ok, reason, want)
			}
		})
	}
}

func TestAgentPIDRefuses(t *testing.T) {
	ok, reason := Check(env(map[string]string{"AGENT_MANAGER_AGENT_PID": "30"}), tree(nil), 40)
	if ok || reason == "" {
		t.Fatalf("%v %q", ok, reason)
	}
}

func TestLookupErrorRefuses(t *testing.T) {
	broken := func(int) (int, string, error) { return 0, "", errors.New("ps: exit 1") }
	if ok, reason := Check(env(nil), broken, 40); ok || !strings.Contains(reason, "unreadable") {
		t.Fatalf("%v %q", ok, reason)
	}
	if ok, _ := Check(env(nil), tree(map[int]proc{20: {7, "launchd"}}), 40); ok {
		t.Fatal("a missing ancestor passed")
	}
}

func TestCycleRefuses(t *testing.T) {
	loop := tree(map[int]proc{20: {40, "launchd"}})
	if ok, reason := Check(env(nil), loop, 40); ok || !strings.Contains(reason, "deeper") {
		t.Fatalf("%v %q", ok, reason)
	}
}

func TestPSReadsThisProcess(t *testing.T) {
	ppid, comm, err := ps(os.Getpid())
	if err != nil || ppid != os.Getppid() || comm == "" {
		t.Fatalf("%d %q %v", ppid, comm, err)
	}
}
