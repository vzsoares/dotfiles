package inbox

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

// schemaV5 adds chief's notes. No existing table changes, so a v4 reader
// still works. AUTOINCREMENT keeps a pruned note's id from being reused.
const schemaV5 = `
CREATE TABLE notes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL,
    session_name TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    read_at TEXT NOT NULL DEFAULT '',
    reply TEXT NOT NULL DEFAULT '',
    replied_at TEXT NOT NULL DEFAULT '',
    delivery TEXT NOT NULL DEFAULT 'none',
    message_id TEXT NOT NULL DEFAULT '',
    delivery_error TEXT NOT NULL DEFAULT ''
);
`

// schemaV7 splits the claim clock from the reply time: replied_at stays when
// the user replied, claimed_at is when a send last took the reply. A v6
// reader still works but a retry there rewrites replied_at.
const schemaV7 = `
ALTER TABLE notes ADD COLUMN claimed_at TEXT NOT NULL DEFAULT '';
UPDATE notes SET claimed_at = replied_at WHERE reply != '';
`

const (
	NoteSender   = "chief"
	TitleMax     = 120
	BodyMax      = 16 * 1024
	VisibleFor   = 24 * time.Hour
	KeepFor      = 30 * 24 * time.Hour
	noteColumns  = "id, task, session_id, session_name, title, body, created_at, read_at, reply, replied_at, claimed_at, delivery, message_id, delivery_error"
	visibleNotes = "(read_at = '' OR read_at >= ? OR delivery IN ('sending', 'failed'))"
	// failedNotes is a failed reply, or a claim older than StaleSeconds. A v6
	// binary claims through replied_at only, so the later of the two counts.
	failedNotes = "(delivery = 'failed' OR (delivery = 'sending' AND max(claimed_at, replied_at) < ?))"
)

// Note is one message from chief; ID is shown as n<ID>.
type Note struct {
	ID                                  int64
	Task, SessionID, SessionName        string
	Title, Body                         string
	CreatedAt, ReadAt, Reply, RepliedAt string
	ClaimedAt                           string
	Delivery, MessageID, DeliveryError  string
}

func (n Note) Label() string { return "n" + strconv.FormatInt(n.ID, 10) }

func (n Note) Unread() bool { return n.ReadAt == "" }

// JSON is the note's --json shape; key order is part of the output.
func (n Note) JSON() pyfmt.Object {
	return pyfmt.Object{
		{Key: "id", Value: n.Label()},
		{Key: "task", Value: n.Task},
		{Key: "session_id", Value: n.SessionID},
		{Key: "session_name", Value: n.SessionName},
		{Key: "title", Value: n.Title},
		{Key: "body", Value: n.Body},
		{Key: "created_at", Value: n.CreatedAt},
		{Key: "read_at", Value: n.ReadAt},
		{Key: "reply", Value: n.Reply},
		{Key: "replied_at", Value: n.RepliedAt},
		{Key: "delivery", Value: n.Delivery},
	}
}

// ParseNoteID reads n12 or 12.
func ParseNoteID(s string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(s, "n"), 10, 64)
	if err != nil || id <= 0 || !digits(strings.TrimPrefix(s, "n")) {
		return 0, errf(2, "bad note id %s", pyfmt.Repr(s))
	}
	return id, nil
}

// NoteInput is one `zen-inbox note`.
type NoteInput struct {
	Task, Title, Body string
	SessionID, Name   string
}

func checkNote(in NoteInput) error {
	if in.Task != "" {
		if err := checkTask(in.Task); err != nil {
			return err
		}
	}
	switch {
	case pyfmt.Blank(in.Title):
		return errf(2, "a --title is required")
	case strings.ContainsAny(in.Title, "\r\n"):
		return errf(2, "--title must be one line")
	case utf8.RuneCountInString(in.Title) > TitleMax:
		return errf(2, "--title is over %d characters", TitleMax)
	case len(in.Body) > BodyMax:
		return errf(2, "--body is over %d bytes", BodyMax)
	}
	return nil
}

// AddNote stores a note from chief and prunes notes read over KeepFor ago,
// keeping any whose reply still awaits delivery.
func (ib *Inbox) AddNote(in NoteInput) (Note, error) {
	if err := checkNote(in); err != nil {
		return Note{}, err
	}
	if in.Name != NoteSender {
		return Note{}, errf(1, "notes come from chief; send chief a message instead")
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var id int64
	err := ib.immediate(func() error {
		if err := ib.exec("DELETE FROM notes WHERE read_at != '' AND read_at < ?"+
			" AND delivery NOT IN ('sending', 'failed')",
			Stamp(Now().Add(-KeepFor))); err != nil {
			return err
		}
		res, err := ib.conn.ExecContext(ctx, "INSERT INTO notes (task, session_id, session_name, title, body,"+
			" created_at) VALUES (?, ?, ?, ?, ?, ?)", in.Task, in.SessionID, in.Name, in.Title, in.Body, stamp())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return Note{}, err
	}
	n, err := ib.note(id)
	return n, dbErr(err)
}

func (ib *Inbox) selNotes(where string, args ...any) ([]Note, error) {
	rows, err := ib.conn.QueryContext(ctx, "SELECT "+noteColumns+" FROM notes WHERE "+where+
		" ORDER BY created_at DESC, id DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Task, &n.SessionID, &n.SessionName, &n.Title, &n.Body, &n.CreatedAt,
			&n.ReadAt, &n.Reply, &n.RepliedAt, &n.ClaimedAt, &n.Delivery, &n.MessageID, &n.DeliveryError); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (ib *Inbox) note(id int64) (Note, error) {
	ns, err := ib.selNotes("id = ?", id)
	if err != nil {
		return Note{}, err
	}
	if len(ns) == 0 {
		return Note{}, errf(2, "no note n%d", id)
	}
	return ns[0], nil
}

func (ib *Inbox) Note(id int64) (Note, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	n, err := ib.note(id)
	return n, dbErr(err)
}

// Notes is what the panel shows, newest first: unread, read within
// VisibleFor, or with a reply in flight or failed; all adds every note kept.
func (ib *Inbox) Notes(all bool) ([]Note, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	if all {
		ns, err := ib.selNotes("1")
		return ns, dbErr(err)
	}
	ns, err := ib.selNotes(visibleNotes, Stamp(Now().Add(-VisibleFor)))
	return ns, dbErr(err)
}

func readStamp(read bool) string {
	if read {
		return stamp()
	}
	return ""
}

// MarkNotes sets ids read or unread; an unknown id changes nothing.
func (ib *Inbox) MarkNotes(ids []int64, read bool) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.immediate(func() error {
		for _, id := range ids {
			if _, err := ib.note(id); err != nil {
				return err
			}
		}
		for _, id := range ids {
			// A note already in the wanted state keeps its read time.
			if err := ib.exec("UPDATE notes SET read_at = ? WHERE id = ? AND (read_at = '') = ?",
				readStamp(read), id, bit(read)); err != nil {
				return err
			}
		}
		return nil
	})
}

// MarkAllNotes marks every unread note read, or every visible read note unread.
func (ib *Inbox) MarkAllNotes(read bool) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	if read {
		return dbErr(ib.exec("UPDATE notes SET read_at = ? WHERE read_at = ''", stamp()))
	}
	return dbErr(ib.exec("UPDATE notes SET read_at = '' WHERE read_at != '' AND read_at >= ?",
		Stamp(Now().Add(-VisibleFor))))
}

// ReplyHeader starts a reply's message.
func ReplyHeader(n Note) string {
	about := n.Title
	if n.Task != "" {
		about = "#" + n.Task + " " + n.Title
	}
	return "[zen-inbox] Reply to your note " + n.Label() + " (" + about + "):"
}

// ReplyMessage is the header then the text, cut at a UTF-8 boundary to fit
// the send limit.
func ReplyMessage(n Note, text string) string {
	msg := ReplyHeader(n) + "\n" + text
	if budget := SendLimit - SendMargin; len(msg) > budget {
		msg = strings.ToValidUTF8(msg[:budget], "")
	}
	return msg
}

// claimReply stores text as id's one reply and moves it none → sending.
func (ib *Inbox) claimReply(id int64, text string) (Note, error) {
	if pyfmt.Blank(text) {
		return Note{}, errf(2, "a reply must not be empty")
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var n Note
	err := ib.immediate(func() error {
		var err error
		if n, err = ib.note(id); err != nil {
			return err
		}
		if n.Delivery != "none" || n.Reply != "" {
			return errf(1, "%s already replied", n.Label())
		}
		n.Reply, n.RepliedAt, n.Delivery = text, stamp(), "sending"
		n.ClaimedAt = n.RepliedAt
		return ib.exec("UPDATE notes SET reply = ?, replied_at = ?, claimed_at = ?, delivery = 'sending'"+
			" WHERE id = ? AND delivery = 'none'", n.Reply, n.RepliedAt, n.ClaimedAt, id)
	})
	return n, err
}

// NoteDelivery is the outcome of one reply send.
type NoteDelivery struct {
	Note     Note
	OK       bool
	Error    string
	Followup string
}

// Describe is the one-line outcome of a reply send.
func (d NoteDelivery) Describe() string {
	if d.OK {
		return "reply " + d.Note.Label() + " sent to " + d.Note.SessionName
	}
	return "reply " + d.Note.Label() + " failed: " + d.Error + "; zen-inbox retry " + d.Note.SessionID
}

// replyTarget is the session a reply goes to: the one live session with the
// note sender's name, else the stored id while that session is alive.
func (ib *Inbox) replyTarget(n Note) (string, error) {
	sessions := ib.AM.Sessions()
	if sessions == nil {
		return n.SessionID, nil
	}
	var live []string
	for _, s := range sessions {
		id, _ := s["id"].(string)
		if name, _ := s["name"].(string); n.SessionName != "" && name == n.SessionName && id != "" && am.Status(s) != "gone" {
			live = append(live, id)
		}
	}
	switch {
	case len(live) == 1:
		return live[0], nil
	case am.Status(am.Find(sessions, n.SessionID)) != "gone":
		return n.SessionID, nil
	case len(live) == 0:
		return "", errf(1, "no live session named %s", n.SessionName)
	}
	return "", errf(1, "%d live sessions named %s: %s", len(live), n.SessionName, strings.Join(live, ", "))
}

// accepted reports whether agent-manager still holds an earlier send of n, so
// a retry after a failed write does not send the reply twice.
func (ib *Inbox) accepted(n Note) bool {
	if n.MessageID == "" {
		return false
	}
	status, errText := ib.AM.MessageStatus(n.MessageID)
	return errText == "" && status != "" && status != "dropped"
}

func (ib *Inbox) markReply(n Note, state, mid, errText string) (Note, error) {
	read := n.ReadAt
	if state == "sent" && read == "" {
		read = stamp()
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	err := ib.immediate(func() error {
		return ib.exec("UPDATE notes SET delivery = ?, message_id = ?, delivery_error = ?, read_at = ?"+
			" WHERE id = ?", state, mid, errText, read, n.ID)
	})
	n.Delivery, n.MessageID, n.DeliveryError, n.ReadAt = state, mid, errText, read
	return n, err
}

// sendReply sends a claimed reply; success also marks the note read. The
// message id is stored before the note is marked sent.
func (ib *Inbox) sendReply(n Note) (NoteDelivery, error) {
	if ib.accepted(n) {
		n, err := ib.markReply(n, "sent", n.MessageID, "")
		return NoteDelivery{Note: n, OK: err == nil}, err
	}
	target, err := ib.replyTarget(n)
	if err != nil {
		n, werr := ib.markReply(n, "failed", "", err.Error())
		return NoteDelivery{Note: n, Error: err.Error()}, werr
	}
	ok, mid, errText := ib.AM.Send(target, ReplyMessage(n, n.Reply))
	if !ok {
		n, err := ib.markReply(n, "failed", "", errText)
		return NoteDelivery{Note: n, Error: errText}, err
	}
	if mid != "" {
		ib.mu.Lock()
		err := ib.immediate(func() error {
			return ib.exec("UPDATE notes SET message_id = ? WHERE id = ?", mid, n.ID)
		})
		ib.mu.Unlock()
		if err != nil {
			return NoteDelivery{}, err
		}
	}
	n, err = ib.markReply(n, "sent", mid, "")
	if err != nil {
		return NoteDelivery{}, err
	}
	return NoteDelivery{Note: n, OK: true, Followup: ib.AM.Followup(target)}, nil
}

// Reply sends text as id's one reply to the note's sender.
func (ib *Inbox) Reply(id int64, text string) (NoteDelivery, error) {
	n, err := ib.claimReply(id, text)
	if err != nil {
		return NoteDelivery{}, err
	}
	return ib.sendReply(n)
}

// FailedNotes is every reply waiting for a retry, oldest first.
func (ib *Inbox) FailedNotes() ([]Note, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	ns, err := ib.selNotes(failedNotes, staleCutoff())
	for i, j := 0, len(ns)-1; i < j; i, j = i+1, j-1 {
		ns[i], ns[j] = ns[j], ns[i]
	}
	return ns, dbErr(err)
}

// RetryNote claims one failed reply again and resends it.
func (ib *Inbox) RetryNote(id int64) (NoteDelivery, error) {
	ns, err := ib.claimRetries("id = ?", id)
	if err != nil {
		return NoteDelivery{}, err
	}
	if len(ns) == 0 {
		return NoteDelivery{}, errf(1, "no failed reply for n%d", id)
	}
	return ib.sendReply(ns[0])
}

// RetryNotes resends a session's failed replies, one message each.
func (ib *Inbox) RetryNotes(session string) ([]NoteDelivery, error) {
	ns, err := ib.claimRetries("(session_id = ? OR session_name = ?)", session, session)
	if err != nil {
		return nil, err
	}
	out := make([]NoteDelivery, 0, len(ns))
	for i := len(ns) - 1; i >= 0; i-- {
		res, err := ib.sendReply(ns[i])
		if err != nil {
			return out, err
		}
		out = append(out, res)
	}
	return out, nil
}

func (ib *Inbox) claimRetries(where string, args ...any) ([]Note, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var ns []Note
	err := ib.immediate(func() error {
		var err error
		if ns, err = ib.selNotes(where+" AND "+failedNotes, append(args, staleCutoff())...); err != nil {
			return err
		}
		now := stamp()
		for i := range ns {
			ns[i].Delivery, ns[i].ClaimedAt = "sending", now
			if err := ib.exec("UPDATE notes SET delivery = 'sending', claimed_at = ? WHERE id = ?",
				now, ns[i].ID); err != nil {
				return err
			}
		}
		return nil
	})
	return ns, err
}

// Unread counts unread notes.
func Unread(ns []Note) int {
	n := 0
	for _, x := range ns {
		if x.Unread() {
			n++
		}
	}
	return n
}

// String is the CLI's one-line listing of a note.
func (n Note) String() string {
	state := "read"
	if n.Unread() {
		state = "unread"
	}
	if n.Delivery != "none" {
		state += "/reply " + n.Delivery
	}
	task := ""
	if n.Task != "" {
		task = "#" + n.Task
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s", n.Label(), state, n.SessionName, task, n.Title)
}
