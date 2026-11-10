// Package guard decides whether the panel runs outside every agent session,
// the only place a prod-action ask may be answered.
package guard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// EnvVars mark a process started by agent-manager or Claude Code.
var EnvVars = []string{"AGENT_MANAGER_SESSION_ID", "AGENT_MANAGER_AGENT_PID", "CLAUDECODE"}

// Agents are the path segments that name an agent CLI: a binary
// (claude, codex), a native install dir (claude/versions/<ver>) or an
// npm package (@anthropic-ai/claude-code).
var Agents = []string{"claude", "claude-code", "codex"}

// runtimes execute a script whose path names the agent.
var runtimes = []string{"node", "bun", "deno"}

// maxDepth stops a walk that never reaches pid 1.
const maxDepth = 256

// Parent returns pid's parent pid and its full command line.
type Parent func(pid int) (ppid int, args string, err error)

// Check refuses when an agent env var is set or an ancestor of pid is an
// agent; it fails closed, so an unreadable process tree refuses too.
func Check(getenv func(string) string, parent Parent, pid int) (ok bool, reason string) {
	for _, v := range EnvVars {
		if getenv(v) != "" {
			return false, v + " is set"
		}
	}
	return walk(parent, pid)
}

func walk(parent Parent, pid int) (bool, string) {
	for range maxDepth {
		if pid <= 1 {
			return true, ""
		}
		ppid, args, err := parent(pid)
		if err != nil {
			return false, fmt.Sprintf("process tree unreadable at pid %d: %v", pid, err)
		}
		if a := agentIn(args); a != "" {
			return false, fmt.Sprintf("inside %s (pid %d)", a, pid)
		}
		pid = ppid
	}
	return false, "process tree deeper than " + strconv.Itoa(maxDepth)
}

// agentIn returns the agent named by a command line's executable path, or
// by the script a runtime executes; segments must equal a name, not contain it.
// Paths containing spaces are split at the space.
func agentIn(args string) string {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return ""
	}
	exe := strings.TrimPrefix(fields[0], "-")
	if a := agentSegment(exe); a != "" {
		return a
	}
	if !slices.Contains(runtimes, filepath.Base(exe)) {
		return ""
	}
	for _, f := range fields[1:] {
		if !strings.HasPrefix(f, "-") {
			return agentSegment(f)
		}
	}
	return ""
}

func agentSegment(path string) string {
	for _, seg := range strings.Split(path, "/") {
		if slices.Contains(Agents, seg) {
			return seg
		}
	}
	return ""
}

// Current checks this process's env and ancestors with ps.
func Current() (bool, string) {
	return Check(os.Getenv, ps, os.Getppid())
}

// ps reads one process (ppid, full command line) with flags shared by macOS and Linux ps.
func ps(pid int) (int, string, error) {
	out, err := exec.Command("ps", "-o", "ppid=,args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, "", err
	}
	ppidText, args, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	ppid, err := strconv.Atoi(ppidText)
	if err != nil {
		return 0, "", fmt.Errorf("ps output %q: %w", out, err)
	}
	return ppid, strings.TrimSpace(args), nil
}
