package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"time"
)

type hookPayload struct {
	SessionID      string          `json:"session_id"`
	Cwd            string          `json:"cwd"`
	PermissionMode string          `json:"permission_mode"`
	Event          string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	ToolUseID      string          `json:"tool_use_id"`
	DurationMS     *int64          `json:"duration_ms"`
	AgentID        string          `json:"agent_id"`
	AgentType      string          `json:"agent_type"`
	PromptID       string          `json:"prompt_id"`
	Effort         json.RawMessage `json:"effort"`
	Error          string          `json:"error"`
	IsInterrupt    bool            `json:"is_interrupt"`
}

// runHook records one PostToolUse/PostToolUseFailure payload. Every failure is
// silent: the hook must never break or slow a tool call.
func runHook(stdin io.Reader, home string, now time.Time) {
	am := os.Getenv("AGENT_MANAGER_SESSION_ID")
	if am == "" {
		return
	}
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return
	}
	c, ok := callFromHook(raw, am, now)
	if !ok {
		return
	}
	path, err := dbPath(home)
	if err != nil {
		return
	}
	db, err := openDB(path, 1000)
	if err != nil {
		return
	}
	defer db.Close()
	if _, err := db.Exec(hookInsertSQL, c.args()...); isNoTable(err) {
		if initSchema(db) == nil {
			_, _ = db.Exec(hookInsertSQL, c.args()...)
		}
	}
}

func callFromHook(raw []byte, am string, now time.Time) (call, bool) {
	var p hookPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return call{}, false
	}
	if p.SessionID == "" || p.ToolUseID == "" || p.ToolName == "" {
		return call{}, false
	}
	failed := p.Event == "PostToolUseFailure"
	c := call{
		SessionID:      p.SessionID,
		ToolUseID:      p.ToolUseID,
		AMSession:      am,
		AgentID:        p.AgentID,
		AgentType:      p.AgentType,
		PromptID:       p.PromptID,
		Host:           shortHost(),
		Cwd:            p.Cwd,
		Effort:         effortString(p.Effort),
		PermissionMode: p.PermissionMode,
		TS:             now,
		Tool:           p.ToolName,
		OutBytes:       len(p.ToolResponse),
		Interrupted:    p.IsInterrupt,
		Source:         "hook",
	}
	if p.DurationMS != nil {
		c.DurationMS = sql.NullInt64{Int64: *p.DurationMS, Valid: true}
	}
	var resp struct {
		Interrupted bool `json:"interrupted"`
	}
	if json.Unmarshal(p.ToolResponse, &resp) == nil && resp.Interrupted {
		c.Interrupted = true
	}
	c.describe(p.ToolInput, failed, p.Error)
	return c, true
}
