package inbox

import (
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

// schemaV10 stores the panel's messages to chief so a thread can show them.
// No existing table changes, so a v9 reader still works.
const schemaV10 = `
CREATE TABLE chats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL,
    sent_at TEXT NOT NULL,
    message_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX chats_task ON chats (task, id);
`

// schemaV13 adds the chat send claim: a chat is 'sending' from claimed_at
// until its send is recorded. Additive: a v12 reader names its columns.
const schemaV13 = `
ALTER TABLE chats ADD COLUMN claimed_at TEXT NOT NULL DEFAULT '';
`

const chatColumns = "id, task, text, sent_at, message_id, status, error, claimed_at, deleted_at"

// Chat is one message the panel wrote to chief; Task is blank outside a
// thread and SentAt is when it was written. Status is sending (claimed by a
// send), waiting (chief is down; a pass sends it), failed, or agent-manager's
// status once it has a message id.
type Chat struct {
	ID                       int64
	Task, Text, SentAt       string
	MessageID, Status, Error string
	DeletedAt                string
	ClaimedAt                string
	// Waited is set by claimChat when chief being down kept the chat waiting;
	// a chat that only queued behind another is not Waited.
	Waited bool
}

func (c Chat) Label() string { return "c" + strconv.FormatInt(c.ID, 10) }

// QueuedBehind starts the Error of a chat held back by an earlier chat.
const QueuedBehind = "queued behind c"

// ChatMessage is what chief receives: a thread's message leads with its task
// and title, and one delivered lateAfter or more after written says so; it
// adds that chief was down only when the chat waited for it (waited).
func ChatMessage(task, title, text, written string, waited bool) string {
	if age, late := writtenAgo(written); late {
		note := "(written " + age + " ago"
		if waited {
			note += ", queued while chief was down"
		}
		text = note + ") " + text
	}
	if task == "" {
		return text
	}
	return "[" + pyfmt.RStrip("#"+task+" "+title) + "] " + text
}

// AddChat stores a chat claimed for its send, or waiting when earlier chats
// still wait or are being sent, so the queue keeps its order.
func (ib *Inbox) AddChat(task, text string) (Chat, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	c := Chat{Task: task, Text: text, SentAt: stamp(), Status: "sending"}
	err := ib.immediate(func() error {
		var behind int64
		err := ib.conn.QueryRowContext(ctx, "SELECT id FROM chats WHERE deleted_at = '' AND status IN ('waiting', 'sending') ORDER BY id DESC LIMIT 1").Scan(&behind)
		switch {
		case err == nil:
			c.Status, c.Error = "waiting", QueuedBehind+strconv.FormatInt(behind, 10)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		default:
			c.ClaimedAt = c.SentAt
		}
		res, err := ib.conn.ExecContext(ctx, "INSERT INTO chats (task, text, sent_at, status, error, claimed_at)"+
			" VALUES (?, ?, ?, ?, ?, ?)", c.Task, c.Text, c.SentAt, c.Status, c.Error, c.ClaimedAt)
		if err != nil {
			return err
		}
		c.ID, err = res.LastInsertId()
		return err
	})
	return c, err
}

// SendChat stores text for chief and sends it: queued with its message id,
// waiting when chief is down or earlier chats wait, else failed.
func (ib *Inbox) SendChat(task, text string) (Chat, error) {
	c, err := ib.AddChat(task, text)
	if err != nil || c.Status == "waiting" {
		return c, err
	}
	return ib.sendChat(c)
}

func (ib *Inbox) taskTitle(task string) (string, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var title string
	err := ib.conn.QueryRowContext(ctx, "SELECT title FROM decision_briefs WHERE task = ? AND title != ''"+
		" ORDER BY updated_at DESC LIMIT 1", task).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return title, dbErr(err)
}

// sendChat sends a claimed chat through zen-chief and records the outcome.
func (ib *Inbox) sendChat(c Chat) (Chat, error) {
	title, err := ib.taskTitle(c.Task)
	if err != nil {
		// Left 'sending', the pass would send it after the user's own resend.
		c.Status, c.Error = "failed", err.Error()
		_ = ib.UpdateChat(c.ID, "", c.Status, c.Error)
		return c, err
	}
	var down bool
	key := am.ChiefName
	if live := chiefLive(ib.AM.Sessions()); len(live) > 0 {
		key = live[0]
	}
	text := ChatMessage(c.Task, title, c.Text, c.SentAt, c.Waited)
	_, id, errText, deferred, err := ib.attempt(key, outgoing{name: am.ChiefName, body: text, key: "chat:" + c.Label()}, func() (bool, string, string) {
		var mid, e string
		mid, e, down = ib.AM.Chief(text)
		return e == "", mid, e
	})
	if err != nil {
		c.Status, c.Error = "failed", err.Error()
		_ = ib.UpdateChat(c.ID, "", c.Status, c.Error)
		return c, err
	}
	c.MessageID, c.Error, c.Status = id, errText, "queued"
	switch {
	case errText != "" && (down || deferred):
		c.Status = "waiting"
	case errText != "":
		c.Status = "failed"
	}
	if err := ib.UpdateChat(c.ID, id, c.Status, errText); err != nil {
		if errText == "" {
			c.Error = "sent as " + id + ", not recorded: " + err.Error()
		}
		return c, err
	}
	if errText == "" {
		return c, ib.closeRefusals("chat:")
	}
	if c.Status == "failed" && !strings.HasPrefix(errText, GaveUp) {
		target := ""
		if key != am.ChiefName {
			target = key
		}
		return c, ib.NoteRefusal("chat:"+c.Label(), target, am.ChiefName, text, errText)
	}
	return c, nil
}

// WaitingChats are the chats kept for chief, oldest first.
func (ib *Inbox) WaitingChats() ([]Chat, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	cs, err := ib.selChats("status = 'waiting'")
	return cs, dbErr(err)
}

// chatClaimable is a waiting chat, or one whose send claim went stale.
const chatClaimable = "(status = 'waiting' OR (status = 'sending' AND claimed_at < ?))"

// claimChat takes the oldest claimable chat for a send; ok is false when
// there is none left.
func (ib *Inbox) claimChat() (c Chat, ok bool, err error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	err = ib.immediate(func() error {
		cs, err := ib.selChats(chatClaimable, staleCutoff())
		if err != nil || len(cs) == 0 {
			return err
		}
		c, ok = cs[0], true
		c.Waited = c.Status == "waiting" && !strings.HasPrefix(c.Error, QueuedBehind) && !IsDeferred(c.Error)
		c.Status, c.ClaimedAt = "sending", stamp()
		return ib.exec("UPDATE chats SET status = 'sending', claimed_at = ? WHERE id = ?", c.ClaimedAt, c.ID)
	})
	return c, ok, err
}

// chiefLive is the ids of the live sessions named chief.
func chiefLive(sessions am.Sessions) []string { return am.LiveNamed(sessions, am.ChiefName) }

// UpdateChat records a chat's message id and delivery status; an empty
// messageID keeps the stored one.
func (ib *Inbox) UpdateChat(id int64, messageID, status, errText string) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.immediate(func() error {
		return ib.exec("UPDATE chats SET message_id = CASE WHEN ? = '' THEN message_id ELSE ? END,"+
			" status = ?, error = ? WHERE id = ?", messageID, messageID, status, errText, id)
	})
}

// PendingChats are the chats whose status is not in done, oldest first.
func (ib *Inbox) PendingChats(done []string) ([]Chat, error) {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(done)), ",")
	args := make([]any, len(done))
	for i, s := range done {
		args[i] = s
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	cs, err := ib.selChats("status NOT IN ("+marks+")", args...)
	return cs, dbErr(err)
}

// selChats is the visible chats matching where; selChatsAny also reads hidden ones.
func (ib *Inbox) selChats(where string, args ...any) ([]Chat, error) {
	return ib.selChatsAny("deleted_at = '' AND ("+where+")", args...)
}

func (ib *Inbox) selChatsAny(where string, args ...any) ([]Chat, error) {
	rows, err := ib.conn.QueryContext(ctx, "SELECT "+chatColumns+" FROM chats WHERE "+where+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.ID, &c.Task, &c.Text, &c.SentAt, &c.MessageID, &c.Status, &c.Error, &c.ClaimedAt, &c.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Chat is one stored chat.
func (ib *Inbox) Chat(id int64) (Chat, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	cs, err := ib.selChats("id = ?", id)
	if err != nil {
		return Chat{}, dbErr(err)
	}
	if len(cs) == 0 {
		return Chat{}, errf(1, "no chat c%d", id)
	}
	return cs[0], nil
}

// Chats lists the visible chats newest first, those of task when it is set.
func (ib *Inbox) Chats(task string) ([]Chat, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	where, args := "1", []any(nil)
	if task != "" {
		where, args = "task = ?", []any{task}
	}
	cs, err := ib.selChats(where, args...)
	slices.Reverse(cs)
	return cs, dbErr(err)
}

// JSON is the chat as `zen-inbox chats --json` prints it.
func (c Chat) JSON() pyfmt.Object {
	return pyfmt.Object{
		{Key: "id", Value: c.Label()},
		{Key: "task", Value: c.Task},
		{Key: "text", Value: c.Text},
		{Key: "sent_at", Value: c.SentAt},
		{Key: "status", Value: c.Status},
		{Key: "message_id", Value: c.MessageID},
		{Key: "error", Value: c.Error},
	}
}
