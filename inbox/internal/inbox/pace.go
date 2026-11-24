package inbox

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
)

// schemaV14 keeps the sends made to each target (for pacing) and the retry
// state of a target whose last send was refused as transient. Additive.
const schemaV14 = `
CREATE TABLE send_log (
    target TEXT NOT NULL,
    at TEXT NOT NULL
);
CREATE INDEX send_log_target ON send_log (target, at);
CREATE TABLE send_retry (
    target TEXT PRIMARY KEY,
    attempts INTEGER NOT NULL,
    first_at TEXT NOT NULL,
    next_at TEXT NOT NULL,
    error TEXT NOT NULL
);
`

const (
	// PaceWindow and PaceMax keep sends to one session under agent-manager's
	// limit of five a minute from one sender, with one to spare.
	PaceWindow = time.Minute
	PaceMax    = 4
	// RetryGiveUp is how long a transient refusal is retried before the send
	// shows as failed.
	RetryGiveUp = 30 * time.Minute
	// Deferred starts the error of a send held back to be retried.
	Deferred = "retrying"
	// PaceOffEnv set to "off" skips pacing, for the golden run's frozen clock.
	PaceOffEnv = "ZEN_INBOX_PACE"
)

// IsDeferred is whether a stored delivery error is a held-back send, not a
// target that is down.
func IsDeferred(errText string) bool { return strings.HasPrefix(errText, Deferred) }

// backoff is the wait after the nth refused attempt: 15 s, 30 s, then 60 s.
func backoff(attempts int) time.Duration {
	switch attempts {
	case 1:
		return 15 * time.Second
	case 2:
		return 30 * time.Second
	}
	return time.Minute
}

func clock(t time.Time) string { return t.Local().Format("15:04:05") }

// SendHold is why target gets no send now ("" when it may): a retry backoff
// still running, or the pacing window full.
func (ib *Inbox) SendHold(target string) (string, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.sendHold(target)
}

func (ib *Inbox) sendHold(target string) (string, error) {
	now := Now()
	var attempts int
	var next, last string
	err := ib.conn.QueryRowContext(ctx, "SELECT attempts, next_at, error FROM send_retry WHERE target = ?", target).
		Scan(&attempts, &next, &last)
	switch {
	case err == nil:
		if t, ok := ParseStamp(next); ok && t.After(now) {
			return fmt.Sprintf("%s (attempt %d, next %s): %s", Deferred, attempts, clock(t), last), nil
		}
	case !errors.Is(err, sql.ErrNoRows):
		return "", dbErr(err)
	}
	if os.Getenv(PaceOffEnv) == "off" {
		return "", nil
	}
	var n int
	var oldest string
	err = ib.conn.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(MIN(at), '') FROM send_log WHERE target = ? AND at > ?",
		target, Stamp(now.Add(-PaceWindow))).Scan(&n, &oldest)
	if err != nil {
		return "", dbErr(err)
	}
	if n >= PaceMax {
		if t, ok := ParseStamp(oldest); ok {
			return fmt.Sprintf("%s: %d sent in the last minute, next %s", Deferred, n, clock(t.Add(PaceWindow))), nil
		}
	}
	return "", nil
}

// attempt runs one send to target under pacing and retry. deferred means the
// send was held or refused as transient and will be retried (errText says
// why); otherwise ok or a final failure.
func (ib *Inbox) attempt(target string, send func() (ok bool, mid, errText string)) (ok bool, mid, errText string, deferred bool, err error) {
	ib.mu.Lock()
	hold, err := ib.sendHold(target)
	if err == nil && hold == "" {
		err = ib.immediate(func() error {
			now := stamp()
			if e := ib.exec("DELETE FROM send_log WHERE at < ?", Stamp(Now().Add(-PaceWindow))); e != nil {
				return e
			}
			return ib.exec("INSERT INTO send_log (target, at) VALUES (?, ?)", target, now)
		})
	}
	ib.mu.Unlock()
	if err != nil || hold != "" {
		return false, "", hold, hold != "", err
	}
	ok, mid, errText = send()
	ib.mu.Lock()
	defer ib.mu.Unlock()
	switch {
	case ok || !am.Transient(errText):
		return ok, mid, errText, false, ib.immediate(func() error {
			return ib.exec("DELETE FROM send_retry WHERE target = ?", target)
		})
	}
	raw := errText
	err = ib.immediate(func() error {
		now := Now()
		attempts, first := 1, Stamp(now)
		var f string
		e := ib.conn.QueryRowContext(ctx, "SELECT attempts + 1, first_at FROM send_retry WHERE target = ?", target).Scan(&attempts, &f)
		if e == nil {
			first = f
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if t, ok := ParseStamp(first); ok && now.Sub(t) >= RetryGiveUp {
			errText = fmt.Sprintf("gave up after %d attempts: %s", attempts, errText)
			return ib.exec("DELETE FROM send_retry WHERE target = ?", target)
		}
		next := now.Add(backoff(attempts))
		deferred = true
		errText = fmt.Sprintf("%s (attempt %d, next %s): %s", Deferred, attempts, clock(next), errText)
		return ib.exec("INSERT INTO send_retry (target, attempts, first_at, next_at, error) VALUES (?, ?, ?, ?, ?)"+
			" ON CONFLICT(target) DO UPDATE SET attempts = excluded.attempts, next_at = excluded.next_at, error = excluded.error",
			target, attempts, first, Stamp(next), raw)
	})
	return false, "", errText, deferred, err
}
