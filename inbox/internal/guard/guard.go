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

// Binaries are the executable names of an agent CLI.
var Binaries = []string{"claude", "claude-code", "codex"}

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
// by any argument of a runtime (node --require x cli.js, bun run cli.js).
// Limit: an executable path containing spaces is split at the space and
// missed; read /proc or use ps -o comm= as well if that install shows up.
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
		if a := agentSegment(f); a != "" {
			return a
		}
	}
	return ""
}

// agentSegment names the agent a path points at: an executable called
// claude or codex, the native install (claude/versions/<ver>) or an npm
// package (@anthropic-ai/claude-code, @openai/codex). A directory that merely
// contains the name (~/code/claude/tools/x) does not match.
func agentSegment(path string) string {
	segs := strings.Split(path, "/")
	if base := segs[len(segs)-1]; slices.Contains(Binaries, base) {
		return base
	}
	for i := 0; i+1 < len(segs); i++ {
		switch {
		case segs[i] == "claude" && segs[i+1] == "versions":
			return "claude"
		case segs[i] == "@anthropic-ai" && segs[i+1] == "claude-code":
			return "claude-code"
		case segs[i] == "@openai" && segs[i+1] == "codex":
			return "codex"
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
