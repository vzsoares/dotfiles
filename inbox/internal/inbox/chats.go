package inbox

import (
	"database/sql"
	"errors"
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

const chatColumns = "id, task, text, sent_at, message_id, status, error, claimed_at"

// Chat is one message the panel wrote to chief; Task is blank outside a
// thread and SentAt is when it was written. Status is sending (claimed by a
// send), waiting (chief is down; a pass sends it), failed, or agent-manager's
// status once it has a message id.
type Chat struct {
	ID                       int64
	Task, Text, SentAt       string
	MessageID, Status, Error string
	ClaimedAt                string
}

func (c Chat) Label() string { return "c" + strconv.FormatInt(c.ID, 10) }

// ChatMessage is what chief receives: a thread's message leads with its task
// and title, and one delivered lateAfter or more after written says so.
func ChatMessage(task, title, text, written string) string {
	if age, late := writtenAgo(written); late {
		text = "(written " + age + " ago, queued while chief was down) " + text
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
		err := ib.conn.QueryRowContext(ctx, "SELECT id FROM chats WHERE status IN ('waiting', 'sending') ORDER BY id DESC LIMIT 1").Scan(&behind)
		switch {
		case err == nil:
			c.Status, c.Error = "waiting", "queued behind c"+strconv.FormatInt(behind, 10)
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
	id, errText, down := ib.AM.Chief(ChatMessage(c.Task, title, c.Text, c.SentAt))
	c.MessageID, c.Error, c.Status = id, errText, "queued"
	switch {
	case errText != "" && down:
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

func (ib *Inbox) selChats(where string, args ...any) ([]Chat, error) {
	rows, err := ib.conn.QueryContext(ctx, "SELECT "+chatColumns+" FROM chats WHERE "+where+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.ID, &c.Task, &c.Text, &c.SentAt, &c.MessageID, &c.Status, &c.Error, &c.ClaimedAt); err != nil {
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
