package inbox

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

// schemaV15 logs agent-manager delivery problems. Additive: a v14 reader
// never names the table.
const schemaV15 = `
CREATE TABLE delivery_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    source TEXT NOT NULL,
    source_id TEXT NOT NULL,
    sender TEXT NOT NULL DEFAULT '',
    recipient_id TEXT NOT NULL DEFAULT '',
    recipient_name TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    sent_at TEXT NOT NULL DEFAULT '',
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'open',
    resolution TEXT NOT NULL DEFAULT '',
    closed_at TEXT NOT NULL DEFAULT '',
    UNIQUE (source, source_id)
);
CREATE INDEX delivery_log_state ON delivery_log (state, closed_at);
`

const (
	KindLost       = "lost"
	KindFalseDrop  = "false_drop"
	KindRefused    = "refused"
	KindHeld       = "held"
	KindUnverified = "unverified"

	SourceInbox     = "am_inbox"
	SourceSend      = "zen_inbox_send"
	SourceTelemetry = "telemetry"
	SourceStatus    = "status_poll"

	// HeldAfter is how long an undelivered message waits before it is held.
	HeldAfter = 5 * time.Minute
	// DeliveryKeep is how long a closed entry is kept.
	DeliveryKeep = 30 * 24 * time.Hour
	// RetryWithin is how soon a sender's successful send_session closes its refusal.
	RetryWithin = 10 * time.Minute
	// telemetryWindow is how far back a scan reads failed send_session calls.
	telemetryWindow = 24 * time.Hour
	// telemetryTS is zen-telemetry's ts layout; it compares as a string.
	telemetryTS = "2006-01-02T15:04:05.000Z"
)

// Entry is one delivery_log row.
type Entry struct {
	ID                                       int64
	Kind, Source, SourceID, Sender           string
	RecipientID, RecipientName, Body, Reason string
	SentAt, FirstSeen, LastSeen              string
	State, Resolution, ClosedAt              string
}

// Open is whether the entry still asks for action.
func (e Entry) Open() bool { return e.State == "open" }

// Recipient is the recipient's name, else its id, else "?".
func (e Entry) Recipient() string {
	switch {
	case e.RecipientName != "":
		return e.RecipientName
	case e.RecipientID != "":
		return e.RecipientID
	}
	return "?"
}

// When is when the message was sent, else when the entry was first seen.
func (e Entry) When() string {
	if e.SentAt != "" {
		return e.SentAt
	}
	return e.FirstSeen
}

// JSON is the entry's --json shape; key order is part of the output.
func (e Entry) JSON() pyfmt.Object {
	return pyfmt.Object{
		{Key: "id", Value: e.ID}, {Key: "kind", Value: e.Kind}, {Key: "source", Value: e.Source},
		{Key: "source_id", Value: e.SourceID}, {Key: "sender", Value: e.Sender},
		{Key: "recipient_id", Value: e.RecipientID}, {Key: "recipient_name", Value: e.RecipientName},
		{Key: "body", Value: e.Body}, {Key: "reason", Value: e.Reason}, {Key: "sent_at", Value: e.SentAt},
		{Key: "first_seen", Value: e.FirstSeen}, {Key: "last_seen", Value: e.LastSeen},
		{Key: "state", Value: e.State}, {Key: "resolution", Value: e.Resolution}, {Key: "closed_at", Value: e.ClosedAt},
	}
}

const entryColumns = "id, kind, source, source_id, sender, recipient_id, recipient_name, body, reason, sent_at," +
	" first_seen, last_seen, state, resolution, closed_at"

func (ib *Inbox) selEntries(where string, args ...any) ([]Entry, error) {
	rows, err := ib.conn.QueryContext(ctx, "SELECT "+entryColumns+" FROM delivery_log WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Kind, &e.Source, &e.SourceID, &e.Sender, &e.RecipientID, &e.RecipientName,
			&e.Body, &e.Reason, &e.SentAt, &e.FirstSeen, &e.LastSeen, &e.State, &e.Resolution, &e.ClosedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Entries are the open entries, newest first; all adds the closed ones after them.
func (ib *Inbox) Entries(all bool) ([]Entry, error) {
	where := "state = 'open'"
	if all {
		where = "1"
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	es, err := ib.selEntries(where + " ORDER BY state = 'open' DESC, id DESC")
	return es, dbErr(err)
}

// Entry is one entry by id.
func (ib *Inbox) Entry(id int64) (Entry, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	es, err := ib.selEntries("id = ?", id)
	if err != nil {
		return Entry{}, dbErr(err)
	}
	if len(es) == 0 {
		return Entry{}, errf(1, "no delivery entry %d", id)
	}
	return es[0], nil
}

// FalseDrops counts the false drops first seen since t.
func (ib *Inbox) FalseDrops(since time.Time) (int, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var n int
	err := ib.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM delivery_log WHERE kind = ? AND first_seen >= ?",
		KindFalseDrop, Stamp(since)).Scan(&n)
	return n, dbErr(err)
}

// upsert records e unless its (source, source_id) is already closed. A
// false drop keeps no body; otherwise an empty body keeps the stored one.
func (ib *Inbox) upsert(e Entry) error {
	now := stamp()
	if e.State == "" {
		e.State = "open"
	}
	if e.State != "open" && e.ClosedAt == "" {
		e.ClosedAt = now
	}
	if e.Kind == KindFalseDrop {
		e.Body = ""
	}
	return ib.exec("INSERT INTO delivery_log (kind, source, source_id, sender, recipient_id, recipient_name, body, reason,"+
		" sent_at, first_seen, last_seen, state, resolution, closed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"+
		" ON CONFLICT(source, source_id) DO UPDATE SET kind = excluded.kind, reason = excluded.reason,"+
		" last_seen = excluded.last_seen, recipient_name = excluded.recipient_name,"+
		" body = CASE WHEN excluded.kind = 'false_drop' THEN '' WHEN excluded.body != '' THEN excluded.body ELSE delivery_log.body END,"+
		" state = excluded.state, resolution = excluded.resolution, closed_at = excluded.closed_at"+
		" WHERE delivery_log.state = 'open'",
		e.Kind, e.Source, e.SourceID, e.Sender, e.RecipientID, e.RecipientName, e.Body, e.Reason,
		e.SentAt, now, now, e.State, e.Resolution, e.ClosedAt)
}

// resolve closes the open entries matching where with resolution.
func (ib *Inbox) resolve(resolution, where string, args ...any) error {
	now := stamp()
	return ib.exec("UPDATE delivery_log SET state = 'resolved', resolution = ?, closed_at = ?, last_seen = ?"+
		" WHERE state = 'open' AND "+where, append([]any{resolution, now, now}, args...)...)
}

// ------------------------------------------------------------- classify --

// verdict is what one session_inbox row says about its message.
type verdict struct {
	kind, reason string
	// delivered: no problem; an open entry for it closes as delivered_late.
	delivered bool
	// pending: undelivered for less than HeldAfter; nothing is recorded.
	pending bool
}

// heldReason is why an undelivered message waits for its recipient.
func heldReason(s am.StateSession) string {
	st := strings.ToLower(s.Status)
	switch {
	case st == "dead" || st == "finished" || st == "stopped" || st == "exited":
		return "not running"
	case strings.Contains(st, "wait") || strings.Contains(st, "input") || strings.Contains(st, "permission") ||
		strings.Contains(st, "dialog") || strings.Contains(st, "ask"):
		return "a dialog on screen"
	}
	return "not delivered yet (" + s.Status + ")"
}

// classify reads one row; a dropped message to a Claude recipient is looked
// up in the recipient's transcripts, the ground truth for delivery.
func classify(r am.InboxRow, sessions map[string]am.StateSession, projects string, now time.Time) verdict {
	s, known := sessions[r.SessionID]
	switch {
	case !r.DroppedAt.IsZero():
		switch {
		case !known:
			return verdict{kind: KindUnverified, reason: "dropped; recipient session is gone"}
		case s.Tool != "claude":
			return verdict{kind: KindUnverified, reason: "dropped; recipient runs " + s.Tool}
		}
		files := am.Transcripts(projects, s, r.SentAt, sessions)
		switch {
		case len(files) == 0:
			return verdict{kind: KindUnverified, reason: "dropped; no transcript found"}
		case am.InTranscripts(files, r.Body, r.SentAt):
			return verdict{kind: KindFalseDrop, reason: "marked dropped"}
		}
		return verdict{kind: KindLost, reason: "dropped; not in its transcripts"}
	case !r.DeliveredAt.IsZero():
		return verdict{delivered: true}
	case !known:
		return verdict{kind: KindLost, reason: "undelivered; recipient session is gone"}
	case s.Archived:
		return verdict{kind: KindLost, reason: "undelivered; recipient is archived"}
	}
	return verdict{kind: KindHeld, reason: heldReason(s), pending: now.Sub(r.SentAt) < HeldAfter}
}

// inboxEntry is the entry a row's verdict records.
func inboxEntry(r am.InboxRow, sessions map[string]am.StateSession, v verdict) Entry {
	e := Entry{Kind: v.kind, Source: SourceInbox, SourceID: strconv.FormatInt(r.ID, 10), Sender: r.SenderName,
		RecipientID: r.SessionID, RecipientName: sessions[r.SessionID].Name, Body: r.Body, Reason: v.reason,
		SentAt: Stamp(r.SentAt)}
	if e.Sender == "" {
		e.Sender = r.SenderID
	}
	if v.kind == KindFalseDrop {
		e.State, e.Resolution = "resolved", "auto"
	}
	return e
}

// recordRow writes one row's verdict; the caller holds the transaction.
func (ib *Inbox) recordRow(r am.InboxRow, sessions map[string]am.StateSession, v verdict) error {
	switch {
	case v.pending:
		return nil
	case v.delivered:
		return ib.resolve("delivered_late", "source = ? AND source_id = ?", SourceInbox, strconv.FormatInt(r.ID, 10))
	}
	return ib.upsert(inboxEntry(r, sessions, v))
}

// ----------------------------------------------------------------- scan --

// ScanReport is what one scan saw.
type ScanReport struct {
	// Unavailable is why state.db was not read; nothing came from it then.
	Unavailable string
	Rows        int
	Open, Lost  int
	// FalseDropsWeek counts the false drops first seen in the last 7 days.
	FalseDropsWeek int
}

// Text is the report as one line.
func (r ScanReport) Text() string {
	head := "scanned " + plural(r.Rows, "message")
	if r.Unavailable != "" {
		head = "unavailable: " + r.Unavailable
	}
	open := plural(r.Open, "open entry")
	if r.Lost > 0 {
		open += " (" + strconv.Itoa(r.Lost) + " lost)"
	}
	return head + " · " + open + " · " + plural(r.FalseDropsWeek, "false drop") + " this week"
}

// JSON is the report's --json shape.
func (r ScanReport) JSON() pyfmt.Object {
	return pyfmt.Object{{Key: "unavailable", Value: r.Unavailable}, {Key: "rows", Value: r.Rows},
		{Key: "open", Value: r.Open}, {Key: "lost", Value: r.Lost}, {Key: "false_drops_week", Value: r.FalseDropsWeek}}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "y") {
		return strconv.Itoa(n) + " " + strings.TrimSuffix(noun, "y") + "ies"
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// readState is every session_inbox row and session, or why there are none.
func readState() ([]am.InboxRow, map[string]am.StateSession, error) {
	st, err := am.OpenState(am.StatePath())
	if err != nil {
		return nil, nil, err
	}
	defer st.Close()
	rows, err := st.Rows()
	if err != nil {
		return nil, nil, &am.Unavailable{Why: err.Error()}
	}
	sessions, err := st.Sessions()
	if err != nil {
		return nil, nil, &am.Unavailable{Why: err.Error()}
	}
	return rows, sessions, nil
}

// ScanDelivery copies agent-manager's delivery problems into delivery_log
// before its rows age out, logs agents' refused send_session calls, and
// prunes closed entries past DeliveryKeep. Transcripts are read outside the
// lock; every write is an idempotent upsert, so concurrent scans agree.
func (ib *Inbox) ScanDelivery() (ScanReport, error) {
	var rep ScanReport
	rows, sessions, err := readState()
	var u *am.Unavailable
	switch {
	case errors.As(err, &u):
		rep.Unavailable = u.Why
	case err != nil:
		return rep, err
	}
	rep.Rows = len(rows)
	ib.mu.Lock()
	closed, err := ib.closedInbox()
	ib.mu.Unlock()
	if err != nil {
		return rep, dbErr(err)
	}
	now := Now()
	type planned struct {
		r am.InboxRow
		v verdict
	}
	var plan []planned
	for _, r := range rows {
		if closed[strconv.FormatInt(r.ID, 10)] {
			continue
		}
		v := classify(r, sessions, am.ProjectsDir(), now)
		if !v.pending {
			plan = append(plan, planned{r, v})
		}
	}
	names := map[string]string{}
	for id, s := range sessions {
		names[id] = s.Name
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	err = ib.immediate(func() error {
		for _, p := range plan {
			if p.v.delivered && !closed.hasOpen(p.r.ID) {
				continue
			}
			if err := ib.recordRow(p.r, sessions, p.v); err != nil {
				return err
			}
		}
		if err := ib.scanTelemetry(names, now); err != nil {
			return err
		}
		return ib.exec("DELETE FROM delivery_log WHERE state != 'open' AND closed_at != '' AND closed_at < ?",
			Stamp(now.Add(-DeliveryKeep)))
	})
	if err != nil {
		return rep, err
	}
	err = ib.conn.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(kind = 'lost'), 0),"+
		" (SELECT COUNT(*) FROM delivery_log WHERE kind = 'false_drop' AND first_seen >= ?)"+
		" FROM delivery_log WHERE state = 'open'", Stamp(now.Add(-7*24*time.Hour))).Scan(&rep.Open, &rep.Lost, &rep.FalseDropsWeek)
	return rep, dbErr(err)
}

// inboxStates maps an am_inbox source id to whether its entry is closed.
type inboxStates map[string]bool

func (s inboxStates) hasOpen(id int64) bool {
	closed, ok := s[strconv.FormatInt(id, 10)]
	return ok && !closed
}

func (ib *Inbox) closedInbox() (inboxStates, error) {
	rows, err := ib.conn.QueryContext(ctx, "SELECT source_id, state != 'open' FROM delivery_log WHERE source = ?", SourceInbox)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := inboxStates{}
	for rows.Next() {
		var id string
		var closed bool
		if err := rows.Scan(&id, &closed); err != nil {
			return nil, err
		}
		out[id] = closed
	}
	return out, rows.Err()
}

// sendSessionCall matches zen-telemetry's rows for agent-manager's send_session tool.
const sendSessionCall = "tool LIKE '%send_session' AND mcp_server LIKE '%agent-manager%'"

// scanTelemetry logs the failed send_session calls zen-telemetry recorded
// in the last day, and closes each one the same sender followed with a
// successful send within RetryWithin. The caller holds the transaction.
func (ib *Inbox) scanTelemetry(names map[string]string, now time.Time) error {
	var n int
	if err := ib.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'tool_calls'").Scan(&n); err != nil || n == 0 {
		return err
	}
	type call struct{ id, sender, ts, errText string }
	read := func(query string, args ...any) ([]call, error) {
		rows, err := ib.conn.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []call
		for rows.Next() {
			var c call
			if err := rows.Scan(&c.id, &c.sender, &c.ts, &c.errText); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
	failed, err := read("SELECT CAST(id AS TEXT), am_session, ts, error FROM tool_calls WHERE ok = 0 AND "+sendSessionCall+
		" AND ts >= ?", now.Add(-telemetryWindow).UTC().Format(telemetryTS))
	if err != nil {
		return err
	}
	for _, c := range failed {
		sender := c.sender
		if names[sender] != "" {
			sender = names[sender]
		}
		sent := ""
		if t, err := time.Parse(telemetryTS, c.ts); err == nil {
			sent = Stamp(t)
		}
		if err := ib.exec("INSERT INTO delivery_log (kind, source, source_id, sender, recipient_id, reason, sent_at, first_seen, last_seen)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(source, source_id) DO NOTHING",
			KindRefused, SourceTelemetry, c.id, sender, "", "send_session failed: "+c.errText, sent, stamp(), stamp()); err != nil {
			return err
		}
	}
	open, err := read("SELECT CAST(t.id AS TEXT), t.am_session, t.ts, '' FROM delivery_log d JOIN tool_calls t"+
		" ON CAST(t.id AS TEXT) = d.source_id WHERE d.source = ? AND d.state = 'open'", SourceTelemetry)
	if err != nil {
		return err
	}
	for _, c := range open {
		at, err := time.Parse(telemetryTS, c.ts)
		if err != nil {
			continue
		}
		var ok int
		if err := ib.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM tool_calls WHERE ok = 1 AND am_session = ? AND "+
			sendSessionCall+" AND ts > ? AND ts <= ?", c.sender, c.ts,
			at.Add(RetryWithin).UTC().Format(telemetryTS)).Scan(&ok); err != nil {
			return err
		}
		if ok > 0 {
			if err := ib.resolve("retried", "source = ? AND source_id = ?", SourceTelemetry, c.id); err != nil {
				return err
			}
		}
	}
	return nil
}

// --------------------------------------------------------- zen-inbox's sends --

// outgoing is a message zen-inbox is about to send, for its refusal entry.
// key names the message across attempts, whose bodies can differ.
type outgoing struct{ name, body, key string }

// retryPrefix starts the source id of every retry entry of target.
func retryPrefix(target string) string { return "retry:" + target + ":" }

// retryKey is the source id of one message's entry in target's retry chain
// that started at first.
func retryKey(target, first, msgKey string) string { return retryPrefix(target) + first + ":" + msgKey }

// logRefusal records a refused send of zen-inbox's own; the caller holds
// the transaction.
func (ib *Inbox) logRefusal(sourceID, target string, msg outgoing, reason string) error {
	return ib.upsert(Entry{Kind: KindRefused, Source: SourceSend, SourceID: sourceID, Sender: "zen-inbox",
		RecipientID: target, RecipientName: msg.name, Body: msg.body, Reason: reason, SentAt: stamp()})
}

// NoteRefusal records a send of zen-inbox's own that ended failed.
func (ib *Inbox) NoteRefusal(sourceID, target string, name, body, reason string) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.immediate(func() error { return ib.logRefusal(sourceID, target, outgoing{name: name, body: body}, reason) })
}

// closeRefusals closes the open refusals whose source id starts with prefix
// as retried.
func (ib *Inbox) closeRefusals(prefix string) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.immediate(func() error {
		return ib.resolve("retried", "source = ? AND substr(source_id, 1, ?) = ?", SourceSend, len(prefix), prefix)
	})
}

// ----------------------------------------------------------- check, act --

// Check is what `delivery check` says about one agent-manager message.
type Check struct {
	// Status is delivered, false drop, lost, held: <why>, unverified: <why> or unknown.
	Status string
	// Why is set on unknown when state.db could not be read.
	Why   string
	Entry *Entry
}

func statusOf(kind, reason string) string {
	switch kind {
	case KindFalseDrop:
		return "false drop"
	case KindLost:
		return "lost"
	case KindHeld:
		return "held: " + reason
	case KindUnverified:
		return "unverified: " + reason
	case KindRefused:
		return "refused: " + reason
	}
	return "delivered"
}

// CheckMessage classifies one agent-manager message, whoever sent it, and
// records what it finds as a scan would.
func (ib *Inbox) CheckMessage(messageID string) (Check, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(messageID), 10, 64)
	if err != nil {
		return Check{Status: "unknown"}, nil
	}
	st, err := am.OpenState(am.StatePath())
	var u *am.Unavailable
	var why string
	switch {
	case errors.As(err, &u):
		why = u.Why
	case err != nil:
		return Check{}, err
	default:
		defer st.Close()
		r, found, err := st.Row(id)
		if err != nil {
			return Check{}, &am.Unavailable{Why: err.Error()}
		}
		if found {
			sessions, err := st.Sessions()
			if err != nil {
				return Check{}, &am.Unavailable{Why: err.Error()}
			}
			v := classify(r, sessions, am.ProjectsDir(), Now())
			ib.mu.Lock()
			err = ib.immediate(func() error { return ib.recordRow(r, sessions, v) })
			ib.mu.Unlock()
			if err != nil {
				return Check{}, err
			}
			c := Check{Status: statusOf(v.kind, v.reason)}
			if v.delivered {
				c.Status = "delivered"
			}
			if e, ok, err := ib.inboxEntry(strconv.FormatInt(id, 10)); err != nil {
				return Check{}, err
			} else if ok {
				c.Entry = &e
			}
			return c, nil
		}
	}
	e, ok, err := ib.inboxEntry(strconv.FormatInt(id, 10))
	switch {
	case err != nil:
		return Check{}, err
	case !ok:
		return Check{Status: "unknown", Why: why}, nil
	case e.Resolution == "delivered_late":
		return Check{Status: "delivered", Entry: &e}, nil
	}
	return Check{Status: statusOf(e.Kind, e.Reason), Entry: &e}, nil
}

func (ib *Inbox) inboxEntry(sourceID string) (Entry, bool, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	es, err := ib.selEntries("source = ? AND source_id = ?", SourceInbox, strings.TrimSpace(sourceID))
	if err != nil || len(es) == 0 {
		return Entry{}, false, dbErr(err)
	}
	return es[0], true, nil
}

// LogStatusDrop records a message agent-manager's status says was dropped
// when state.db cannot tell more: unverified, with the body for a resend.
func (ib *Inbox) LogStatusDrop(messageID, target, name, body, why string) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.immediate(func() error {
		return ib.upsert(Entry{Kind: KindUnverified, Source: SourceStatus, SourceID: messageID, Sender: "zen-inbox",
			RecipientID: target, RecipientName: name, Body: body, Reason: "message-status says dropped; " + why})
	})
}

// Dismiss closes an open entry without acting on it.
func (ib *Inbox) Dismiss(id int64) (Entry, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var e Entry
	err := ib.immediate(func() error {
		es, err := ib.selEntries("id = ?", id)
		switch {
		case err != nil:
			return err
		case len(es) == 0:
			return errf(1, "no delivery entry %d", id)
		case !es[0].Open():
			return errf(1, "delivery entry %d is already %s", id, es[0].State)
		}
		now := stamp()
		e = es[0]
		e.State, e.Resolution, e.ClosedAt = "dismissed", "dismissed", now
		return ib.exec("UPDATE delivery_log SET state = 'dismissed', resolution = 'dismissed', closed_at = ?, last_seen = ?"+
			" WHERE id = ?", now, now, id)
	})
	return e, err
}

// ResendMessage is what a resend delivers: the original body after a line
// naming whose message it was and when.
func ResendMessage(e Entry) string {
	when := "?"
	if t, ok := ParseStamp(e.When()); ok {
		when = t.Local().Format("15:04")
	}
	return fmt.Sprintf("(resent from the cockpit: %s's message %s of %s) %s", e.Sender, e.SourceID, when, e.Body)
}

// meantFor names an entry's recipient as "NAME (SID)", or whichever it has.
func (e Entry) meantFor() string {
	if e.RecipientName != "" && e.RecipientID != "" {
		return e.RecipientName + " (" + e.RecipientID + ")"
	}
	return e.Recipient()
}

// ResendToChiefMessage is ResendMessage for a copy that goes to chief.
func ResendToChiefMessage(e Entry) string {
	msg := ResendMessage(e)
	return strings.Replace(msg, " of ", ", meant for "+e.meantFor()+", of ", 1)
}

// resendable is the open entry with a body to send again.
func (ib *Inbox) resendable(id int64) (Entry, error) {
	e, err := ib.Entry(id)
	switch {
	case err != nil:
		return e, err
	case !e.Open():
		return e, errf(1, "delivery entry %d is already %s", id, e.State)
	case e.Body == "":
		return e, errf(1, "delivery entry %d has no body to resend", id)
	}
	return e, nil
}

// Resend sends an open entry's body again: to its recipient when live, else
// to the one live session with its name, under pacing and retry. A sent one
// closes as resent; a recipient that is down or a refusal is exit 3.
func (ib *Inbox) Resend(id int64) (Entry, string, error) {
	e, err := ib.resendable(id)
	if err != nil {
		return e, "", err
	}
	sessions := ib.AM.Sessions()
	if sessions == nil {
		return e, "", errf(3, "agent-manager could not list sessions")
	}
	target := ""
	if e.RecipientID != "" && am.Live(am.Find(sessions, e.RecipientID)) {
		target = e.RecipientID
	} else if live := am.LiveNamed(sessions, e.RecipientName); len(live) == 1 {
		target = live[0]
	} else if len(live) > 1 {
		return e, "", errf(3, "%d live sessions are named %s: resend by hand", len(live), e.RecipientName)
	}
	if target == "" {
		return e, "", errf(3, "%s is down: nothing resent", e.Recipient())
	}
	return ib.resendTo(e, target, e.RecipientName, ResendMessage(e), e.Recipient())
}

// ResendToChief sends an open entry's body to the one live session named
// chief, whatever kept it from its recipient; it refuses when chief is not
// exactly one, leaving the entry open.
func (ib *Inbox) ResendToChief(id int64) (Entry, string, error) {
	e, err := ib.resendable(id)
	if err != nil {
		return e, "", err
	}
	sessions := ib.AM.Sessions()
	if sessions == nil {
		return e, "", errf(3, "agent-manager could not list sessions")
	}
	chief := chiefLive(sessions)
	if len(chief) != 1 {
		return e, "", errf(1, "need exactly one live session named %s, found %d", am.ChiefName, len(chief))
	}
	return ib.resendTo(e, chief[0], am.ChiefName, ResendToChiefMessage(e), am.ChiefName)
}

// resendTo sends msg to target and closes the entry as resent.
func (ib *Inbox) resendTo(e Entry, target, name, msg, label string) (Entry, string, error) {
	ok, mid, errText, _, err := ib.attempt(target, outgoing{name: name, body: msg, key: "resend:" + strconv.FormatInt(e.ID, 10)}, func() (bool, string, string) {
		return ib.AM.Send(target, msg)
	})
	if err != nil {
		return e, "", err
	}
	if !ok {
		return e, "", errf(3, "resend to %s: %s", label, errText)
	}
	ib.mu.Lock()
	err = ib.immediate(func() error { return ib.resolve("resent", "id = ?", e.ID) })
	ib.mu.Unlock()
	if err != nil {
		return e, "", err
	}
	e.State, e.Resolution = "resolved", "resent"
	text := "resent entry " + strconv.FormatInt(e.ID, 10) + " to " + label
	if mid != "" {
		text += " as message " + mid
	}
	return e, text, nil
}

// StatusLost is a chat's status once its message is proven lost.
const StatusLost = "lost"
