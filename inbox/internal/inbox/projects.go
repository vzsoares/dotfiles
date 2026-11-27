package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// schemaV16 keeps each task's board project tag. A row with an empty project
// means the board was asked and the task has none. Additive: a v15 reader
// never names the table.
const schemaV16 = `
CREATE TABLE task_projects (
    task TEXT PRIMARY KEY,
    project TEXT NOT NULL DEFAULT '',
    checked_at TEXT NOT NULL
);
`

// ProjectTTL is how long a stored answer stands before the board is asked
// again, so a task moved to another project, or given one later, is picked up.
const ProjectTTL = 24 * time.Hour

// BoardBinEnv names the command that answers `project <task> --json`.
const BoardBinEnv = "ZEN_BOARD_BIN"

// boardNotFound is zen-board's exit for a task or reference it cannot find: a
// permanent answer, stored as no project.
const boardNotFound = 2

// LookupTimeout bounds one board lookup.
const LookupTimeout = 8 * time.Second

// Lookup returns a task's project tag, "" when the task has none. An error
// means the board could not be asked; nothing is stored then.
type Lookup func(task string) (string, error)

// BoardLookup asks `zen-board project <task> --json`.
func BoardLookup(bin string) Lookup {
	return func(task string) (string, error) {
		c, cancel := context.WithTimeout(context.Background(), LookupTimeout)
		defer cancel()
		out, err := exec.CommandContext(c, bin, "project", task, "--json").Output()
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == boardNotFound {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		var got struct {
			Tag string `json:"tag"`
		}
		if err := json.Unmarshal(out, &got); err != nil {
			return "", err
		}
		return strings.TrimSpace(got.Tag), nil
	}
}

// SetProject stores a task's project tag.
func (ib *Inbox) SetProject(task, tag string) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return dbErr(ib.exec("INSERT INTO task_projects (task, project, checked_at) VALUES (?, ?, ?)"+
		" ON CONFLICT(task) DO UPDATE SET project = excluded.project, checked_at = excluded.checked_at",
		task, tag, stamp()))
}

// Projects maps each task to its project tag, leaving out tasks with none.
func (ib *Inbox) Projects() (map[string]string, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	rows, err := ib.conn.QueryContext(ctx, "SELECT task, project FROM task_projects WHERE project != ''")
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var task, tag string
		if err := rows.Scan(&task, &tag); err != nil {
			return nil, dbErr(err)
		}
		out[task] = tag
	}
	return out, dbErr(rows.Err())
}

// Unresolved are the tasks of open decisions and visible notes the board has
// not been asked about, or not for ProjectTTL, in task order. Until a stale
// task is answered again its stored tag keeps showing.
func (ib *Inbox) Unresolved() ([]string, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	rows, err := ib.conn.QueryContext(ctx, "SELECT task FROM ("+
		"SELECT task FROM decisions WHERE task != '' AND status = 'open'"+
		" UNION SELECT task FROM notes WHERE task != '' AND "+visibleNotes+") "+
		"WHERE task NOT IN (SELECT task FROM task_projects WHERE checked_at >= ?) ORDER BY task",
		Stamp(Now().Add(-VisibleFor)), Stamp(Now().Add(-ProjectTTL)))
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var task string
		if err := rows.Scan(&task); err != nil {
			return nil, dbErr(err)
		}
		out = append(out, task)
	}
	return out, dbErr(rows.Err())
}

// ProjectFresh is whether the board answered for task within ProjectTTL.
func (ib *Inbox) ProjectFresh(task string) (bool, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var n int
	err := ib.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM task_projects WHERE task = ? AND checked_at >= ?",
		task, Stamp(Now().Add(-ProjectTTL))).Scan(&n)
	return n > 0, dbErr(err)
}

// ResolveProjects asks the board about each task without a stored answer and
// stores what it says. A failed lookup is skipped and counted, never raised:
// the board being down must not touch an ask or a note.
func (ib *Inbox) ResolveProjects(lookup Lookup, tasks []string) (resolved, failed int) {
	for _, task := range tasks {
		tag, err := lookup(task)
		if err != nil {
			failed++
			continue
		}
		if err := ib.SetProject(task, tag); err != nil {
			failed++
			continue
		}
		resolved++
	}
	return resolved, failed
}
