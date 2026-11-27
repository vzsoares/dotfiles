package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

// NoSpawnEnv, when set, keeps Detach from starting anything (tests).
const NoSpawnEnv = "ZEN_INBOX_NO_SPAWN"

// Detach starts `zen-inbox <args>` in its own session without waiting for it,
// so the board lookup never delays the command that asked for it.
func Detach(args ...string) {
	if os.Getenv(NoSpawnEnv) != "" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	c := exec.Command(self, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if c.Start() == nil {
		go func() { _ = c.Wait() }()
	}
}

func (a *App) lookup() inbox.Lookup {
	if a.Lookup != nil {
		return a.Lookup
	}
	bin := os.Getenv(inbox.BoardBinEnv)
	if bin == "" {
		bin = "zen-board"
	}
	return inbox.BoardLookup(bin)
}

// resolveLater has the board asked about task in the background; a task answered within
// the last day is left alone.
func (a *App) resolveLater(ib *inbox.Inbox, task string) {
	if a.Spawn == nil || task == "" {
		return
	}
	if done, err := ib.ProjectFresh(task); err != nil || done {
		return
	}
	a.Spawn("projects", task)
}

func (a *App) projects() *cobra.Command {
	return &cobra.Command{
		Use:   "projects [TASK]...",
		Short: "Ask the board for each task's project; none given: every open decision and note without one.",
		Long: "Stores each answer with the task so the panel can tag it. A board that fails or is slow\n" +
			"leaves the task untagged and is retried on the next run; the exit code stays 0.",
		RunE: func(_ *cobra.Command, args []string) error {
			ib, err := a.open()
			if err != nil {
				return err
			}
			defer ib.Close()
			tasks := args
			if len(tasks) == 0 {
				if tasks, err = ib.Unresolved(); err != nil {
					return err
				}
			}
			resolved, failed := ib.ResolveProjects(a.lookup(), tasks)
			a.out(fmt.Sprintf("resolved %d, failed %d", resolved, failed))
			return nil
		},
	}
}
