package main

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path"
	"sort"
	"strings"
	"time"
)

//go:embed jobs.json
var jobsJSON []byte

type jobsMap struct {
	Jobs        map[string][]string `json:"jobs"`
	Subcommands []string            `json:"subcommands"`
	Exclude     []string            `json:"exclude"`
}

const (
	retryWindow   = 5 * time.Minute
	retentionDays = 365
	sinkCount     = 6
	toolRows      = 30
	slowGroupRows = 10
	unmappedRows  = 10
	totalsNote    = "Totals are sums of call durations: parallel calls overlap, so they can exceed wall time. Import rows are approximate and include permission waits."
)

type reportOpts struct {
	Days int
	Slow time.Duration
	JSON bool
	Due  bool
}

type Report struct {
	Week       string      `json:"week"`
	From       string      `json:"from"`
	To         string      `json:"to"`
	Days       int         `json:"days"`
	Note       string      `json:"note"`
	Slow       SlowSection `json:"slow"`
	Tools      []ToolStat  `json:"tools"`
	Jobs       []JobStat   `json:"jobs"`
	Background []KeyCount  `json:"background"`
	Sinks      []Sink      `json:"sinks"`
	Unmapped   []KeyTotal  `json:"unmapped"`
}

type SlowSection struct {
	ThresholdMS int64       `json:"threshold_ms"`
	Count       int         `json:"count"`
	TotalMS     int64       `json:"total_ms"`
	Groups      []SlowGroup `json:"groups"`
}

type SlowGroup struct {
	Key      string      `json:"key"`
	Calls    int         `json:"calls"`
	TotalMS  int64       `json:"total_ms"`
	Excluded bool        `json:"excluded_from_sinks"`
	Example  SlowExample `json:"example"`
}

type SlowExample struct {
	Input      string `json:"input"`
	Session    string `json:"session"`
	Repo       string `json:"repo"`
	DurationMS int64  `json:"duration_ms"`
}

type ToolStat struct {
	Key            string  `json:"key"`
	Calls          int     `json:"calls"`
	TotalMS        int64   `json:"total_ms"`
	MedianMS       int64   `json:"median_ms"`
	P90MS          int64   `json:"p90_ms"`
	FailPct        float64 `json:"fail_pct"`
	RetryPct       float64 `json:"retry_pct"`
	Timeouts       int     `json:"timeouts"`
	MedianOutBytes int64   `json:"median_out_bytes"`
	TotalOutBytes  int64   `json:"total_out_bytes"`
}

type JobStat struct {
	Job   string     `json:"job"`
	Tools []ToolStat `json:"tools"`
}

type KeyCount struct {
	Key   string `json:"key"`
	Calls int    `json:"calls"`
}

type KeyTotal struct {
	Key     string `json:"key"`
	Calls   int    `json:"calls"`
	TotalMS int64  `json:"total_ms"`
}

type Filed struct {
	Week   string `json:"week"`
	TaskID string `json:"task_id"`
}

type Sink struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Calls    int      `json:"calls"`
	TotalMS  int64    `json:"total_ms"`
	Jobs     []string `json:"jobs"`
	Filed    []Filed  `json:"filed"`
	Markdown string   `json:"markdown"`
}

type row struct {
	session, agent string
	ts             time.Time
	durMS          int64
	hasDur         bool
	tool, exe      string
	input          string
	ok, timedOut   bool
	background     bool
	outBytes       int64
	name, repo     string
	key            string
	retry          bool
}

func isoWeek(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

func loadJobs() (jobsMap, error) {
	var j jobsMap
	err := json.Unmarshal(jobsJSON, &j)
	return j, err
}

func matchAny(patterns []string, key string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, key); ok {
			return true
		}
	}
	return false
}

// groupKey is the tool name, or bash:<exe> for Bash. Exes listed under
// subcommands also keep their first non-flag argument (bash:agent-manager wait).
func groupKey(tool, exe, input string, subcommands []string) string {
	if tool != "Bash" {
		return tool
	}
	if exe == "" {
		exe = "?"
	}
	key := "bash:" + exe
	for _, sc := range subcommands {
		if sc != exe {
			continue
		}
		for _, s := range parseCommand(input) {
			if s.exe != exe {
				continue
			}
			for _, a := range s.args {
				if !strings.HasPrefix(a, "-") {
					return key + " " + a
				}
			}
			break
		}
	}
	return key
}

func runReport(home string, o reportOpts, stdout io.Writer, now time.Time) (int, error) {
	jobs, err := loadJobs()
	if err != nil {
		return 1, err
	}
	p, err := dbPath(home)
	if err != nil {
		return 1, err
	}
	db, err := openDB(p, 5000)
	if err != nil {
		return 1, err
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		return 1, err
	}
	week := isoWeek(now)
	if o.Due {
		var one int
		err := db.QueryRow(`SELECT 1 FROM telemetry_weeks WHERE week = ?`, week).Scan(&one)
		if err == nil {
			return 10, nil
		}
		if err != sql.ErrNoRows {
			return 1, err
		}
	}
	if _, err := db.Exec(`DELETE FROM tool_calls WHERE ts < ?`,
		now.Add(-retentionDays*24*time.Hour).UTC().Format(tsLayout)); err != nil {
		return 1, err
	}
	from := now.Add(-time.Duration(o.Days) * 24 * time.Hour)
	if err := fillSessions(db, home, from, now); err != nil {
		return 1, err
	}
	rows, err := loadRows(db, from, now, jobs)
	if err != nil {
		return 1, err
	}
	rep, err := buildReport(db, rows, jobs, o, week, from, now)
	if err != nil {
		return 1, err
	}
	if o.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return 0, enc.Encode(rep)
	}
	_, err = io.WriteString(stdout, renderMarkdown(rep))
	return 0, err
}

func fillSessions(db *sql.DB, home string, from, to time.Time) error {
	q, err := db.Query(`SELECT c.session_id, c.cwd, MIN(c.ts) FROM tool_calls c
		LEFT JOIN tool_sessions s ON s.session_id = c.session_id
		WHERE c.ts >= ? AND c.ts <= ? AND s.session_id IS NULL
		GROUP BY c.session_id`, from.UTC().Format(tsLayout), to.UTC().Format(tsLayout))
	if err != nil {
		return err
	}
	type miss struct{ id, cwd string }
	var missing []miss
	for q.Next() {
		var m miss
		var first string
		if err := q.Scan(&m.id, &m.cwd, &first); err != nil {
			q.Close()
			return err
		}
		missing = append(missing, m)
	}
	q.Close()
	if err := q.Err(); err != nil || len(missing) == 0 {
		return err
	}
	am := amSessions()
	for _, m := range missing {
		meta := metaFor(m.id, m.cwd, lastModel(findTranscript(home, m.id)), am)
		if err := upsertSession(db, meta, to); err != nil {
			return err
		}
	}
	return nil
}

func loadRows(db *sql.DB, from, to time.Time, jobs jobsMap) ([]row, error) {
	q, err := db.Query(`SELECT c.session_id, c.agent_id, c.ts, c.duration_ms, c.tool, c.exe, c.input,
			c.ok, c.timed_out, c.background, c.out_bytes, COALESCE(s.name, ''), COALESCE(s.repo, '')
		FROM tool_calls c LEFT JOIN tool_sessions s ON s.session_id = c.session_id
		WHERE c.ts >= ? AND c.ts <= ?
		ORDER BY c.session_id, c.agent_id, c.ts`, from.UTC().Format(tsLayout), to.UTC().Format(tsLayout))
	if err != nil {
		return nil, err
	}
	defer q.Close()
	var out []row
	type gk struct{ session, agent, key string }
	lastFail := map[gk]time.Time{}
	for q.Next() {
		var r row
		var ts string
		var dur sql.NullInt64
		if err := q.Scan(&r.session, &r.agent, &ts, &dur, &r.tool, &r.exe, &r.input, &r.ok, &r.timedOut,
			&r.background, &r.outBytes, &r.name, &r.repo); err != nil {
			return nil, err
		}
		if r.ts, err = time.Parse(tsLayout, ts); err != nil {
			return nil, fmt.Errorf("bad ts %q: %w", ts, err)
		}
		r.durMS, r.hasDur = dur.Int64, dur.Valid
		r.key = groupKey(r.tool, r.exe, r.input, jobs.Subcommands)
		g := gk{r.session, r.agent, r.key}
		if t, ok := lastFail[g]; ok && r.ts.Sub(t) < retryWindow {
			r.retry = true
		}
		if r.ok {
			delete(lastFail, g)
		} else {
			lastFail[g] = r.ts
		}
		out = append(out, r)
	}
	return out, q.Err()
}

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	return sorted[i]
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return math.Round(1000*float64(n)/float64(d)) / 10
}

func stats(key string, rows []row) ToolStat {
	s := ToolStat{Key: key, Calls: len(rows)}
	var durs, outs []int64
	fails, retries := 0, 0
	for _, r := range rows {
		if r.hasDur {
			durs = append(durs, r.durMS)
			s.TotalMS += r.durMS
		}
		outs = append(outs, r.outBytes)
		s.TotalOutBytes += r.outBytes
		if !r.ok {
			fails++
		}
		if r.retry {
			retries++
		}
		if r.timedOut {
			s.Timeouts++
		}
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	sort.Slice(outs, func(i, j int) bool { return outs[i] < outs[j] })
	s.MedianMS = percentile(durs, 0.5)
	s.P90MS = percentile(durs, 0.9)
	s.MedianOutBytes = percentile(outs, 0.5)
	s.FailPct = pct(fails, len(rows))
	s.RetryPct = pct(retries, len(rows))
	return s
}

func byKey(rows []row, keep func(row) bool) (map[string][]row, []string) {
	m := map[string][]row{}
	for _, r := range rows {
		if keep(r) {
			m[r.key] = append(m[r.key], r)
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return m, keys
}

func totalMS(rows []row) int64 {
	var t int64
	for _, r := range rows {
		t += r.durMS
	}
	return t
}

// sortByTotal orders keys by summed duration, then name, so output is stable.
func sortByTotal(keys []string, m map[string][]row) {
	totals := make(map[string]int64, len(m))
	for k, rows := range m {
		totals[k] = totalMS(rows)
	}
	sort.Slice(keys, func(i, j int) bool {
		ti, tj := totals[keys[i]], totals[keys[j]]
		if ti != tj {
			return ti > tj
		}
		return keys[i] < keys[j]
	})
}

func buildReport(db *sql.DB, rows []row, jobs jobsMap, o reportOpts, week string, from, to time.Time) (Report, error) {
	rep := Report{
		Week: week, From: from.UTC().Format(time.RFC3339), To: to.UTC().Format(time.RFC3339),
		Days: o.Days, Note: totalsNote,
		Tools: []ToolStat{}, Jobs: []JobStat{}, Background: []KeyCount{}, Sinks: []Sink{}, Unmapped: []KeyTotal{},
	}
	slowMS := o.Slow.Milliseconds()
	rep.Slow = SlowSection{ThresholdMS: slowMS, Groups: []SlowGroup{}}
	excluded := func(key string) bool { return matchAny(jobs.Exclude, key) }

	slow, slowKeys := byKey(rows, func(r row) bool { return r.hasDur && r.durMS > slowMS })
	sortByTotal(slowKeys, slow)
	for i, k := range slowKeys {
		g := slow[k]
		rep.Slow.Count += len(g)
		rep.Slow.TotalMS += totalMS(g)
		if i >= slowGroupRows {
			continue
		}
		ex := g[0]
		for _, r := range g {
			if r.durMS > ex.durMS {
				ex = r
			}
		}
		session := ex.name
		if session == "" {
			session = ex.session
		}
		rep.Slow.Groups = append(rep.Slow.Groups, SlowGroup{Key: k, Calls: len(g), TotalMS: totalMS(g),
			Excluded: excluded(k),
			Example:  SlowExample{Input: ex.input, Session: session, Repo: ex.repo, DurationMS: ex.durMS}})
	}

	all, keys := byKey(rows, func(row) bool { return true })
	sortByTotal(keys, all)
	statOf := map[string]ToolStat{}
	for _, k := range keys {
		statOf[k] = stats(k, all[k])
		rep.Tools = append(rep.Tools, statOf[k])
	}

	jobNames := make([]string, 0, len(jobs.Jobs))
	for j := range jobs.Jobs {
		jobNames = append(jobNames, j)
	}
	sort.Strings(jobNames)
	jobsOf := map[string][]string{}
	for _, j := range jobNames {
		js := JobStat{Job: j, Tools: []ToolStat{}}
		for _, k := range keys {
			if matchAny(jobs.Jobs[j], k) {
				js.Tools = append(js.Tools, statOf[k])
				jobsOf[k] = append(jobsOf[k], j)
			}
		}
		if len(js.Tools) > 0 {
			rep.Jobs = append(rep.Jobs, js)
		}
	}

	bg, bgKeys := byKey(rows, func(r row) bool { return r.background })
	sort.Strings(bgKeys)
	for _, k := range bgKeys {
		rep.Background = append(rep.Background, KeyCount{Key: k, Calls: len(bg[k])})
	}

	sinks, sinkKeys := byKey(rows, func(r row) bool { return !r.background && !excluded(r.key) })
	sortByTotal(sinkKeys, sinks)
	for _, k := range sinkKeys {
		if len(rep.Sinks) == sinkCount {
			break
		}
		g := sinks[k]
		if totalMS(g) == 0 {
			continue
		}
		filed, err := filedFor(db, k)
		if err != nil {
			return rep, err
		}
		s := Sink{Key: k, Calls: len(g), TotalMS: totalMS(g), Jobs: jobsOf[k], Filed: filed,
			Title: fmt.Sprintf("Cut agent time in %s: %d calls, %s over %d days", k, len(g), fmtDur(totalMS(g)), o.Days)}
		if s.Jobs == nil {
			s.Jobs = []string{}
		}
		s.Markdown = sinkMarkdown(s, stats(k, g), rep.Jobs)
		rep.Sinks = append(rep.Sinks, s)
	}

	for _, k := range keys {
		if len(rep.Unmapped) == unmappedRows {
			break
		}
		if len(jobsOf[k]) == 0 && !excluded(k) {
			rep.Unmapped = append(rep.Unmapped, KeyTotal{Key: k, Calls: len(all[k]), TotalMS: totalMS(all[k])})
		}
	}
	return rep, nil
}

func filedFor(db *sql.DB, key string) ([]Filed, error) {
	q, err := db.Query(`SELECT week, task_id FROM telemetry_filed WHERE sink_key = ? ORDER BY week`, key)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	out := []Filed{}
	for q.Next() {
		var f Filed
		if err := q.Scan(&f.Week, &f.TaskID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, q.Err()
}

func fmtDur(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", ms)
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func fmtBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1<<20:
		return fmt.Sprintf("%.1fK", float64(b)/1024)
	}
	return fmt.Sprintf("%.1fM", float64(b)/(1<<20))
}

func cell(s string) string {
	s = strings.ReplaceAll(truncate(s, 120), "\n", " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

const toolHeader = "| tool | calls | total | median | p90 | fail % | retry % | timeouts | median out | total out |\n|---|---|---|---|---|---|---|---|---|---|\n"

func toolLine(s ToolStat) string {
	return fmt.Sprintf("| `%s` | %d | %s | %s | %s | %.1f | %.1f | %d | %s | %s |\n", s.Key, s.Calls,
		fmtDur(s.TotalMS), fmtDur(s.MedianMS), fmtDur(s.P90MS), s.FailPct, s.RetryPct, s.Timeouts,
		fmtBytes(s.MedianOutBytes), fmtBytes(s.TotalOutBytes))
}

func sinkMarkdown(s Sink, st ToolStat, jobs []JobStat) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### `%s`\n\n%s\n\n", s.Key, s.Title)
	b.WriteString(toolHeader + toolLine(st))
	for _, j := range jobs {
		for _, jt := range j.Tools {
			if jt.Key != s.Key {
				continue
			}
			fmt.Fprintf(&b, "\nSame job (%s):\n\n%s", j.Job, toolHeader)
			for _, t := range j.Tools {
				b.WriteString(toolLine(t))
			}
		}
	}
	return b.String()
}

func renderMarkdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent tool telemetry %s\n\n%s to %s (%d days). %s\n\n", r.Week, r.From, r.To, r.Days, r.Note)

	fmt.Fprintf(&b, "## Calls over %s\n\n%d calls, %s total.\n\n", fmtDur(r.Slow.ThresholdMS), r.Slow.Count, fmtDur(r.Slow.TotalMS))
	if len(r.Slow.Groups) > 0 {
		b.WriteString("| tool | calls | total | slowest example |\n|---|---|---|---|\n")
		for _, g := range r.Slow.Groups {
			mark := ""
			if g.Excluded {
				mark = " (not a sink)"
			}
			fmt.Fprintf(&b, "| `%s`%s | %d | %s | %s: `%s` · %s · %s |\n", g.Key, mark, g.Calls, fmtDur(g.TotalMS),
				fmtDur(g.Example.DurationMS), cell(g.Example.Input), cell(g.Example.Session), cell(g.Example.Repo))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Time per tool\n\n")
	if len(r.Tools) > toolRows {
		fmt.Fprintf(&b, "Top %d of %d.\n\n", toolRows, len(r.Tools))
	}
	if len(r.Tools) > 0 {
		b.WriteString(toolHeader)
		for i, t := range r.Tools {
			if i == toolRows {
				break
			}
			b.WriteString(toolLine(t))
		}
	} else {
		b.WriteString("No calls in the window.\n")
	}

	b.WriteString("\n## Same-job comparisons\n")
	for _, j := range r.Jobs {
		fmt.Fprintf(&b, "\n### %s\n\n%s", j.Job, toolHeader)
		for _, t := range j.Tools {
			b.WriteString(toolLine(t))
		}
	}

	if len(r.Background) > 0 {
		parts := make([]string, len(r.Background))
		for i, k := range r.Background {
			parts[i] = fmt.Sprintf("`%s` %d", k.Key, k.Calls)
		}
		fmt.Fprintf(&b, "\nBackground runs (real runtime not in the payload, not sinks): %s\n", strings.Join(parts, ", "))
	}

	b.WriteString("\n## Time-sink candidates\n\n")
	if len(r.Sinks) == 0 {
		b.WriteString("None.\n")
	}
	for i, s := range r.Sinks {
		filed := ""
		if len(s.Filed) > 0 {
			ids := make([]string, len(s.Filed))
			for j, f := range s.Filed {
				ids[j] = fmt.Sprintf("#%s (%s)", f.TaskID, f.Week)
			}
			filed = " · filed: " + strings.Join(ids, ", ")
		}
		fmt.Fprintf(&b, "%d. `%s`: %s%s\n", i+1, s.Key, s.Title, filed)
	}

	b.WriteString("\n## Unmapped tools\n\nNot in any job in jobs.json, by total time.\n\n")
	for _, u := range r.Unmapped {
		fmt.Fprintf(&b, "- `%s`: %d calls, %s\n", u.Key, u.Calls, fmtDur(u.TotalMS))
	}
	return b.String()
}

func runFiled(home, week, key, task string, now time.Time) error {
	db, err := openRW(home)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO telemetry_filed (week, sink_key, task_id, filed_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (week, sink_key) DO UPDATE SET task_id = excluded.task_id, filed_at = excluded.filed_at`,
		week, key, task, now.UTC().Format(tsLayout))
	return err
}

func runMarkDone(home, week string, now time.Time) error {
	db, err := openRW(home)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`INSERT OR REPLACE INTO telemetry_weeks (week, done_at) VALUES (?, ?)`,
		week, now.UTC().Format(tsLayout))
	return err
}

func openRW(home string) (*sql.DB, error) {
	p, err := dbPath(home)
	if err != nil {
		return nil, err
	}
	db, err := openDB(p, 5000)
	if err != nil {
		return nil, err
	}
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
