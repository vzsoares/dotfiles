// Package guard decides whether the panel runs outside every agent session,
// the only place a prod-action ask may be answered.
package guard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// EnvVars mark a process started by agent-manager or Claude Code.
var EnvVars = []string{"AGENT_MANAGER_SESSION_ID", "AGENT_MANAGER_AGENT_PID", "CLAUDECODE"}

// Agents are the process names (comm basename) of agent CLIs.
var Agents = []string{"claude", "codex"}

// maxDepth stops a walk that never reaches pid 1.
const maxDepth = 256

// Parent returns pid's parent pid and its own command name.
type Parent func(pid int) (ppid int, comm string, err error)

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
		ppid, comm, err := parent(pid)
		if err != nil {
			return false, fmt.Sprintf("process tree unreadable at pid %d: %v", pid, err)
		}
		name := filepath.Base(strings.TrimPrefix(comm, "-"))
		for _, a := range Agents {
			if name == a {
				return false, fmt.Sprintf("inside %s (pid %d)", a, pid)
			}
		}
		pid = ppid
	}
	return false, "process tree deeper than " + strconv.Itoa(maxDepth)
}

// Current checks this process's env and ancestors with ps.
func Current() (bool, string) {
	return Check(os.Getenv, ps, os.Getppid())
}

// ps reads one process with flags shared by macOS and Linux ps.
func ps(pid int) (int, string, error) {
	out, err := exec.Command("ps", "-o", "ppid=,comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, "", err
	}
	ppidText, comm, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	ppid, err := strconv.Atoi(ppidText)
	if err != nil {
		return 0, "", fmt.Errorf("ps output %q: %w", out, err)
	}
	return ppid, strings.TrimSpace(comm), nil
}
