package inbox

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
)

const pinned = "2026-03-01T12:00:00+00:00"

// closedAsk is an answered, delivered plain ask.
func closedAsk(t *testing.T, ib *Inbox, task string, gate bool) string {
	t.Helper()
	id := ask(t, ib, task, "s1", gate, "Q?")
	if _, err := ib.Answer(id, p("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	return id
}

func count(t *testing.T, ib *Inbox, query string, args ...any) int {
	t.Helper()
	var n int
	if err := ib.conn.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func del(t *testing.T, ib *Inbox, ids []string, task string, purge, dry bool) DeleteResult {
	t.Helper()
	res, err := ib.Delete(ids, task, purge, dry)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func reasons(res DeleteResult) []string {
	var out []string
	for _, s := range res.Skipped {
		out = append(out, s.Msg)
	}
	return out
}

func deletedIDs(res DeleteResult) []string {
	var out []string
	for _, d := range res.Deleted {
		out = append(out, d.ID)
	}
	return out
}

func TestDeleteHidesEachKindAndKeepsTheRow(t *testing.T) {
	at(t, pinned)
	ib, _, _ := open(t)
	d := closedAsk(t, ib, "7", false)
	g := closedAsk(t, ib, "7", true)
	n := addNote(t, ib, "7", "done")
	c, err := ib.AddChat("7", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if err := ib.UpdateChat(c.ID, "m1", "delivered", ""); err != nil {
		t.Fatal(err)
	}
	res := del(t, ib, []string{d, g, n.Label(), c.Label()}, "", false, false)
	if res.Refused || !slices.Equal(deletedIDs(res), []string{"7.1", "7.g", "n1", "c1"}) {
		t.Fatalf("%+v", res)
	}
	for i, kind := range []string{KindDecision, KindDecision, KindNote, KindChat} {
		if res.Deleted[i].Kind != kind || res.Deleted[i].Mode != ModeHide {
			t.Fatalf("%+v", res.Deleted[i])
		}
	}
	if _, err := ib.Get("7.1"); code(err) != 1 || err.Error() != "7.1 is deleted" {
		t.Fatalf("%v", err)
	}
	if got := count(t, ib, "SELECT COUNT(*) FROM decisions WHERE deleted_at = ?", pinned); got != 2 {
		t.Fatalf("%d hidden decisions", got)
	}
	if count(t, ib, "SELECT COUNT(*) FROM notes WHERE deleted_at != ''") != 1 ||
		count(t, ib, "SELECT COUNT(*) FROM chats WHERE deleted_at != ''") != 1 {
		t.Fatal("note or chat not stamped")
	}
	if got := get2(t, ib, "SELECT status FROM decisions WHERE id = '7.1'"); got != "answered" {
		t.Fatalf("hide changed the row: %q", got)
	}
}

func get2(t *testing.T, ib *Inbox, query string) string {
	t.Helper()
	var s string
	if err := ib.conn.QueryRowContext(ctx, query).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDeletePurgeCascadesToChildren(t *testing.T) {
	at(t, pinned)
	ib, _, _ := open(t)
	ib.User = true
	d := closedAsk(t, ib, "7", false)
	if err := ib.exec("INSERT INTO decision_drafts (decision_id, kind, option, text, rev, updated_at)" +
		" VALUES ('7.1', 'pick', 'a', '', 0, 'x')"); err != nil {
		t.Fatal(err)
	}
	hold := askProd(t, ib, "s1")
	if _, err := ib.Answer(hold, p("b"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	n := addNote(t, ib, "7", "x")
	if err := ib.exec("INSERT INTO note_replies (note_id, text, replied_at) VALUES (?, 'r', 'x')", n.ID); err != nil {
		t.Fatal(err)
	}
	res := del(t, ib, []string{d, hold, n.Label()}, "", true, false)
	if res.Refused || len(res.Deleted) != 3 || res.Deleted[0].Mode != ModePurge {
		t.Fatalf("%+v", res)
	}
	for _, q := range []string{
		"SELECT COUNT(*) FROM decisions", "SELECT COUNT(*) FROM decision_options",
		"SELECT COUNT(*) FROM decision_drafts", "SELECT COUNT(*) FROM prod_actions",
		"SELECT COUNT(*) FROM notes", "SELECT COUNT(*) FROM note_replies",
	} {
		if got := count(t, ib, q); got != 0 {
			t.Fatalf("%s = %d", q, got)
		}
	}
}

func TestDeletePurgeAlsoRemovesHiddenRows(t *testing.T) {
	ib, _, _ := open(t)
	d := closedAsk(t, ib, "7", false)
	del(t, ib, []string{d}, "", false, false)
	if res := del(t, ib, []string{d}, "", false, false); !res.Refused || !slices.Equal(reasons(res), []string{"7.1 is already deleted"}) {
		t.Fatalf("hiding twice: %+v", res)
	}
	if res := del(t, ib, []string{d}, "", true, false); res.Refused || len(res.Deleted) != 1 {
		t.Fatalf("%+v", res)
	}
	if count(t, ib, "SELECT COUNT(*) FROM decisions") != 0 {
		t.Fatal("row survived the purge")
	}
}

func TestPurgedIDsAreNeverReissued(t *testing.T) {
	for _, tc := range []struct {
		name  string
		gate  bool
		purge int
		want  string
	}{
		{"newest decision", false, 3, "7.4"},
		{"newest gate", true, 3, "7.g4"},
		{"only decision", false, 1, "7.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ib, _, _ := open(t)
			var ids []string
			for range tc.purge {
				ids = append(ids, closedAsk(t, ib, "7", tc.gate))
			}
			del(t, ib, []string{ids[len(ids)-1]}, "", true, false)
			if got := ask(t, ib, "7", "s1", tc.gate, "Q?"); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestPurgedGateTwoNextGateIsThree(t *testing.T) {
	ib, _, _ := open(t)
	closedAsk(t, ib, "7", true)
	g2 := closedAsk(t, ib, "7", true)
	if g2 != "7.g2" {
		t.Fatal(g2)
	}
	del(t, ib, []string{"7.g2"}, "", true, false)
	if got := ask(t, ib, "7", "s1", true, "Q?"); got != "7.g3" {
		t.Fatal(got)
	}
}

func TestDeleteRefusals(t *testing.T) {
	fresh := "2026-03-01T11:59:30+00:00"
	cases := []struct {
		name  string
		setup func(t *testing.T, ib *Inbox) string
		want  string
	}{
		{"open decision", func(t *testing.T, ib *Inbox) string { return ask(t, ib, "580", "s1", false, "Q?") },
			"580.1 is open; withdraw it first"},
		{"held", func(t *testing.T, ib *Inbox) string { return deliv(t, ib, "held") },
			"580.1 has an answer waiting for delivery"},
		{"sending", func(t *testing.T, ib *Inbox) string { return deliv(t, ib, "sending") },
			"580.1 has an answer waiting for delivery"},
		{"waiting", func(t *testing.T, ib *Inbox) string { return deliv(t, ib, "waiting") },
			"580.1 has an answer waiting for delivery"},
		{"approved unused prod", func(t *testing.T, ib *Inbox) string {
			ib.User = true
			id := askProd(t, ib, "s1")
			approve(ib, id)
			return id
		}, "680.1 is an approved prod ask; it stays as the audit trail"},
		{"approved used prod", func(t *testing.T, ib *Inbox) string {
			ib.User = true
			id := askProd(t, ib, "s1")
			approve(ib, id)
			if _, err := ib.Approval(id, prodWant(), "s9"); err != nil {
				t.Fatal(err)
			}
			return id
		}, "680.1 is an approved prod ask; it stays as the audit trail"},
		{"chat in flight", func(t *testing.T, ib *Inbox) string {
			if _, err := ib.AddChat("", "x"); err != nil {
				t.Fatal(err)
			}
			if err := ib.exec("UPDATE chats SET claimed_at = ?", fresh); err != nil {
				t.Fatal(err)
			}
			return "c1"
		}, "c1 in flight; try again in a minute"},
		{"note reply in flight", func(t *testing.T, ib *Inbox) string {
			addNote(t, ib, "", "x")
			if err := ib.exec("UPDATE notes SET delivery = 'sending', claimed_at = ?", fresh); err != nil {
				t.Fatal(err)
			}
			return "n1"
		}, "n1 in flight; try again in a minute"},
		{"not found decision", func(*testing.T, *Inbox) string { return "9.9" }, "no decision 9.9"},
		{"not found note", func(*testing.T, *Inbox) string { return "n9" }, "no note n9"},
		{"not found chat", func(*testing.T, *Inbox) string { return "c9" }, "no chat c9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at(t, pinned)
			ib, _, _ := open(t)
			id := tc.setup(t, ib)
			before := count(t, ib, "SELECT COUNT(*) FROM decisions WHERE deleted_at != ''")
			for _, purge := range []bool{false, true} {
				res := del(t, ib, []string{id}, "", purge, false)
				if !res.Refused || len(res.Deleted) != 0 || !slices.Equal(reasons(res), []string{tc.want}) {
					t.Fatalf("purge=%v %+v", purge, res)
				}
			}
			if count(t, ib, "SELECT COUNT(*) FROM decisions WHERE deleted_at != ''") != before ||
				count(t, ib, "SELECT COUNT(*) FROM notes WHERE deleted_at != ''")+
					count(t, ib, "SELECT COUNT(*) FROM chats WHERE deleted_at != ''") != 0 {
				t.Fatal("a refusal wrote")
			}
		})
	}
}

// deliv is an answered ask whose delivery was forced to state.
func deliv(t *testing.T, ib *Inbox, state string) string {
	t.Helper()
	id := ask(t, ib, "580", "s1", false, "Q?")
	if _, err := ib.Answer(id, p("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := ib.exec("UPDATE decisions SET delivery = ? WHERE id = ?", state, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDeleteBadIDsAreUsageErrors(t *testing.T) {
	ib, _, _ := open(t)
	for _, id := range []string{"580", "12", "n", "c", "nx", "7.", ".1", "7.x", "7.g1x", ""} {
		if _, err := ib.Delete([]string{id}, "", false, false); code(err) != 2 {
			t.Fatalf("%q: %v", id, err)
		}
	}
	for _, task := range []string{"7x", "-1", "a.b"} {
		if _, err := ib.Delete(nil, task, false, false); code(err) != 2 {
			t.Fatalf("task %q: %v", task, err)
		}
	}
	if _, err := ib.Delete(nil, "", false, false); code(err) != 2 {
		t.Fatal("no target must be a usage error")
	}
}

func TestDeleteAllowsFailedDeliveryAndNonApprovedProdAsks(t *testing.T) {
	at(t, pinned)
	ib, _, _ := open(t)
	ib.User = true
	failed := deliv(t, ib, "failed")
	withdrawn := askProd(t, ib, "s1")
	if _, err := ib.Withdraw(withdrawn, "s1", false); err != nil {
		t.Fatal(err)
	}
	hold := askProd(t, ib, "s1")
	if _, err := ib.Answer(hold, p("b"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	talk := askProd(t, ib, "s1")
	if _, err := ib.Resolve(talk, ""); err != nil {
		t.Fatal(err)
	}
	text := askProd(t, ib, "s1")
	if _, err := ib.Answer(text, nil, p("Approve"), "", nil); err != nil {
		t.Fatal(err)
	}
	ids := []string{failed, withdrawn, hold, talk, text}
	res := del(t, ib, ids, "", false, false)
	if res.Refused || len(res.Deleted) != len(ids) {
		t.Fatalf("%+v", res)
	}
}

func TestDeleteStaleSendingClaimIsNotInFlight(t *testing.T) {
	at(t, pinned)
	ib, _, _ := open(t)
	if _, err := ib.AddChat("", "x"); err != nil {
		t.Fatal(err)
	}
	if err := ib.exec("UPDATE chats SET claimed_at = '2026-03-01T11:58:00+00:00'"); err != nil {
		t.Fatal(err)
	}
	if res := del(t, ib, []string{"c1"}, "", false, false); res.Refused || len(res.Deleted) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestDeletedWaitingChatAndReplyAreNeverSent(t *testing.T) {
	at(t, pinned)
	setup := func(t *testing.T) (*Inbox, *fakeAM) {
		ib, fake, _ := open(t)
		fake.sessions = am.Sessions{session("c1", "chief", "idle")}
		if _, err := ib.AddChat("", "ping"); err != nil {
			t.Fatal(err)
		}
		addNote(t, ib, "", "n")
		if err := ib.exec("UPDATE chats SET status = 'waiting', error = 'chief is down'"); err != nil {
			t.Fatal(err)
		}
		if err := ib.exec("INSERT INTO note_replies (note_id, text, replied_at) VALUES (1, 'thanks', ?)", pinned); err != nil {
			t.Fatal(err)
		}
		if err := ib.exec("UPDATE notes SET reply = 'thanks', replied_at = ?, claimed_at = ?, delivery = 'waiting',"+
			" delivery_error = 'chief is down'", pinned, pinned); err != nil {
			t.Fatal(err)
		}
		return ib, fake
	}
	t.Run("control sends", func(t *testing.T) {
		ib, fake := setup(t)
		for range 3 {
			pass(t, ib)
		}
		if len(fake.sends) != 1 || len(fake.chiefs) != 1 {
			t.Fatalf("sends %q chiefs %q", fake.sends, fake.chiefs)
		}
	})
	t.Run("deleted sends nothing", func(t *testing.T) {
		ib, fake := setup(t)
		if res := del(t, ib, []string{"c1", "n1"}, "", false, false); res.Refused || len(res.Deleted) != 2 {
			t.Fatalf("%+v", res)
		}
		for range 3 {
			if out := pass(t, ib); len(out) != 0 {
				t.Fatalf("%+v", out)
			}
		}
		if len(fake.sends)+len(fake.chiefs) != 0 {
			t.Fatalf("sends %q chiefs %q", fake.sends, fake.chiefs)
		}
		if w, err := ib.WaitingNotes(); err != nil || len(w) != 0 {
			t.Fatalf("%+v %v", w, err)
		}
		if pending, err := ib.PendingChats([]string{"delivered"}); err != nil || len(pending) != 0 {
			t.Fatalf("%+v %v", pending, err)
		}
	})
}

func TestDeleteStrictRefusalWritesNothing(t *testing.T) {
	at(t, pinned)
	ib, _, _ := open(t)
	good := closedAsk(t, ib, "7", false)
	open1 := ask(t, ib, "7", "s1", false, "Q?")
	res := del(t, ib, []string{good, open1, "n5"}, "", false, false)
	if !res.Refused || len(res.Deleted) != 0 ||
		!slices.Equal(reasons(res), []string{"7.2 is open; withdraw it first", "no note n5"}) {
		t.Fatalf("%+v", res)
	}
	if count(t, ib, "SELECT COUNT(*) FROM decisions WHERE deleted_at != ''") != 0 {
		t.Fatal("the good ID was hidden")
	}
}

func TestDeleteTaskIsLenientAndMixesWithIDs(t *testing.T) {
	at(t, pinned)
	ib, _, _ := open(t)
	closedAsk(t, ib, "7", false)
	ask(t, ib, "7", "s1", false, "Open?")
	n := addNote(t, ib, "7", "x")
	if _, err := ib.AddChat("7", "hi"); err != nil {
		t.Fatal(err)
	}
	if err := ib.UpdateChat(1, "m", "delivered", ""); err != nil {
		t.Fatal(err)
	}
	other := closedAsk(t, ib, "8", false)
	addNote(t, ib, "9", "elsewhere")
	res := del(t, ib, nil, "7", false, false)
	if res.Refused || !slices.Equal(deletedIDs(res), []string{"7.1", "n1", "c1"}) ||
		len(res.Skipped) != 1 || res.Skipped[0].ID != "7.2" || res.Skipped[0].Reason != "open; withdraw it first" {
		t.Fatalf("%+v", res)
	}
	if n.ID != 1 || count(t, ib, "SELECT COUNT(*) FROM decisions WHERE deleted_at != ''") != 1 {
		t.Fatal("task delete touched the wrong rows")
	}
	res = del(t, ib, []string{other}, "7", false, false)
	if res.Refused || !slices.Equal(deletedIDs(res), []string{"8.1"}) || len(res.Skipped) != 1 {
		t.Fatalf("mixed: %+v", res)
	}
	ask(t, ib, "9", "s1", false, "Open?")
	res = del(t, ib, []string{"9.1"}, "7", false, false)
	if !res.Refused || len(res.Deleted) != 0 {
		t.Fatalf("a failing explicit ID must stop the task rows too: %+v", res)
	}
}

func TestDeleteTaskPurgeTakesHiddenRows(t *testing.T) {
	ib, _, _ := open(t)
	closedAsk(t, ib, "7", false)
	del(t, ib, []string{"7.1"}, "", false, false)
	if res := del(t, ib, nil, "7", false, false); len(res.Deleted) != 0 {
		t.Fatalf("hide skips what is hidden: %+v", res)
	}
	if res := del(t, ib, nil, "7", true, false); len(res.Deleted) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestDeleteDryRunWritesNothingAndReportsTheSameRows(t *testing.T) {
	at(t, pinned)
	ib, _, _ := open(t)
	closedAsk(t, ib, "7", false)
	ask(t, ib, "7", "s1", false, "Q?")
	addNote(t, ib, "7", "x")
	snapshot := func() int {
		return count(t, ib, "SELECT (SELECT COUNT(*) FROM decisions WHERE deleted_at != '')"+
			" + (SELECT COUNT(*) FROM notes WHERE deleted_at != '') + (SELECT COUNT(*) FROM decision_options)"+
			" + (SELECT COUNT(*) FROM decision_seq) + (SELECT COUNT(*) FROM notes)")
	}
	before := snapshot()
	for _, purge := range []bool{false, true} {
		dry := del(t, ib, nil, "7", purge, true)
		if snapshot() != before {
			t.Fatalf("purge=%v: a dry run wrote", purge)
		}
		real := del(t, ib, nil, "7", purge, false)
		if !slices.Equal(deletedIDs(dry), deletedIDs(real)) || !slices.Equal(reasons(dry), reasons(real)) {
			t.Fatalf("dry %+v real %+v", dry, real)
		}
		if !purge {
			if err := ib.exec("UPDATE decisions SET deleted_at = ''"); err != nil {
				t.Fatal(err)
			}
			if err := ib.exec("UPDATE notes SET deleted_at = ''"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestHiddenRowsDisappearFromEveryRead(t *testing.T) {
	at(t, pinned)
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "dead")}
	waiting := deliv(t, ib, "waiting")
	failed := deliv(t, ib, "failed")
	_ = failed
	n := addNote(t, ib, "580", "x")
	if _, err := ib.AddChat("580", "hi"); err != nil {
		t.Fatal(err)
	}
	if err := ib.exec("UPDATE decisions SET delivery = 'waiting' WHERE id = ?", waiting); err != nil {
		t.Fatal(err)
	}
	// A hidden row is hidden by stamping, bypassing the delivery guard.
	for _, q := range []string{"UPDATE decisions", "UPDATE notes", "UPDATE chats"} {
		if err := ib.exec(q + " SET deleted_at = '" + pinned + "'"); err != nil {
			t.Fatal(err)
		}
	}
	if ds, _ := ib.Select("1"); len(ds) != 0 {
		t.Fatalf("Select %v", ds)
	}
	if _, err := ib.Get(waiting); code(err) != 1 || !strings.HasSuffix(err.Error(), "is deleted") {
		t.Fatalf("Get %v", err)
	}
	if ns, _ := ib.Notes(true); len(ns) != 0 {
		t.Fatalf("Notes %v", ns)
	}
	if _, err := ib.Note(n.ID); err == nil {
		t.Fatal("Note found a hidden note")
	}
	if th, _ := ib.Thread("580"); len(th.Items) != 0 {
		t.Fatalf("Thread %+v", th.Items)
	}
	if cs, _ := ib.Chats(""); len(cs) != 0 {
		t.Fatalf("Chats %v", cs)
	}
	if u, _ := ib.Undelivered(); len(u) != 0 {
		t.Fatalf("Undelivered %v", u)
	}
	if w, _ := ib.Waiting(); len(w) != 0 {
		t.Fatalf("Waiting %v", w)
	}
	if out := pass(t, ib); len(out) != 0 {
		t.Fatalf("Pass %v", out)
	}
	if tasks, _ := ib.Unresolved(); len(tasks) != 0 {
		t.Fatalf("Unresolved %v", tasks)
	}
	if last, _ := ib.LastClosed(); last != "" {
		t.Fatalf("LastClosed %q", last)
	}
}

func TestHiddenReadNoteStillPrunes(t *testing.T) {
	at(t, "2026-01-01T00:00:00+00:00")
	ib, _, _ := open(t)
	n := addNote(t, ib, "", "old")
	if err := ib.MarkNotes([]int64{n.ID}, true); err != nil {
		t.Fatal(err)
	}
	del(t, ib, []string{"n1"}, "", false, false)
	at(t, "2026-06-01T00:00:00+00:00")
	addNote(t, ib, "", "new")
	if count(t, ib, "SELECT COUNT(*) FROM notes WHERE id = 1") != 0 {
		t.Fatal("the 30-day prune skipped a hidden read note")
	}
}

func TestV16DBMigratesToV17KeepingEveryRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zen-flow.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 16; v++ {
		for _, stmt := range strings.Split(Migrations[v], ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := raw.Exec(stmt); err != nil {
				t.Fatalf("v%d: %v", v, err)
			}
		}
	}
	for _, stmt := range []string{
		"PRAGMA user_version = 16",
		"INSERT INTO decisions (id, task, seq, gate, session_id, session_name, question, created_at, updated_at)" +
			" VALUES ('7.1', '7', 1, 0, 's1', 'spec-s1', 'Q?', 'x', 'x'), ('7.g', '7', 1, 1, 's1', 'spec-s1', 'G?', 'x', 'x')",
		"INSERT INTO decision_options VALUES ('7.1', 'a', 'x'), ('7.1', 'b', 'y')",
		"INSERT INTO notes (task, session_id, session_name, title, created_at) VALUES ('7', 'c1', 'chief', 'n', 'x')",
		"INSERT INTO note_replies (note_id, text, replied_at) VALUES (1, 'r', 'x')",
		"INSERT INTO chats (task, text, sent_at, status) VALUES ('7', 'hi', 'x', 'delivered')",
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()
	ib, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	defer ib.Close()
	if v, _ := ib.userVersion(); v != 17 {
		t.Fatalf("user_version %d", v)
	}
	for table, want := range map[string]int{"decisions": 2, "decision_options": 2, "notes": 1, "note_replies": 1, "chats": 1} {
		if got := count(t, ib, "SELECT COUNT(*) FROM "+table); got != want {
			t.Fatalf("%s: %d rows, want %d", table, got, want)
		}
	}
	for _, table := range []string{"decisions", "notes", "chats"} {
		if got := count(t, ib, "SELECT COUNT(*) FROM "+table+" WHERE deleted_at != ''"); got != 0 {
			t.Fatalf("%s: %d rows hidden by the migration", table, got)
		}
	}
	if count(t, ib, "SELECT COUNT(*) FROM decision_seq") != 0 {
		t.Fatal("the migration must not fill decision_seq")
	}
	if got := ask(t, ib, "7", "s1", false, "Q?"); got != "7.2" {
		t.Fatalf("next ID %s", got)
	}
}

func TestChatsListsVisibleNewestFirst(t *testing.T) {
	ib, _, _ := open(t)
	for _, task := range []string{"7", "8", "7"} {
		if _, err := ib.AddChat(task, "m"+task); err != nil {
			t.Fatal(err)
		}
	}
	del(t, ib, []string{"c3"}, "", false, false)
	all, _ := ib.Chats("")
	one, _ := ib.Chats("7")
	if len(all) != 2 || all[0].ID != 2 || len(one) != 1 || one[0].ID != 1 {
		t.Fatalf("%+v %+v", all, one)
	}
}
