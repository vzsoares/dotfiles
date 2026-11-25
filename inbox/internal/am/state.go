package am

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// StateEnv overrides where agent-manager's state.db is read from.
const StateEnv = "ZEN_AM_STATE_DB"

// StatePath is $ZEN_AM_STATE_DB, else agent-manager's state.db in the user
// config dir.
func StatePath() string {
	if v := os.Getenv(StateEnv); v != "" {
		return v
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "agent-manager", "state.db")
}

// Unavailable is why agent-manager's state.db cannot be read as expected.
type Unavailable struct{ Why string }

func (u *Unavailable) Error() string { return "unavailable: " + u.Why }

// stateColumns are the columns the reader needs, per table.
var stateColumns = map[string][]string{
	"session_inbox": {"id", "session_id", "sender_id", "sender_name", "body", "sent_at", "delivered_at", "dropped_at"},
	"sessions": {"id", "name", "tool", "cwd", "status", "archived", "agent_session_id",
		"retired_agent_session_id", "released_agent_session_id"},
}

// State is a read-only handle on agent-manager's state.db; it is
// agent-manager's internal schema and is never written.
type State struct{ db *sql.DB }

// InboxRow is one session_inbox row; a zero time is an unset column.
type InboxRow struct {
	ID                                    int64
	SessionID, SenderID, SenderName, Body string
	SentAt, DeliveredAt, DroppedAt        time.Time
}

// StateSession is one sessions row.
type StateSession struct {
	ID, Name, Tool, Cwd, Status string
	Archived                    bool
	// AgentIDs are the current, retired and released agent session ids.
	AgentIDs []string
}

// OpenState opens path read-only and checks the columns it reads; any
// problem is an *Unavailable.
func OpenState(path string) (*State, error) {
	if path == "" {
		return nil, &Unavailable{"no user config dir for agent-manager's state.db"}
	}
	if _, err := os.Stat(path); err != nil {
		return nil, &Unavailable{"no agent-manager state.db at " + path}
	}
	dsn := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, &Unavailable{err.Error()}
	}
	db.SetMaxOpenConns(1)
	st := &State{db: db}
	if err := st.check(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return st, nil
}

func (s *State) check() error {
	for _, table := range []string{"session_inbox", "sessions"} {
		rows, err := s.db.Query("SELECT name FROM pragma_table_info(?)", table)
		if err != nil {
			return &Unavailable{err.Error()}
		}
		have := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				_ = rows.Close()
				return &Unavailable{err.Error()}
			}
			have[name] = true
		}
		if err := errors.Join(rows.Close(), rows.Err()); err != nil {
			return &Unavailable{err.Error()}
		}
		if len(have) == 0 {
			return &Unavailable{"state.db has no " + table + " table"}
		}
		for _, col := range stateColumns[table] {
			if !have[col] {
				return &Unavailable{fmt.Sprintf("state.db %s has no %s column", table, col)}
			}
		}
	}
	return nil
}

func (s *State) Close() error { return s.db.Close() }

// stamp reads an agent-manager time column, whatever its unit.
func stamp(n int64) time.Time {
	switch {
	case n <= 0:
		return time.Time{}
	case n < 1e11:
		return time.Unix(n, 0).UTC()
	case n < 1e14:
		return time.UnixMilli(n).UTC()
	case n < 1e17:
		return time.UnixMicro(n).UTC()
	}
	return time.Unix(0, n).UTC()
}

const inboxSelect = "SELECT id, session_id, sender_id, sender_name, body, sent_at, delivered_at, dropped_at FROM session_inbox"

func (s *State) inbox(where string, args ...any) ([]InboxRow, error) {
	rows, err := s.db.Query(inboxSelect+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboxRow
	for rows.Next() {
		var r InboxRow
		var sent, delivered, dropped int64
		if err := rows.Scan(&r.ID, &r.SessionID, &r.SenderID, &r.SenderName, &r.Body, &sent, &delivered, &dropped); err != nil {
			return nil, err
		}
		r.SentAt, r.DeliveredAt, r.DroppedAt = stamp(sent), stamp(delivered), stamp(dropped)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Rows are every session_inbox row, oldest first.
func (s *State) Rows() ([]InboxRow, error) { return s.inbox("ORDER BY id") }

// Row is the session_inbox row with that id.
func (s *State) Row(id int64) (InboxRow, bool, error) {
	rows, err := s.inbox("WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return InboxRow{}, false, err
	}
	return rows[0], true, nil
}

// Sessions are agent-manager's sessions by id, archived ones included.
func (s *State) Sessions() (map[string]StateSession, error) {
	rows, err := s.db.Query("SELECT id, name, tool, cwd, status, archived, agent_session_id," +
		" retired_agent_session_id, released_agent_session_id FROM sessions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]StateSession{}
	for rows.Next() {
		var ss StateSession
		var archived int64
		var cur, retired, released string
		if err := rows.Scan(&ss.ID, &ss.Name, &ss.Tool, &ss.Cwd, &ss.Status, &archived, &cur, &retired, &released); err != nil {
			return nil, err
		}
		ss.Archived = archived != 0
		ss.AgentIDs = agentIDs(cur, retired, released)
		out[ss.ID] = ss
	}
	return out, rows.Err()
}

// agentIDs splits the id columns, which may hold one id or a list.
func agentIDs(cols ...string) []string {
	var out []string
	for _, c := range cols {
		for _, id := range strings.FieldsFunc(c, func(r rune) bool {
			return !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
		}) {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}
