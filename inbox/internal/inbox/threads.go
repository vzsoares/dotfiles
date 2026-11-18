package inbox

import (
	"cmp"
	"database/sql"
	"errors"
	"slices"
)

// ThreadItem is one entry of a task's timeline; exactly one of its pointers is set.
type ThreadItem struct {
	At       string
	Decision *Decision
	Note     *Note
	Chat     *Chat
}

// rank orders items stamped in the same second: decisions, notes, chats.
func (t ThreadItem) rank() int {
	switch {
	case t.Decision != nil:
		return 0
	case t.Note != nil:
		return 1
	}
	return 2
}

func compareItems(a, b ThreadItem) int {
	if c := cmp.Compare(a.At, b.At); c != 0 {
		return c
	}
	if c := cmp.Compare(a.rank(), b.rank()); c != 0 {
		return c
	}
	switch {
	case a.Decision != nil:
		return CompareID(a.Decision.ID, b.Decision.ID)
	case a.Note != nil:
		return cmp.Compare(a.Note.ID, b.Note.ID)
	}
	return cmp.Compare(a.Chat.ID, b.Chat.ID)
}

// Thread is everything the db holds for one task, oldest first.
type Thread struct {
	Task, Title, Brief string
	Items              []ThreadItem
}

// Thread reads task's decisions of any status, its kept notes with their
// replies and its chats, ordered by creation time then ID.
func (ib *Inbox) Thread(task string) (Thread, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	t, err := ib.thread(task)
	return t, dbErr(err)
}

func (ib *Inbox) thread(task string) (Thread, error) {
	t := Thread{Task: task}
	ds, err := ib.sel("task = ?", task)
	if err != nil {
		return t, err
	}
	ns, err := ib.selNotes("task = ?", task)
	if err != nil {
		return t, err
	}
	cs, err := ib.selChats("task = ?", task)
	if err != nil {
		return t, err
	}
	for i := range ds {
		t.Items = append(t.Items, ThreadItem{At: ds[i].CreatedAt, Decision: &ds[i]})
	}
	for i := range ns {
		t.Items = append(t.Items, ThreadItem{At: ns[i].CreatedAt, Note: &ns[i]})
	}
	for i := range cs {
		t.Items = append(t.Items, ThreadItem{At: cs[i].SentAt, Chat: &cs[i]})
	}
	slices.SortStableFunc(t.Items, compareItems)
	for _, q := range []struct {
		col string
		to  *string
	}{{"title", &t.Title}, {"brief", &t.Brief}} {
		err := ib.conn.QueryRowContext(ctx, "SELECT "+q.col+" FROM decision_briefs WHERE task = ? AND "+q.col+
			" != '' ORDER BY updated_at DESC LIMIT 1", task).Scan(q.to)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return t, err
		}
	}
	return t, nil
}

// TaskTitles is the newest non-blank brief title of every task that has one.
func (ib *Inbox) TaskTitles() (map[string]string, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	rows, err := ib.conn.QueryContext(ctx,
		"SELECT task, title FROM decision_briefs WHERE title != '' ORDER BY updated_at, task")
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var task, title string
		if err := rows.Scan(&task, &title); err != nil {
			return nil, dbErr(err)
		}
		out[task] = title
	}
	return out, dbErr(rows.Err())
}
