package main

import (
	"database/sql"
	"fmt"
	"io"
	"sort"
	"time"
)

// Sinks live as checklist items on one rolling board task; telemetry_entries
// mirrors which items are open so the report can say what to append or tick.

const appendCount = 3

type Checklist struct {
	Task   string       `json:"task"`
	Append []AppendItem `json:"append"`
	Close  []CloseItem  `json:"close"`
}

type AppendItem struct {
	Key      string `json:"key"`
	TotalMS  int64  `json:"total_ms"`
	OutBytes int64  `json:"total_out_bytes"`
	Item     string `json:"item"`
}

type CloseItem struct {
	Key     string  `json:"key"`
	Match   string  `json:"match"`
	PrevMS  int64   `json:"prev_ms"`
	CurMS   int64   `json:"cur_ms"`
	PrevOut int64   `json:"prev_out_bytes"`
	CurOut  int64   `json:"cur_out_bytes"`
	DropPct float64 `json:"drop_pct"`
}

// itemPrefix is what a checklist item starts with, so `zen-board checklist
// --check` can find it without matching a longer key.
func itemPrefix(key string) string { return key + " ·" }

// halved reports a drop of 50% or more from a nonzero previous week.
func halved(prev, cur int64) bool { return prev > 0 && cur*2 <= prev }

// improved is halved on time and output bytes, where a metric that was 0 must stay 0.
func improved(prev, cur sinkTotal) bool {
	return (halved(prev.ms, cur.ms) || halved(prev.out, cur.out)) &&
		cur.ms*2 <= prev.ms && cur.out*2 <= prev.out
}

type sinkTotal struct{ ms, out int64 }

func openEntries(db *sql.DB) (map[string]bool, error) {
	q, err := db.Query(`SELECT sink_key FROM telemetry_entries WHERE closed_at = ''`)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	out := map[string]bool{}
	for q.Next() {
		var k string
		if err := q.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, q.Err()
}

func sinkTotals(rows []row, excluded func(string) bool) map[string]sinkTotal {
	out := map[string]sinkTotal{}
	for _, r := range rows {
		if r.background || excluded(r.key) {
			continue
		}
		t := out[r.key]
		if r.hasDur {
			t.ms += r.durMS
		}
		t.out += r.outBytes
		out[r.key] = t
	}
	return out
}

func buildChecklist(db *sql.DB, rep Report, cur, prev map[string]sinkTotal) (Checklist, error) {
	c := Checklist{Append: []AppendItem{}, Close: []CloseItem{}}
	if err := db.QueryRow(`SELECT value FROM telemetry_meta WHERE key = 'sink_task'`).Scan(&c.Task); err != nil && err != sql.ErrNoRows {
		return c, err
	}
	open, err := openEntries(db)
	if err != nil {
		return c, err
	}
	queued := map[string]bool{}
	lists := []struct {
		sinks  []Sink
		metric func(Sink) string
	}{
		{rep.Sinks, func(s Sink) string { return fmtDur(s.TotalMS) }},
		{rep.ByteSinks, func(s Sink) string { return fmtBytes(s.OutBytes) + " out" }},
	}
	for _, l := range lists {
		n := 0
		for _, s := range l.sinks {
			if n == appendCount {
				break
			}
			if open[s.Key] || queued[s.Key] {
				continue
			}
			queued[s.Key] = true
			n++
			c.Append = append(c.Append, AppendItem{Key: s.Key, TotalMS: s.TotalMS, OutBytes: s.OutBytes,
				Item: fmt.Sprintf("%s %s over %d days (%s)", itemPrefix(s.Key), l.metric(s), rep.Days, rep.Week)})
		}
	}
	keys := make([]string, 0, len(open))
	for k := range open {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p, n := prev[k], cur[k]
		if !improved(p, n) {
			continue
		}
		drop := pct(int(p.ms-n.ms), int(p.ms))
		if p.ms == 0 {
			drop = pct(int(p.out-n.out), int(p.out))
		}
		c.Close = append(c.Close, CloseItem{Key: k, Match: itemPrefix(k), PrevMS: p.ms, CurMS: n.ms,
			PrevOut: p.out, CurOut: n.out, DropPct: drop})
	}
	return c, nil
}

func runSinkTask(home, task string, fold []string, stdout io.Writer, now time.Time) error {
	db, err := openRW(home)
	if err != nil {
		return err
	}
	defer db.Close()
	if task == "" {
		var cur string
		err := db.QueryRow(`SELECT value FROM telemetry_meta WHERE key = 'sink_task'`).Scan(&cur)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		_, err = fmt.Fprintln(stdout, cur)
		return err
	}
	if _, err := db.Exec(`INSERT INTO telemetry_meta (key, value) VALUES ('sink_task', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, task); err != nil {
		return err
	}
	for _, old := range fold {
		q, err := db.Query(`SELECT DISTINCT sink_key FROM telemetry_filed WHERE task_id = ? ORDER BY sink_key`, old)
		if err != nil {
			return err
		}
		var keys []string
		for q.Next() {
			var k string
			if err := q.Scan(&k); err != nil {
				q.Close()
				return err
			}
			keys = append(keys, k)
		}
		q.Close()
		if err := q.Err(); err != nil {
			return err
		}
		for _, k := range keys {
			// A fold never reopens an entry already ticked.
			res, err := db.Exec(`INSERT OR IGNORE INTO telemetry_entries (sink_key, added_at, closed_at) VALUES (?, ?, '')`,
				k, now.UTC().Format(tsLayout))
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n > 0 {
				fmt.Fprintln(stdout, k)
			}
		}
	}
	return nil
}

// addEntry opens an entry for key; false when one is already open.
func addEntry(db *sql.DB, key string, now time.Time) (bool, error) {
	res, err := db.Exec(`INSERT INTO telemetry_entries (sink_key, added_at, closed_at) VALUES (?, ?, '')
		ON CONFLICT (sink_key) DO UPDATE SET added_at = excluded.added_at, closed_at = ''
		WHERE telemetry_entries.closed_at != ''`, key, now.UTC().Format(tsLayout))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func runEntry(home, action, key string, stdout io.Writer, now time.Time) error {
	db, err := openRW(home)
	if err != nil {
		return err
	}
	defer db.Close()
	if action == "add" {
		added, err := addEntry(db, key, now)
		if err != nil {
			return err
		}
		state := "already open"
		if added {
			state = "added"
		}
		_, err = fmt.Fprintln(stdout, state)
		return err
	}
	res, err := db.Exec(`UPDATE telemetry_entries SET closed_at = ? WHERE sink_key = ? AND closed_at = ''`,
		now.UTC().Format(tsLayout), key)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no open entry %q", key)
	}
	_, err = fmt.Fprintln(stdout, "closed")
	return err
}
