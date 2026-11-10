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

func TestAgentAncestorRefuses(t *testing.T) {
	for _, comm := range []string{"claude", "/usr/local/bin/codex"} {
		parent := tree(map[int]proc{40: {35, "-zsh"}, 35: {30, comm}})
		ok, reason := Check(env(nil), parent, 40)
		if ok || reason != fmt.Sprintf("inside %s (pid 35)", comm[strings.LastIndex(comm, "/")+1:]) {
			t.Fatalf("%s: %v %q", comm, ok, reason)
		}
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
