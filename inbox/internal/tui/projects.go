package tui

import (
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

// ProjectRetry is how long a task the board failed on waits before the panel
// asks again.
const ProjectRetry = 5 * time.Minute

func boardBin() string {
	if v := os.Getenv(inbox.BoardBinEnv); v != "" {
		return v
	}
	return "zen-board"
}

// projectsDue starts one off-thread board pass over the tasks without an
// answer that were not tried lately. The panel never waits on it: rows show
// their chip once a later read finds the stored answer.
func (m *Model) projectsDue() tea.Cmd {
	if m.lookup == nil || m.projBusy {
		return nil
	}
	var tasks []string
	for _, task := range m.snap.unresolved {
		if at, ok := m.projTried[task]; !ok || m.now().Sub(at) >= ProjectRetry {
			tasks = append(tasks, task)
		}
	}
	if len(tasks) == 0 {
		return nil
	}
	if m.projTried == nil {
		m.projTried = map[string]time.Time{}
	}
	for _, task := range tasks {
		m.projTried[task] = m.now()
	}
	m.projBusy = true
	ib, lookup := m.ib, m.lookup
	return func() tea.Msg {
		ib.ResolveProjects(lookup, tasks)
		return projectsMsg{}
	}
}
