package inbox

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func at(t *testing.T, stamp string) { t.Setenv("ZEN_INBOX_NOW", stamp) }

func addNote(t *testing.T, ib *Inbox, task, title string) Note {
	t.Helper()
	n, err := ib.AddNote(NoteInput{Task: task, Title: title, SessionID: "c1", Name: NoteSender})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func labels(t *testing.T, ib *Inbox, all bool) string {
	t.Helper()
	ns, err := ib.Notes(all)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range ns {
		out = append(out, n.Label())
	}
	return strings.Join(out, " ")
}

func TestNoteIDsIncreaseAndSurvivePrune(t *testing.T) {
	ib, _, _ := open(t)
	at(t, "2026-01-01T00:00:00+00:00")
	a, b := addNote(t, ib, "707", "one"), addNote(t, ib, "", "two")
	if a.Label() != "n1" || b.Label() != "n2" {
		t.Fatalf("%s %s", a.Label(), b.Label())
	}
	if err := ib.MarkNotes([]int64{1, 2}, true); err != nil {
		t.Fatal(err)
	}
	at(t, "2026-02-15T00:00:00+00:00")
	c := addNote(t, ib, "", "three")
	if c.Label() != "n3" {
		t.Fatalf("id reused: %s", c.Label())
	}
	if got := labels(t, ib, true); got != "n3" {
		t.Fatalf("prune kept %q", got)
	}
}

func TestPruneOnlyReadOverThirtyDays(t *testing.T) {
	ib, _, _ := open(t)
	at(t, "2026-01-01T00:00:00+00:00")
	addNote(t, ib, "", "old unread")
	addNote(t, ib, "", "old read")
	if err := ib.MarkNotes([]int64{2}, true); err != nil {
		t.Fatal(err)
	}
	at(t, "2026-01-20T00:00:00+00:00")
	addNote(t, ib, "", "recent read")
	if err := ib.MarkNotes([]int64{3}, true); err != nil {
		t.Fatal(err)
	}
	at(t, "2026-02-05T00:00:00+00:00")
	addNote(t, ib, "", "new")
	if got := labels(t, ib, true); got != "n4 n3 n1" {
		t.Fatalf("%q", got)
	}
}

func TestVisibleIsUnreadOrReadWithinADay(t *testing.T) {
	ib, _, _ := open(t)
	at(t, "2026-01-01T00:00:00+00:00")
	addNote(t, ib, "", "a")
	addNote(t, ib, "", "b")
	addNote(t, ib, "", "c")
	if err := ib.MarkNotes([]int64{2, 3}, true); err != nil {
		t.Fatal(err)
	}
	at(t, "2026-01-01T23:59:00+00:00")
	if got := labels(t, ib, false); got != "n3 n2 n1" {
		t.Fatalf("%q", got)
	}
	at(t, "2026-01-02T00:01:00+00:00")
	if got := labels(t, ib, false); got != "n1" {
		t.Fatalf("%q", got)
	}
	if got := labels(t, ib, true); got != "n3 n2 n1" {
		t.Fatalf("--all %q", got)
	}
}

func TestMarkToggleAndMarkAll(t *testing.T) {
	ib, _, _ := open(t)
	addNote(t, ib, "", "a")
	addNote(t, ib, "", "b")
	if err := ib.MarkNotes([]int64{1}, true); err != nil {
		t.Fatal(err)
	}
	first, _ := ib.Note(1)
	if first.Unread() {
		t.Fatal("not read")
	}
	if err := ib.MarkNotes([]int64{1}, true); err != nil {
		t.Fatal(err)
	}
	if again, _ := ib.Note(1); again.ReadAt != first.ReadAt {
		t.Fatal("marking read again moved read_at")
	}
	if err := ib.MarkNotes([]int64{1}, false); err != nil {
		t.Fatal(err)
	}
	if n, _ := ib.Note(1); !n.Unread() {
		t.Fatal("not unread")
	}
	if err := ib.MarkNotes([]int64{1, 9}, true); code(err) != 2 {
		t.Fatalf("unknown id: %v", err)
	}
	if n, _ := ib.Note(1); !n.Unread() {
		t.Fatal("a refused mark changed a note")
	}
	if err := ib.MarkAllNotes(true); err != nil {
		t.Fatal(err)
	}
	ns, _ := ib.Notes(false)
	if Unread(ns) != 0 || len(ns) != 2 {
		t.Fatalf("%+v", ns)
	}
	if err := ib.MarkAllNotes(false); err != nil {
		t.Fatal(err)
	}
	ns, _ = ib.Notes(false)
	if Unread(ns) != 2 {
		t.Fatalf("%+v", ns)
	}
}

func TestNoteInputLimits(t *testing.T) {
	ib, _, _ := open(t)
	for _, in := range []NoteInput{
		{Title: " "},
		{Title: "two\nlines"},
		{Title: strings.Repeat("é", TitleMax+1)},
		{Title: "ok", Body: strings.Repeat("x", BodyMax+1)},
		{Title: "ok", Task: "bad task"},
	} {
		in.SessionID, in.Name = "c1", NoteSender
		if _, err := ib.AddNote(in); code(err) != 2 {
			t.Errorf("%q: %v", in.Title, err)
		}
	}
	if _, err := ib.AddNote(NoteInput{Title: strings.Repeat("é", TitleMax), Body: strings.Repeat("x", BodyMax),
		SessionID: "c1", Name: NoteSender}); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.AddNote(NoteInput{Title: "hi", SessionID: "w1", Name: "worker"}); code(err) != 1 {
		t.Fatalf("non-chief: %v", err)
	}
}

func TestReplyOnceMarksReadAndFormats(t *testing.T) {
	ib, fake, _ := open(t)
	addNote(t, ib, "707", "Task done")
	res, err := ib.Reply(1, "thanks")
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	want := "[zen-inbox] Reply to your note n1 (#707 Task done):\nthanks"
	if len(fake.sends) != 1 || fake.sends[0] != [2]string{"c1", want} {
		t.Fatalf("%q", fake.sends)
	}
	n, _ := ib.Note(1)
	if n.Unread() || n.Delivery != "sent" || n.MessageID != "42" || fake.followed != 1 {
		t.Fatalf("%+v", n)
	}
	if _, err := ib.Reply(1, "again"); code(err) != 1 {
		t.Fatalf("second reply: %v", err)
	}
	addNote(t, ib, "", "No task")
	if _, err := ib.Reply(2, " "); code(err) != 2 {
		t.Fatalf("blank reply: %v", err)
	}
	if _, err := ib.Reply(2, "ok"); err != nil {
		t.Fatal(err)
	}
	if got := fake.sends[1][1]; !strings.HasPrefix(got, "[zen-inbox] Reply to your note n2 (No task):\n") {
		t.Fatalf("%q", got)
	}
}

func TestReplyMessageTruncatesAtUTF8Boundary(t *testing.T) {
	n := Note{ID: 3, Task: "707", Title: "t"}
	msg := ReplyMessage(n, strings.Repeat("é", SendLimit))
	if len(msg) > SendLimit || !utf8.ValidString(msg) || !strings.HasPrefix(msg, ReplyHeader(n)+"\n") {
		t.Fatalf("%d bytes, valid %v", len(msg), utf8.ValidString(msg))
	}
	if short := ReplyMessage(n, "hi"); short != ReplyHeader(n)+"\nhi" {
		t.Fatalf("%q", short)
	}
}

func TestConcurrentRepliesSendOnce(t *testing.T) {
	ib, fake, path := open(t)
	addNote(t, ib, "", "race")
	other, err := Open(path, fake)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, x := range []*Inbox{ib, other} {
		wg.Go(func() { _, errs[i] = x.Reply(1, "mine") })
	}
	wg.Wait()
	if len(fake.sends) != 1 || (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("sends %d, errs %v", len(fake.sends), errs)
	}
}

func TestFailedReplyKeptAndRetried(t *testing.T) {
	ib, fake, _ := open(t)
	at(t, "2026-01-01T00:00:00+00:00")
	addNote(t, ib, "", "a")
	addNote(t, ib, "", "b")
	addNote(t, ib, "", "c")
	fake.fail = "session gone"
	res, err := ib.Reply(1, "first")
	if err != nil || res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	n, _ := ib.Note(1)
	if n.Reply != "first" || n.DeliveryError != "session gone" || n.Delivery != "failed" || !n.Unread() {
		t.Fatalf("%+v", n)
	}
	// n2 is a fresh claim (in flight), n3 a crashed one.
	if err := ib.Exec("UPDATE notes SET reply = 'x', delivery = 'sending', replied_at = ? WHERE id = 2",
		Stamp(Now())); err != nil {
		t.Fatal(err)
	}
	if err := ib.Exec("UPDATE notes SET reply = 'y', delivery = 'sending', replied_at = ? WHERE id = 3",
		"2025-12-31T23:58:00+00:00"); err != nil {
		t.Fatal(err)
	}
	failed, _ := ib.FailedNotes()
	if len(failed) != 2 || failed[0].ID != 1 || failed[1].ID != 3 {
		t.Fatalf("%+v", failed)
	}
	fake.fail = ""
	out, err := ib.RetryNotes("c1")
	if err != nil || len(out) != 2 || !out[0].OK || !out[1].OK {
		t.Fatalf("%+v %v", out, err)
	}
	if n, _ := ib.Note(2); n.Delivery != "sending" {
		t.Fatalf("in-flight claim retried: %+v", n)
	}
	if n, _ := ib.Note(1); n.Delivery != "sent" || n.Unread() {
		t.Fatalf("%+v", n)
	}
	if out, _ := ib.RetryNotes("c1"); len(out) != 0 {
		t.Fatalf("%+v", out)
	}
}

func TestV4DumpMigratesToV5(t *testing.T) {
	dump, err := os.ReadFile("../../testdata/xver/v4.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "zen-flow.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(string(dump)); err != nil {
		t.Fatal(err)
	}
	v4 := map[string]string{}
	counts := map[string]int{}
	for _, table := range []string{"decisions", "decision_options", "decision_briefs", "prod_actions", "decision_drafts"} {
		v4[table] = createSQL(t, raw, table)
		var n int
		if err := raw.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n == 0 {
			t.Fatalf("%s: %d rows, %v", table, n, err)
		}
		counts[table] = n
	}
	_ = raw.Close()
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() {
			ib, err := Open(path, &fakeAM{})
			if err == nil {
				_, err = ib.Notes(true)
				_ = ib.Close()
			}
			errs[i] = err
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	ib, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	defer ib.Close()
	if v, _ := ib.userVersion(); v != 5 {
		t.Fatalf("user_version %d", v)
	}
	for table, want := range v4 {
		if got := createSQL(t, ib.conn, table); got != want {
			t.Fatalf("%s changed:\n%s\n%s", table, got, want)
		}
		var n int
		if err := ib.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil || n != counts[table] {
			t.Fatalf("%s: %d rows, want %d", table, n, counts[table])
		}
	}
	if d := get(t, ib, "9.1"); d.Prod == nil {
		t.Fatalf("%+v", d)
	}
	addNote(t, ib, "", "first")
}
