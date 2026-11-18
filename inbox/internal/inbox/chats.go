package inbox

import (
	"strconv"
	"strings"
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

const chatColumns = "id, task, text, sent_at, message_id, status, error"

// Chat is one message the panel sent to chief; Task is blank outside a thread.
type Chat struct {
	ID                       int64
	Task, Text, SentAt       string
	MessageID, Status, Error string
}

func (c Chat) Label() string { return "c" + strconv.FormatInt(c.ID, 10) }

// AddChat stores a chat before it is sent, with status queued.
func (ib *Inbox) AddChat(task, text string) (Chat, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	c := Chat{Task: task, Text: text, SentAt: stamp(), Status: "queued"}
	err := ib.immediate(func() error {
		res, err := ib.conn.ExecContext(ctx, "INSERT INTO chats (task, text, sent_at, status) VALUES (?, ?, ?, ?)",
			c.Task, c.Text, c.SentAt, c.Status)
		if err != nil {
			return err
		}
		c.ID, err = res.LastInsertId()
		return err
	})
	return c, err
}

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
		if err := rows.Scan(&c.ID, &c.Task, &c.Text, &c.SentAt, &c.MessageID, &c.Status, &c.Error); err != nil {
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
