// Package inbox is the decision store: schema, IDs, guarded transitions and
// batched delivery.
package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

const (
	SchemaVersion = 4
	Letters       = "abcd"
	// A 'sending' row older than this is a crashed send, not one in flight.
	StaleSeconds = 2 * 30
)

const schemaV1 = `
CREATE TABLE decisions (
    id TEXT PRIMARY KEY,
    task TEXT NOT NULL,
    seq INTEGER NOT NULL,
    gate INTEGER NOT NULL DEFAULT 0,
    session_id TEXT NOT NULL,
    session_name TEXT NOT NULL,
    question TEXT NOT NULL,
    context TEXT NOT NULL DEFAULT '',
    rec TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    amended INTEGER NOT NULL DEFAULT 0,
    answer_kind TEXT NOT NULL DEFAULT '',
    answer_option TEXT NOT NULL DEFAULT '',
    answer_text TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    answered_at TEXT NOT NULL DEFAULT '',
    delivery TEXT NOT NULL DEFAULT 'none',
    message_id TEXT NOT NULL DEFAULT '',
    delivery_error TEXT NOT NULL DEFAULT '',
    rev INTEGER NOT NULL DEFAULT 0,
    UNIQUE (task, gate, seq)
);
CREATE TABLE decision_options (
    decision_id TEXT NOT NULL REFERENCES decisions(id),
    letter TEXT NOT NULL,
    text TEXT NOT NULL,
    PRIMARY KEY (decision_id, letter)
);
CREATE INDEX decisions_session ON decisions (session_id, status);
`

const schemaV2 = `
CREATE TABLE decision_briefs (
    session_id TEXT NOT NULL,
    task TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    brief TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (session_id, task)
);
`

// schemaV3 binds a prod-action ask to what it approves; used_at makes it
// single-use. No decisions columns change, so a v2 reader still works.
const schemaV3 = `
CREATE TABLE prod_actions (
    decision_id TEXT PRIMARY KEY REFERENCES decisions(id),
    action TEXT NOT NULL,
    repo TEXT NOT NULL,
    branch TEXT NOT NULL,
    sha TEXT NOT NULL,
    used_at TEXT NOT NULL DEFAULT '',
    used_by TEXT NOT NULL DEFAULT ''
);
`

// Migrations maps a schema version to the SQL that reaches it, so fixtures
// can build a db of any version.
var Migrations = map[int]string{1: schemaV1, 2: schemaV2, 3: schemaV3, 4: schemaV4}

// Error carries the exit code the CLI reports it with.
type Error struct {
	Msg     string
	Code    int
	Amended bool
}

func (e *Error) Error() string { return e.Msg }

func errf(code int, format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Code: code}
}

// Now is the clock; ZEN_INBOX_NOW pins it for tests.
func Now() time.Time {
	if v := os.Getenv("ZEN_INBOX_NOW"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

// Stamp formats like Python's isoformat(timespec="seconds") in UTC; retry's
// stale cutoff compares these as strings.
func Stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05+00:00") }

func stamp() string { return Stamp(Now()) }

// ParseStamp reads a stored timestamp the way datetime.fromisoformat would.
func ParseStamp(s string) (time.Time, bool) {
	for _, layout := range []string{
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05.999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999Z07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Decision is one row with its options and its session+task brief.
type Decision struct {
	ID            string
	Task          string
	Seq           int64
	Gate          bool
	SessionID     string
	SessionName   string
	Question      string
	Context       string
	Rec           string
	Status        string
	Amended       bool
	AnswerKind    string
	AnswerOption  string
	AnswerText    string
	Note          string
	CreatedAt     string
	UpdatedAt     string
	AnsweredAt    string
	Delivery      string
	MessageID     string
	DeliveryError string
	Options       []Option
	Rev           int64
	Title         string
	Brief         string
	// Prod is set on a prod-action ask.
	Prod *ProdAction
}

type Option struct{ Letter, Text string }

func (d Decision) OptionText(letter string) string {
	for _, o := range d.Options {
		if o.Letter == letter {
			return o.Text
		}
	}
	return ""
}

// JSON is the decision's --json shape; key order is part of the output.
func (d Decision) JSON() pyfmt.Object {
	opts := make([]pyfmt.Object, len(d.Options))
	for i, o := range d.Options {
		opts[i] = pyfmt.Object{{Key: "letter", Value: o.Letter}, {Key: "text", Value: o.Text}}
	}
	return pyfmt.Object{
		{Key: "id", Value: d.ID},
		{Key: "task", Value: d.Task},
		{Key: "seq", Value: d.Seq},
		{Key: "gate", Value: d.Gate},
		{Key: "session_id", Value: d.SessionID},
		{Key: "session_name", Value: d.SessionName},
		{Key: "question", Value: d.Question},
		{Key: "context", Value: d.Context},
		{Key: "rec", Value: d.Rec},
		{Key: "status", Value: d.Status},
		{Key: "amended", Value: d.Amended},
		{Key: "answer_kind", Value: d.AnswerKind},
		{Key: "answer_option", Value: d.AnswerOption},
		{Key: "answer_text", Value: d.AnswerText},
		{Key: "note", Value: d.Note},
		{Key: "created_at", Value: d.CreatedAt},
		{Key: "updated_at", Value: d.UpdatedAt},
		{Key: "answered_at", Value: d.AnsweredAt},
		{Key: "delivery", Value: d.Delivery},
		{Key: "message_id", Value: d.MessageID},
		{Key: "delivery_error", Value: d.DeliveryError},
		{Key: "options", Value: opts},
		{Key: "rev", Value: d.Rev},
		{Key: "title", Value: d.Title},
		{Key: "brief", Value: d.Brief},
	}
}

// Delivery is the outcome of one send.
type Delivery struct {
	SessionID string
	IDs       []string
	OK        bool
	MessageID string
	Error     string
	// Message is set when the answers were handed back to print, not sent.
	Message string
	// Followup is am.Submitted or am.StillStuck when the pane needed a nudge.
	Followup string
}

// Inbox is one open db. Its mutex serialises the single connection, so a
// BEGIN IMMEDIATE and its COMMIT never interleave with another goroutine's.
type Inbox struct {
	mu   sync.Mutex
	db   *sql.DB
	conn *sql.Conn
	AM   am.Client
	Path string
	// Self is the calling agent session; answers for it are recorded, never sent.
	Self string
	// User permits answering a prod-action ask. Only the panel sets it, after
	// guard.Current passed; no flag or env var reaches it.
	User bool
}

var ctx = context.Background()

func expandUser(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

// DBPath is $ZEN_FLOW_DB, else <vault>/config/zen-flow.db.
func DBPath(home string) (string, error) {
	if v := os.Getenv("ZEN_FLOW_DB"); v != "" {
		return expandUser(v), nil
	}
	brain := filepath.Join(home, ".claude", "second-brain.json")
	raw, err := os.ReadFile(brain)
	if err != nil {
		return "", errf(4, "vault config unreadable: %s: %s", brain, osError(err, brain))
	}
	var cfg any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", errf(4, "vault config unreadable: %s: %s", brain, err)
	}
	obj, _ := cfg.(map[string]any)
	vault, ok := obj["vault"].(string)
	if !ok {
		return "", errf(4, "no vault in %s", brain)
	}
	config := filepath.Join(expandUser(vault), "config")
	if st, err := os.Stat(config); err != nil || !st.IsDir() {
		return "", errf(4, "vault config dir missing: %s", config)
	}
	return filepath.Join(config, "zen-flow.db"), nil
}

// osError renders like Python's OSError str: "[Errno 2] No such file ...: 'path'".
func osError(err error, path string) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		msg := errno.Error()
		if msg != "" {
			msg = strings.ToUpper(msg[:1]) + msg[1:]
		}
		return fmt.Sprintf("[Errno %d] %s: %s", int(errno), msg, pyfmt.Repr(path))
	}
	return err.Error()
}

// lockDir takes an exclusive flock on dir. Converting a fresh db to WAL
// returns SQLITE_BUSY without consulting busy_timeout, so concurrent cold
// starts must queue here instead.
func lockDir(dir string) (unlock func(), err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// Open connects, sets WAL and busy_timeout, and migrates under BEGIN IMMEDIATE.
func Open(path string, client am.Client) (*Inbox, error) {
	fail := func(err error) error { return errf(4, "db unavailable: %s: %s", path, err) }
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fail(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fail(err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fail(err)
	}
	ib := &Inbox{db: db, conn: conn, AM: client, Path: path}
	unlock, err := lockDir(filepath.Dir(path))
	if err != nil {
		_ = ib.Close()
		return nil, fail(err)
	}
	err = ib.setup()
	unlock()
	if err != nil {
		_ = ib.Close()
		return nil, fail(err)
	}
	return ib, nil
}

func (ib *Inbox) setup() error {
	for _, pragma := range []string{"PRAGMA busy_timeout = 5000", "PRAGMA journal_mode = WAL"} {
		if _, err := ib.conn.ExecContext(ctx, pragma); err != nil {
			return err
		}
	}
	version, err := ib.userVersion()
	if err != nil || version >= SchemaVersion {
		return err
	}
	if _, err := ib.conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	err = ib.migrateLocked()
	if err == nil {
		_, err = ib.conn.ExecContext(ctx, "COMMIT")
	}
	if err != nil {
		_, _ = ib.conn.ExecContext(ctx, "ROLLBACK")
	}
	return err
}

func (ib *Inbox) userVersion() (int, error) {
	var v int
	err := ib.conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

func (ib *Inbox) migrateLocked() error {
	version, err := ib.userVersion()
	if err != nil || version >= SchemaVersion {
		return err
	}
	for target := version + 1; target <= SchemaVersion; target++ {
		for _, stmt := range strings.Split(Migrations[target], ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := ib.conn.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
	}
	_, err = ib.conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion))
	return err
}

func (ib *Inbox) Close() error {
	if ib.conn != nil {
		_ = ib.conn.Close()
	}
	return ib.db.Close()
}

func dbErr(err error) error {
	if err == nil {
		return nil
	}
	var ie *Error
	if errors.As(err, &ie) {
		return err
	}
	return errf(4, "db error: %s", err)
}

// immediate runs fn inside BEGIN IMMEDIATE ... COMMIT, rolled back on error.
// The caller holds ib.mu.
func (ib *Inbox) immediate(fn func() error) error {
	if _, err := ib.conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return dbErr(err)
	}
	if err := fn(); err != nil {
		_, _ = ib.conn.ExecContext(ctx, "ROLLBACK")
		return dbErr(err)
	}
	if _, err := ib.conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = ib.conn.ExecContext(ctx, "ROLLBACK")
		return dbErr(err)
	}
	return nil
}

func (ib *Inbox) exec(query string, args ...any) error {
	_, err := ib.conn.ExecContext(ctx, query, args...)
	return err
}

// Exec runs one statement outside the inbox's operations (tests, fixtures).
func (ib *Inbox) Exec(query string, args ...any) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.exec(query, args...)
}

const columns = "id, task, seq, gate, session_id, session_name, question, context, rec," +
	" status, amended, answer_kind, answer_option, answer_text, note, created_at," +
	" updated_at, answered_at, delivery, message_id, delivery_error, rev"

func (ib *Inbox) sel(where string, args ...any) ([]Decision, error) {
	rows, err := ib.conn.QueryContext(ctx, "SELECT "+columns+" FROM decisions WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	var out []Decision
	for rows.Next() {
		var d Decision
		var gate, amended int64
		if err := rows.Scan(&d.ID, &d.Task, &d.Seq, &gate, &d.SessionID, &d.SessionName,
			&d.Question, &d.Context, &d.Rec, &d.Status, &amended, &d.AnswerKind,
			&d.AnswerOption, &d.AnswerText, &d.Note, &d.CreatedAt, &d.UpdatedAt,
			&d.AnsweredAt, &d.Delivery, &d.MessageID, &d.DeliveryError, &d.Rev); err != nil {
			_ = rows.Close()
			return nil, err
		}
		d.Gate, d.Amended = gate != 0, amended != 0
		out = append(out, d)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The connection is single: rows must be closed before these lookups.
	for i := range out {
		if err := ib.fill(&out[i]); err != nil {
			return nil, err
		}
	}
	SortByID(out)
	return out, nil
}

func (ib *Inbox) fill(d *Decision) error {
	if p, ok, err := ib.prodAction(d.ID); err != nil {
		return err
	} else if ok {
		d.Prod = &p
	}
	rows, err := ib.conn.QueryContext(ctx,
		"SELECT letter, text FROM decision_options WHERE decision_id = ? ORDER BY letter", d.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var o Option
		if err := rows.Scan(&o.Letter, &o.Text); err != nil {
			_ = rows.Close()
			return err
		}
		d.Options = append(d.Options, o)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	err = ib.conn.QueryRowContext(ctx,
		"SELECT title, brief FROM decision_briefs WHERE session_id = ? AND task = ?",
		d.SessionID, d.Task).Scan(&d.Title, &d.Brief)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

// Select is every decision matching a SQL condition, in ID order.
func (ib *Inbox) Select(where string, args ...any) ([]Decision, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	ds, err := ib.sel(where, args...)
	return ds, dbErr(err)
}

func (ib *Inbox) get(id string) (Decision, error) {
	found, err := ib.sel("id = ?", id)
	if err != nil {
		return Decision{}, err
	}
	if len(found) == 0 {
		return Decision{}, errf(1, "no decision %s", id)
	}
	return found[0], nil
}

func (ib *Inbox) Get(id string) (Decision, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	d, err := ib.get(id)
	return d, dbErr(err)
}

func (ib *Inbox) sessionOpen(sid string) ([]Decision, error) {
	return ib.sel("session_id = ? AND status = 'open'", sid)
}

func (ib *Inbox) SessionOpen(sid string) ([]Decision, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	ds, err := ib.sessionOpen(sid)
	return ds, dbErr(err)
}

func ids(ds []Decision) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

func (ib *Inbox) openIDs(sid string) ([]string, error) {
	ds, err := ib.sessionOpen(sid)
	return ids(ds), err
}

func (ib *Inbox) OpenIDs(sid string) ([]string, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	out, err := ib.openIDs(sid)
	return out, dbErr(err)
}

// ---------------------------------------------------------------- ID order --

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// cmpNum compares two digit strings by value without overflow.
func cmpNum(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}

type idKey struct {
	numTask bool
	task    string
	gate    bool
	n       string
}

func keyOf(id string) idKey {
	task, suffix := "", id
	if i := strings.LastIndex(id, "."); i >= 0 {
		task, suffix = id[:i], id[i+1:]
	}
	k := idKey{numTask: digits(task), task: task}
	switch {
	case strings.HasPrefix(suffix, "g"):
		k.gate, k.n = true, suffix[1:]
		if k.n == "" {
			k.n = "1"
		}
	case digits(suffix):
		k.n = suffix
	default:
		k.n = "0"
	}
	return k
}

// CompareID orders IDs: numeric tasks first by value, then
// plain IDs before gates, then the sequence number by value.
func CompareID(a, b string) int {
	ka, kb := keyOf(a), keyOf(b)
	if ka.numTask != kb.numTask {
		if ka.numTask {
			return -1
		}
		return 1
	}
	c := strings.Compare(ka.task, kb.task)
	if ka.numTask {
		c = cmpNum(ka.task, kb.task)
	}
	if c != 0 {
		return c
	}
	if ka.gate != kb.gate {
		if kb.gate {
			return -1
		}
		return 1
	}
	return cmpNum(ka.n, kb.n)
}

func SortByID(ds []Decision) {
	sort.SliceStable(ds, func(i, j int) bool { return CompareID(ds[i].ID, ds[j].ID) < 0 })
}

func SortIDs(s []string) {
	sort.SliceStable(s, func(i, j int) bool { return CompareID(s[i], s[j]) < 0 })
}
