package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type amSession struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Group          string `json:"group"`
	Directory      string `json:"directory"`
	AgentSessionID string `json:"agent_session_id"`
}

// amSessions maps a Claude session id to its agent-manager row. A missing or
// failing agent-manager yields an empty map: metadata is best effort.
func amSessions() map[string]amSession {
	out := map[string]amSession{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "agent-manager", "sessions", "--json").Output()
	if err != nil {
		return out
	}
	var rows []amSession
	if json.Unmarshal(raw, &rows) != nil {
		return out
	}
	for _, r := range rows {
		if r.AgentSessionID != "" {
			out[r.AgentSessionID] = r
		}
	}
	return out
}

func roleOf(name, dir string) string {
	switch {
	case strings.EqualFold(name, "chief"):
		return "chief"
	case strings.EqualFold(name, "orchestrator"):
		return "orchestrator"
	case strings.HasPrefix(name, "spec-"):
		return "spec-writer"
	case isWorktree(dir):
		return "worker"
	}
	return "agent"
}

func gitOut(dir string, args ...string) (string, bool) {
	if dir == "" {
		return "", false
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", false
	}
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// isWorktree: a linked git worktree, or, once it has been removed, a directory
// under agent-manager's `<repo>-worktrees/` parent.
func isWorktree(dir string) bool {
	if out, ok := gitOut(dir, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir"); ok {
		lines := strings.Split(out, "\n")
		return len(lines) == 2 && lines[0] != lines[1]
	}
	return strings.HasSuffix(filepath.Dir(dir), "-worktrees")
}

// repoOf names the repo so worktrees map to it: the parent of the git common dir.
func repoOf(dir string) string {
	if dir == "" {
		return ""
	}
	if out, ok := gitOut(dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); ok {
		return filepath.Base(filepath.Dir(out))
	}
	if parent := filepath.Dir(dir); strings.HasSuffix(parent, "-worktrees") {
		return strings.TrimSuffix(filepath.Base(parent), "-worktrees")
	}
	return filepath.Base(dir)
}

// lastModel reads the transcript tail for the last assistant message.model.
func lastModel(transcript string) string {
	f, err := os.Open(transcript)
	if err != nil {
		return ""
	}
	defer f.Close()
	const tail = 1 << 20
	if st, err := f.Stat(); err == nil && st.Size() > tail {
		_, _ = f.Seek(st.Size()-tail, io.SeekStart)
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	lines := bytes.Split(buf, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if m := modelOf(lines[i]); m != "" {
			return m
		}
	}
	return ""
}

func modelOf(line []byte) string {
	if !bytes.Contains(line, []byte(`"model"`)) || !bytes.Contains(line, []byte(`"assistant"`)) {
		return ""
	}
	var e struct {
		Type    string `json:"type"`
		Message struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil || e.Type != "assistant" || strings.HasPrefix(e.Message.Model, "<") {
		return ""
	}
	return e.Message.Model
}

type sessionMeta struct {
	SessionID, AMSession, Name, Group, Role, Model, Repo string
}

func upsertSession(db *sql.DB, m sessionMeta, now time.Time) error {
	_, err := db.Exec(`INSERT INTO tool_sessions (session_id, am_session, name, grp, role, model, repo, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (session_id) DO UPDATE SET
			am_session = CASE WHEN excluded.am_session != '' THEN excluded.am_session ELSE am_session END,
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE name END,
			grp = CASE WHEN excluded.grp != '' THEN excluded.grp ELSE grp END,
			role = CASE WHEN excluded.name != '' OR name = '' THEN excluded.role ELSE role END,
			model = CASE WHEN excluded.model != '' THEN excluded.model ELSE model END,
			repo = CASE WHEN excluded.repo != '' THEN excluded.repo ELSE repo END,
			updated = excluded.updated`,
		m.SessionID, m.AMSession, m.Name, m.Group, m.Role, m.Model, m.Repo, now.UTC().Format(tsLayout))
	return err
}

// metaFor builds a session's metadata from agent-manager, falling back to the
// first cwd seen in its calls.
func metaFor(sessionID, cwd, model string, am map[string]amSession) sessionMeta {
	m := sessionMeta{SessionID: sessionID, Model: model}
	dir := cwd
	if s, ok := am[sessionID]; ok {
		m.AMSession, m.Name, m.Group = s.ID, s.Name, s.Group
		if s.Directory != "" {
			dir = s.Directory
		}
	}
	m.Role = roleOf(m.Name, dir)
	m.Repo = repoOf(dir)
	return m
}

func projectsDir(home string) string {
	return filepath.Join(home, ".claude", "projects")
}

func findTranscript(home, sessionID string) string {
	matches, _ := filepath.Glob(filepath.Join(projectsDir(home), "*", sessionID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}
