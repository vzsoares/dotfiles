package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const usageSchema = `
CREATE TABLE IF NOT EXISTS tool_usage (
	session_id         TEXT NOT NULL,
	agent_id           TEXT NOT NULL DEFAULT '',
	agent_type         TEXT NOT NULL DEFAULT '',
	model              TEXT NOT NULL,
	day                TEXT NOT NULL,
	messages           INTEGER NOT NULL,
	input_tokens       INTEGER NOT NULL,
	output_tokens      INTEGER NOT NULL,
	cache_read_tokens  INTEGER NOT NULL,
	cache_write_tokens INTEGER NOT NULL,
	PRIMARY KEY (session_id, agent_id, model, day)
);
CREATE INDEX IF NOT EXISTS tool_usage_day ON tool_usage (day);
`

type tokens struct {
	Input, Output, CacheRead, CacheWrite int64
}

type usageKey struct{ model, day string }

type usageRow struct {
	tokens
	messages int
}

// usageTally sums assistant usage per model and UTC day. A response streamed
// as several transcript lines repeats its message id: the last line wins.
type usageTally struct {
	byMsg map[string]msgUsage
	anon  int
}

type msgUsage struct {
	key usageKey
	tokens
}

func (u *usageTally) add(line []byte) {
	if !bytes.Contains(line, []byte(`"usage"`)) || !bytes.Contains(line, []byte(`"assistant"`)) {
		return
	}
	var e struct {
		Type      string    `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		Message   struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage struct {
				Input      int64 `json:"input_tokens"`
				Output     int64 `json:"output_tokens"`
				CacheRead  int64 `json:"cache_read_input_tokens"`
				CacheWrite int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil || e.Type != "assistant" ||
		e.Message.Model == "" || strings.HasPrefix(e.Message.Model, "<") {
		return
	}
	id := e.Message.ID
	if id == "" {
		u.anon++
		id = fmt.Sprintf("anon-%d", u.anon)
	}
	if u.byMsg == nil {
		u.byMsg = map[string]msgUsage{}
	}
	us := e.Message.Usage
	u.byMsg[id] = msgUsage{
		key:    usageKey{e.Message.Model, e.Timestamp.UTC().Format("2006-01-02")},
		tokens: tokens{us.Input, us.Output, us.CacheRead, us.CacheWrite},
	}
}

func (u *usageTally) rows() map[usageKey]usageRow {
	out := map[usageKey]usageRow{}
	for _, m := range u.byMsg {
		r := out[m.key]
		r.messages++
		r.Input += m.Input
		r.Output += m.Output
		r.CacheRead += m.CacheRead
		r.CacheWrite += m.CacheWrite
		out[m.key] = r
	}
	return out
}

// insertUsage replaces a transcript's rows: a live session's totals grow, so
// the last import wins. It returns the rows that were new.
func insertUsage(db *sql.DB, t transcript, u *usageTally) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	added := 0
	for k, r := range u.rows() {
		var one int
		err := tx.QueryRow(`SELECT 1 FROM tool_usage WHERE session_id = ? AND agent_id = ? AND model = ? AND day = ?`,
			t.sessionID, t.agentID, k.model, k.day).Scan(&one)
		if err == sql.ErrNoRows {
			added++
		} else if err != nil {
			return 0, err
		}
		_, err = tx.Exec(`INSERT INTO tool_usage
			(session_id, agent_id, agent_type, model, day, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (session_id, agent_id, model, day) DO UPDATE SET
				agent_type = excluded.agent_type, messages = excluded.messages,
				input_tokens = excluded.input_tokens, output_tokens = excluded.output_tokens,
				cache_read_tokens = excluded.cache_read_tokens, cache_write_tokens = excluded.cache_write_tokens`,
			t.sessionID, t.agentID, t.agentType, k.model, k.day, r.messages, r.Input, r.Output, r.CacheRead, r.CacheWrite)
		if err != nil {
			return 0, err
		}
	}
	return added, tx.Commit()
}

type ModelStat struct {
	Role       string `json:"role"`
	Agent      string `json:"agent"`
	Model      string `json:"model"`
	Runs       int    `json:"runs"`
	Messages   int    `json:"messages"`
	Input      int64  `json:"input_tokens"`
	Output     int64  `json:"output_tokens"`
	CacheRead  int64  `json:"cache_read_tokens"`
	CacheWrite int64  `json:"cache_write_tokens"`
}

type ModelReport struct {
	From     string      `json:"from"`
	To       string      `json:"to"`
	Days     int         `json:"days"`
	Current  []ModelStat `json:"current"`
	Previous []ModelStat `json:"previous"`
}

// loadModelStats groups usage by session role, agent type ("main" for the
// session itself) and model, over whole UTC days from..to inclusive.
func loadModelStats(db *sql.DB, from, to string) ([]ModelStat, error) {
	q, err := db.Query(`SELECT COALESCE(NULLIF(s.role, ''), 'unknown'),
			CASE WHEN u.agent_id = '' THEN 'main' WHEN u.agent_type = '' THEN 'subagent' ELSE u.agent_type END,
			u.model, COUNT(DISTINCT u.session_id || '/' || u.agent_id), SUM(u.messages),
			SUM(u.input_tokens), SUM(u.output_tokens), SUM(u.cache_read_tokens), SUM(u.cache_write_tokens)
		FROM tool_usage u LEFT JOIN tool_sessions s ON s.session_id = u.session_id
		WHERE u.day >= ? AND u.day <= ?
		GROUP BY 1, 2, 3
		ORDER BY SUM(u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_write_tokens) DESC, 1, 2, 3`, from, to)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	out := []ModelStat{}
	for q.Next() {
		var m ModelStat
		if err := q.Scan(&m.Role, &m.Agent, &m.Model, &m.Runs, &m.Messages, &m.Input, &m.Output, &m.CacheRead, &m.CacheWrite); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, q.Err()
}

func runByModel(home string, days int, asJSON bool, stdout io.Writer, now time.Time) error {
	p, err := dbPath(home)
	if err != nil {
		return err
	}
	db, err := openDB(p, 5000)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		return err
	}
	const dayFmt = "2006-01-02"
	end := now.UTC()
	start := end.AddDate(0, 0, -(days - 1))
	prevEnd := start.AddDate(0, 0, -1)
	prevStart := prevEnd.AddDate(0, 0, -(days - 1))
	rep := ModelReport{From: start.Format(dayFmt), To: end.Format(dayFmt), Days: days}
	if rep.Current, err = loadModelStats(db, rep.From, rep.To); err != nil {
		return err
	}
	if rep.Previous, err = loadModelStats(db, prevStart.Format(dayFmt), prevEnd.Format(dayFmt)); err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	_, err = io.WriteString(stdout, renderByModel(rep, prevStart.Format(dayFmt), prevEnd.Format(dayFmt)))
	return err
}

func renderByModel(r ModelReport, prevFrom, prevTo string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Tokens by model\n\n")
	section := func(title string, rows []ModelStat) {
		fmt.Fprintf(&b, "## %s\n\n", title)
		if len(rows) == 0 {
			b.WriteString("No usage recorded. Run `zen-telemetry import`.\n\n")
			return
		}
		b.WriteString("| role | agent | model | runs | input | output | cache read | cache write |\n|---|---|---|---|---|---|---|---|\n")
		for _, m := range rows {
			fmt.Fprintf(&b, "| %s | %s | `%s` | %d | %d | %d | %d | %d |\n",
				m.Role, m.Agent, m.Model, m.Runs, m.Input, m.Output, m.CacheRead, m.CacheWrite)
		}
		b.WriteString("\n")
	}
	section(fmt.Sprintf("%s to %s", r.From, r.To), r.Current)
	section(fmt.Sprintf("Previous: %s to %s", prevFrom, prevTo), r.Previous)
	return b.String()
}
