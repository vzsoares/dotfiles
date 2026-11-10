package main

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	inputMax = 500
	errorMax = 300
	tsLayout = "2006-01-02T15:04:05.000Z"
)

// call is one tool_calls row.
type call struct {
	SessionID      string
	ToolUseID      string
	AMSession      string
	AgentID        string
	AgentType      string
	PromptID       string
	Host           string
	Cwd            string
	Effort         string
	PermissionMode string
	TS             time.Time
	DurationMS     sql.NullInt64
	Tool           string
	MCPServer      string
	Exe            string
	Exes           []string
	Input          string
	OK             bool
	ExitCode       sql.NullInt64
	Error          string
	Interrupted    bool
	TimedOut       bool
	Background     bool
	OutBytes       int
	InBytes        int
	Source         string
}

var (
	exitCodeRe = regexp.MustCompile(`Exit code (-?\d+)`)
	timedOutRe = regexp.MustCompile(`(?i)timed out`)
)

// describe fills the fields derived from the tool name, its input and the
// failure text. errText is empty for a success.
func (c *call) describe(input json.RawMessage, failed bool, errText string) {
	c.InBytes = len(input)
	c.OK = !failed
	if strings.HasPrefix(c.Tool, "mcp__") {
		if parts := strings.SplitN(c.Tool, "__", 3); len(parts) == 3 {
			c.MCPServer = parts[1]
		}
	}
	var in map[string]json.RawMessage
	_ = json.Unmarshal(input, &in)
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(in[k], &s)
		return s
	}
	var bg bool
	_ = json.Unmarshal(in["run_in_background"], &bg)
	c.Background = bg

	switch c.Tool {
	case "Bash":
		cmd := str("command")
		exe, exes := commandExes(cmd)
		c.Exe = redact(exe)
		c.Exes = make([]string, len(exes))
		for i, e := range exes {
			c.Exes[i] = redact(e)
		}
		c.Input = truncate(redact(cmd), inputMax)
		if failed {
			if m := exitCodeRe.FindStringSubmatch(errText); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil {
					c.ExitCode = sql.NullInt64{Int64: int64(n), Valid: true}
				}
			}
			c.TimedOut = timedOutRe.MatchString(errText)
		} else {
			c.ExitCode = sql.NullInt64{Int64: 0, Valid: true}
		}
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		p := str("file_path")
		if p == "" {
			p = str("notebook_path")
		}
		c.Input = truncate(redact(p), inputMax)
	case "Grep", "Glob":
		c.Input = truncate(redact(joinNonEmpty(" ", str("pattern"), str("path"))), inputMax)
	case "WebFetch":
		if u, err := url.Parse(str("url")); err == nil {
			c.Input = u.Hostname()
		}
	case "WebSearch":
		c.Input = truncate(redact(str("query")), inputMax)
	case "Agent", "Task":
		c.Input = truncate(redact(joinNonEmpty(": ", str("subagent_type"), str("description"))), inputMax)
	case "Skill":
		c.Input = truncate(redact(str("skill")), inputMax)
	default:
		// MCP and unknown tools: key names only, values may carry private content.
		keys := make([]string, 0, len(in))
		for k := range in {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		c.Input = truncate(redact(strings.Join(keys, ",")), inputMax)
	}
	if failed {
		c.Error = truncate(redact(errText), errorMax)
	}
}

const upsertCols = `session_id, tool_use_id, am_session, agent_id, agent_type, prompt_id, host, cwd,
	effort, permission_mode, ts, duration_ms, tool, mcp_server, exe, exes, input, ok, exit_code,
	error, interrupted, timed_out, background, out_bytes, in_bytes, source`

const insertSQL = `INSERT INTO tool_calls (` + upsertCols + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// A hook row replaces an import row for the same call; an import never
// overwrites anything.
const hookInsertSQL = insertSQL + ` ON CONFLICT (session_id, tool_use_id) DO UPDATE SET
	am_session = excluded.am_session, agent_id = excluded.agent_id, agent_type = excluded.agent_type,
	prompt_id = excluded.prompt_id, host = excluded.host, cwd = excluded.cwd, effort = excluded.effort,
	permission_mode = excluded.permission_mode, ts = excluded.ts, duration_ms = excluded.duration_ms,
	tool = excluded.tool, mcp_server = excluded.mcp_server, exe = excluded.exe, exes = excluded.exes,
	input = excluded.input, ok = excluded.ok, exit_code = excluded.exit_code, error = excluded.error,
	interrupted = excluded.interrupted, timed_out = excluded.timed_out, background = excluded.background,
	out_bytes = excluded.out_bytes, in_bytes = excluded.in_bytes, source = excluded.source`

const importInsertSQL = insertSQL + ` ON CONFLICT (session_id, tool_use_id) DO NOTHING`

func (c *call) args() []any {
	return []any{c.SessionID, c.ToolUseID, c.AMSession, c.AgentID, c.AgentType, c.PromptID, c.Host,
		c.Cwd, c.Effort, c.PermissionMode, c.TS.UTC().Format(tsLayout), c.DurationMS, c.Tool, c.MCPServer,
		c.Exe, strings.Join(c.Exes, " "), c.Input, c.OK, c.ExitCode, c.Error, c.Interrupted, c.TimedOut,
		c.Background, c.OutBytes, c.InBytes, c.Source}
}

func joinNonEmpty(sep string, parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, sep)
}

// effortString accepts "high" or {"level":"high"}.
func effortString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Level string `json:"level"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.Level
	}
	return ""
}
