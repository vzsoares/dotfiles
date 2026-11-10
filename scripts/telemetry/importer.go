package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type entry struct {
	Type           string          `json:"type"`
	Timestamp      time.Time       `json:"timestamp"`
	SessionID      string          `json:"sessionId"`
	AgentID        string          `json:"agentId"`
	Cwd            string          `json:"cwd"`
	PermissionMode string          `json:"permissionMode"`
	PromptID       string          `json:"promptId"`
	Effort         json.RawMessage `json:"effort"`
	ToolUseResult  json.RawMessage `json:"toolUseResult"`
	Message        struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type transcript struct {
	path      string
	sessionID string
	agentType string // set for subagent transcripts
}

type parsed struct {
	calls []call
	model string
	cwd   string
}

var bannerMarkers = [][]byte{[]byte("[agent-manager]"), []byte("agent-manager rename")}

// listTranscripts returns the top-level transcripts modified since cutoff and
// their subagent transcripts.
func listTranscripts(home string, cutoff time.Time) []transcript {
	var out []transcript
	mains, _ := filepath.Glob(filepath.Join(projectsDir(home), "*", "*.jsonl"))
	sort.Strings(mains)
	for _, p := range mains {
		st, err := os.Stat(p)
		if err != nil || st.ModTime().Before(cutoff) {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		out = append(out, transcript{path: p, sessionID: id})
		subs, _ := filepath.Glob(filepath.Join(filepath.Dir(p), id, "subagents", "*.jsonl"))
		sort.Strings(subs)
		for _, s := range subs {
			out = append(out, transcript{path: s, sessionID: id, agentType: subagentType(s)})
		}
	}
	return out
}

func subagentType(path string) string {
	raw, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json")
	if err != nil {
		return ""
	}
	var m struct {
		AgentType string `json:"agentType"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.AgentType
}

// hasBanner reports whether one of the first user turns carries the
// agent-manager banner.
func hasBanner(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	users := 0
	for users < 5 {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, []byte(`"type":"user"`)) {
			users++
			for _, m := range bannerMarkers {
				if bytes.Contains(line, m) {
					return true
				}
			}
		}
		if err != nil {
			return false
		}
	}
	return false
}

type pending struct {
	ts       time.Time
	name     string
	input    json.RawMessage
	cwd      string
	effort   string
	mode     string
	promptID string
}

// parseTranscript streams a transcript line by line, pairing each tool_use
// with its tool_result.
func parseTranscript(r io.Reader, t transcript, am string) (parsed, error) {
	var out parsed
	br := bufio.NewReaderSize(r, 1<<20)
	open := map[string]pending{}
	var mode, promptID string
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			out.consume(line, t, am, open, &mode, &promptID)
		}
		if errors.Is(rerr, io.EOF) {
			return out, nil
		}
		if rerr != nil {
			return out, rerr
		}
	}
}

func (out *parsed) consume(line []byte, t transcript, am string, open map[string]pending, mode, promptID *string) {
	isUse := bytes.Contains(line, []byte(`"tool_use"`))
	isResult := bytes.Contains(line, []byte(`"tool_result"`))
	if !isUse && !isResult {
		if m := modelOf(line); m != "" {
			out.model = m
		}
		return
	}
	var e entry
	if json.Unmarshal(line, &e) != nil {
		return
	}
	if e.PermissionMode != "" {
		*mode = e.PermissionMode
	}
	if e.PromptID != "" {
		*promptID = e.PromptID
	}
	if out.cwd == "" {
		out.cwd = e.Cwd
	}
	if e.Type != "assistant" && e.Type != "user" {
		return
	}
	if e.Type == "assistant" && e.Message.Model != "" && !strings.HasPrefix(e.Message.Model, "<") {
		out.model = e.Message.Model
	}
	var blocks []block
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch {
		case e.Type == "assistant" && b.Type == "tool_use" && b.ID != "":
			open[b.ID] = pending{ts: e.Timestamp, name: b.Name, input: b.Input, cwd: e.Cwd,
				effort: effortString(e.Effort), mode: *mode, promptID: *promptID}
		case e.Type == "user" && b.Type == "tool_result":
			p, ok := open[b.ToolUseID]
			if !ok {
				continue
			}
			delete(open, b.ToolUseID)
			out.calls = append(out.calls, importedCall(p, b, e, t, am))
		}
	}
}

func importedCall(p pending, b block, e entry, t transcript, am string) call {
	sessionID := e.SessionID
	if sessionID == "" {
		sessionID = t.sessionID
	}
	c := call{
		SessionID:      sessionID,
		ToolUseID:      b.ToolUseID,
		AMSession:      am,
		AgentID:        e.AgentID,
		AgentType:      t.agentType,
		PromptID:       p.promptID,
		Host:           shortHost(),
		Cwd:            p.cwd,
		Effort:         p.effort,
		PermissionMode: p.mode,
		TS:             e.Timestamp,
		Tool:           p.name,
		Source:         "import",
	}
	if !p.ts.IsZero() && !e.Timestamp.IsZero() {
		c.DurationMS = sql.NullInt64{Int64: e.Timestamp.Sub(p.ts).Milliseconds(), Valid: true}
	}
	if len(e.ToolUseResult) > 0 {
		c.OutBytes = len(e.ToolUseResult)
	} else {
		c.OutBytes = len(b.Content)
	}
	var tur struct {
		Interrupted bool `json:"interrupted"`
	}
	if json.Unmarshal(e.ToolUseResult, &tur) == nil {
		c.Interrupted = tur.Interrupted
	}
	errText := ""
	if b.IsError {
		errText = resultText(b.Content)
	}
	c.describe(p.input, b.IsError, errText)
	return c
}

// resultText flattens a tool_result content: a string or a list of text blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

type importStats struct {
	Transcripts int
	Sessions    int
	Parsed      int
	Added       int
}

func runImport(home string, days int, dryRun bool, stdout io.Writer, now time.Time) error {
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	am := amSessions()
	selected := map[string]bool{}
	var stats importStats
	var db *sql.DB
	if !dryRun {
		path, err := dbPath(home)
		if err != nil {
			return err
		}
		if db, err = openDB(path, 5000); err != nil {
			return err
		}
		defer db.Close()
		if err := initSchema(db); err != nil {
			return err
		}
	}
	type sessionInfo struct{ cwd, model string }
	infos := map[string]*sessionInfo{}
	for _, t := range listTranscripts(home, cutoff) {
		if t.path == mainPath(t) {
			_, inAM := am[t.sessionID]
			selected[t.sessionID] = inAM || hasBanner(t.path)
		}
		if !selected[t.sessionID] {
			continue
		}
		f, err := os.Open(t.path)
		if err != nil {
			return err
		}
		p, err := parseTranscript(f, t, am[t.sessionID].ID)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", t.path, err)
		}
		stats.Transcripts++
		stats.Parsed += len(p.calls)
		info := infos[t.sessionID]
		if info == nil {
			info = &sessionInfo{}
			infos[t.sessionID] = info
			stats.Sessions++
		}
		if t.agentType == "" {
			info.cwd, info.model = p.cwd, p.model
		}
		if dryRun {
			continue
		}
		added, err := insertImported(db, p.calls)
		if err != nil {
			return err
		}
		stats.Added += added
	}
	if !dryRun {
		for id, info := range infos {
			if err := upsertSession(db, metaFor(id, info.cwd, info.model, am), now); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(stdout, "import: %d sessions, %d transcripts, %d calls parsed", stats.Sessions, stats.Transcripts, stats.Parsed)
	if dryRun {
		fmt.Fprintln(stdout, " (dry run, nothing written)")
	} else {
		fmt.Fprintf(stdout, ", %d added\n", stats.Added)
	}
	return nil
}

func mainPath(t transcript) string {
	return filepath.Join(filepath.Dir(t.path), t.sessionID+".jsonl")
}

func insertImported(db *sql.DB, calls []call) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(importInsertSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	added := 0
	for i := range calls {
		res, err := stmt.Exec(calls[i].args()...)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		added += int(n)
	}
	return added, tx.Commit()
}
