package inbox

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
)

func archived(id, name string) am.Session {
	s := session(id, name, "idle")
	s["archived"] = true
	return s
}

// answerTo asks one question as sid and answers it, returning the delivery.
func answerTo(t *testing.T, ib *Inbox, task, sid string) *Delivery {
	t.Helper()
	id := ask(t, ib, task, sid, false, "Q?")
	res, err := ib.Answer(id, p("a"), nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func pass(t *testing.T, ib *Inbox) []Outcome {
	t.Helper()
	out, err := ib.Pass()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func chat(t *testing.T, ib *Inbox, id int64) Chat {
	t.Helper()
	c, err := ib.Chat(id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFlushToADownSessionWaits(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "dead")}
	res := answerTo(t, ib, "7", "s1")
	if res.OK || !res.Waiting || res.Error != "session s1 is down" || len(fake.sends) != 0 {
		t.Fatalf("%+v sends %q", res, fake.sends)
	}
	if d := get(t, ib, "7.1"); d.Delivery != "waiting" || d.DeliveryError != "session s1 is down" {
		t.Fatalf("%+v", d)
	}
	if failed, _ := ib.Undelivered(); len(failed) != 0 {
		t.Fatalf("a waiting batch is listed as failed: %+v", failed)
	}
	if w, _ := ib.Waiting(); len(w) != 1 || w[0].SessionID != "s1" {
		t.Fatalf("%+v", w)
	}
	text, err := ib.Describe(res, "s1", "7", "")
	if want := "waiting: session s1 is down; the panel sends 7.1 when the session is back"; err != nil || text != want {
		t.Fatalf("%q %v", text, err)
	}
}

func TestFlushToASessionGoneForGoodFails(t *testing.T) {
	for name, sessions := range map[string]am.Sessions{
		"archived": {archived("s1", "spec-s1")},
		"missing":  {session("other", "other", "idle")},
	} {
		ib, fake, _ := open(t)
		fake.sessions = sessions
		res := answerTo(t, ib, "7", "s1")
		want := "session s1 is gone (dead, archived or missing)"
		if res.OK || res.Waiting || res.Error != want || len(fake.sends) != 0 {
			t.Fatalf("%s: %+v sends %q", name, res, fake.sends)
		}
		if d := get(t, ib, "7.1"); d.Delivery != "failed" || d.DeliveryError != want {
			t.Fatalf("%s: %+v", name, d)
		}
	}
}

func TestAnswersFallBackToTheOneLiveNamesake(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "dead"), session("new", "spec-s1", "idle")}
	if res := answerTo(t, ib, "7", "s1"); !res.OK || res.Target != "new" || len(fake.sends) != 1 || fake.sends[0][0] != "new" {
		t.Fatalf("%+v sends %q", res, fake.sends)
	}
	fake.sessions = append(fake.sessions, session("newer", "spec-s1", "idle"))
	res := answerTo(t, ib, "8", "s1")
	want := "2 live sessions named spec-s1: new, newer"
	if res.OK || res.Waiting || res.Error != want || len(fake.sends) != 1 {
		t.Fatalf("%+v sends %q", res, fake.sends)
	}
	if d := get(t, ib, "8.1"); d.Delivery != "failed" {
		t.Fatalf("%+v", d)
	}
}

func TestAnswersForTheCallersLiveNamesakeAreHandedBack(t *testing.T) {
	ib, fake, _ := open(t)
	ib.Self = "new"
	fake.sessions = am.Sessions{session("s1", "spec-s1", "dead"), session("new", "spec-s1", "idle")}
	res := answerTo(t, ib, "7", "s1")
	if !res.OK || res.Message == "" || len(fake.sends) != 0 {
		t.Fatalf("%+v sends %q", res, fake.sends)
	}
}

func TestPassSendsOnceTheTargetIsBack(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "dead")}
	ask(t, ib, "7", "s1", false, "One?")
	ask(t, ib, "7", "s1", false, "Two?")
	for _, id := range []string{"7.1", "7.2"} {
		if _, err := ib.Answer(id, p("a"), nil, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	before := get(t, ib, "7.1")
	out := pass(t, ib)
	if len(fake.sends) != 0 || len(out) != 1 || out[0].Sent || out[0].Failed ||
		out[0].Text != "waiting 7.1, 7.2 for spec-s1: session s1 is down" {
		t.Fatalf("%+v sends %q", out, fake.sends)
	}
	if d := get(t, ib, "7.1"); d.Delivery != "waiting" || d.Rev != before.Rev || d.UpdatedAt != before.UpdatedAt {
		t.Fatalf("a pass on a down target touched the row: %+v", d)
	}
	fake.sessions = am.Sessions{session("s1", "spec-s1", "idle")}
	fake.listed = 0
	out = pass(t, ib)
	if len(fake.sends) != 1 || fake.sends[0][0] != "s1" || !strings.Contains(fake.sends[0][1], "7.1 ") ||
		!strings.Contains(fake.sends[0][1], "7.2 ") {
		t.Fatalf("sends %q", fake.sends)
	}
	if len(out) != 1 || !out[0].Sent || out[0].Count != 2 || out[0].Name != "spec-s1" || out[0].Text != "sent 7.1, 7.2 to spec-s1" {
		t.Fatalf("%+v", out)
	}
	if fake.listed != 1 {
		t.Fatalf("the pass listed sessions %d times", fake.listed)
	}
	for _, id := range []string{"7.1", "7.2"} {
		if d := get(t, ib, id); d.Delivery != "sent" || d.MessageID != "42" || d.DeliveryError != "" {
			t.Fatalf("%+v", d)
		}
	}
	if out := pass(t, ib); len(out) != 0 || len(fake.sends) != 1 {
		t.Fatalf("a second pass sent again: %+v %q", out, fake.sends)
	}
}

func TestPassWithNothingWaitingCallsNothing(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "idle")}
	answerTo(t, ib, "7", "s1")
	fake.listed, fake.sends = 0, nil
	if out := pass(t, ib); out != nil || fake.listed != 0 || len(fake.sends) != 0 || len(fake.chiefs) != 0 || fake.followed != 1 {
		t.Fatalf("%+v listed %d sends %q followed %d", out, fake.listed, fake.sends, fake.followed)
	}
}

func TestPassSendDownAgainGoesBackToWaiting(t *testing.T) {
	ib, fake, _ := open(t)
	down := am.Sessions{session("s1", "spec-s1", "dead")}
	fake.sessions = down
	answerTo(t, ib, "7", "s1")
	fake.sessions = am.Sessions{session("s1", "spec-s1", "idle")}
	fake.fail = "session s1 is gone (dead, archived or missing)"
	fake.onSend = func() { fake.sessions = down }
	out := pass(t, ib)
	if len(out) != 1 || out[0].Sent || out[0].Failed {
		t.Fatalf("%+v", out)
	}
	if d := get(t, ib, "7.1"); d.Delivery != "waiting" || d.DeliveryError != "session s1 is down" {
		t.Fatalf("%+v", d)
	}
}

func TestPassOtherFailureFailsAndStopsThatTarget(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("c1", "chief", "dead")}
	if _, err := ib.AddNote(NoteInput{Title: "n", SessionID: "c1", Name: NoteSender}); err != nil {
		t.Fatal(err)
	}
	ask(t, ib, "7", "c1", false, "Q?")
	if err := ib.exec("UPDATE decisions SET session_name = 'chief'"); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Answer("7.1", p("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if res, err := ib.Reply(1, "hi"); err != nil || !res.Waiting {
		t.Fatalf("%+v %v", res, err)
	}
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	fake.fail = "too many undelivered"
	out := pass(t, ib)
	if len(fake.sends) != 1 || len(out) != 2 || !out[0].Failed || out[1].Sent || out[1].Failed {
		t.Fatalf("%+v sends %q", out, fake.sends)
	}
	if d := get(t, ib, "7.1"); d.Delivery != "failed" || d.DeliveryError != "too many undelivered" {
		t.Fatalf("%+v", d)
	}
	if n, _ := ib.Note(1); n.Delivery != "waiting" {
		t.Fatalf("%+v", n)
	}
}

func TestPassSendsOneMessagePerTargetInOrder(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("c1", "chief", "dead")}
	fake.chiefErr, fake.chiefDown = "no live session named 'chief'", true
	if _, err := ib.AddNote(NoteInput{Title: "n", SessionID: "c1", Name: NoteSender}); err != nil {
		t.Fatal(err)
	}
	ask(t, ib, "7", "c1", false, "Q?")
	if err := ib.exec("UPDATE decisions SET session_name = 'chief'"); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Answer("7.1", p("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if res, err := ib.Reply(1, "hi"); err != nil || !res.Waiting {
		t.Fatalf("%+v %v", res, err)
	}
	if c, err := ib.SendChat("", "ping"); err != nil || c.Status != "waiting" {
		t.Fatalf("%+v %v", c, err)
	}
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	fake.chiefErr = ""
	state := func() string {
		n, _ := ib.Note(1)
		return get(t, ib, "7.1").Delivery + " " + n.Delivery + " " + chat(t, ib, 1).Status
	}
	for i, want := range []string{"sent waiting waiting", "sent sent waiting", "sent sent queued"} {
		pass(t, ib)
		if got := state(); got != want {
			t.Fatalf("pass %d: %s, want %s", i+1, got, want)
		}
	}
	if len(fake.sends) != 2 || !strings.HasPrefix(fake.sends[0][1], Header) || !strings.Contains(fake.sends[1][1], "Reply to your note n1") ||
		!slices.Equal(fake.chiefs, []string{"ping"}) {
		t.Fatalf("sends %q chiefs %q", fake.sends, fake.chiefs)
	}
}

func TestPassNeverSendsToTheCallersOwnSession(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("c1", "chief", "dead")}
	fake.chiefErr, fake.chiefDown = "no live session named 'chief'", true
	if _, err := ib.AddNote(NoteInput{Title: "n", SessionID: "c1", Name: NoteSender}); err != nil {
		t.Fatal(err)
	}
	answerTo(t, ib, "7", "c1")
	if _, err := ib.Reply(1, "hi"); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.SendChat("", "ping"); err != nil {
		t.Fatal(err)
	}
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	fake.chiefErr = ""
	ib.Self = "c1"
	out := pass(t, ib)
	if len(out) != 3 || len(fake.sends) != 0 || len(fake.chiefs) != 0 {
		t.Fatalf("%+v sends %q chiefs %q", out, fake.sends, fake.chiefs)
	}
	for _, o := range out {
		if o.Sent || o.Failed || !strings.HasSuffix(o.Text, "the target is your own session") {
			t.Fatalf("%+v", o)
		}
	}
	n, _ := ib.Note(1)
	if get(t, ib, "7.1").Delivery != "waiting" || n.Delivery != "waiting" || chat(t, ib, 1).Status != "waiting" {
		t.Fatalf("an item left waiting: %s %s %s", get(t, ib, "7.1").Delivery, n.Delivery, chat(t, ib, 1).Status)
	}
}

func TestChatsWaitForChiefAndDrainOnePerPassInOrder(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("x", "worker", "idle")}
	fake.chiefErr, fake.chiefDown = "no live session named 'chief'", true
	for i, text := range []string{"one", "two", "three"} {
		c, err := ib.SendChat("", text)
		if err != nil || c.Status != "waiting" {
			t.Fatalf("%+v %v", c, err)
		}
		if want := "no live session named 'chief'"; i == 0 && c.Error != want {
			t.Fatalf("reason %q", c.Error)
		}
	}
	fake.chiefErr = ""
	if out := pass(t, ib); len(fake.chiefs) != 0 || len(out) != 1 || out[0].Sent || out[0].Count != 3 {
		t.Fatalf("sent to a chief that is down: %+v %q", out, fake.chiefs)
	}
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	// A chat written while others wait queues behind them, chief live or not.
	if c, err := ib.SendChat("", "four"); err != nil || c.Status != "waiting" || c.Error != "queued behind c3" {
		t.Fatalf("%+v %v", c, err)
	}
	want := []string{"one", "two", "three", "four"}
	for i := range want {
		pass(t, ib)
		if !slices.Equal(fake.chiefs, want[:i+1]) {
			t.Fatalf("pass %d sent %q", i+1, fake.chiefs)
		}
	}
	if c := chat(t, ib, 2); c.Status != "queued" || c.MessageID != "m2" || c.Error != "" {
		t.Fatalf("%+v", c)
	}
}

func TestChatOtherFailureFailsAndSendsNothingMore(t *testing.T) {
	ib, fake, _ := open(t)
	fake.chiefErr, fake.chiefDown = "no live session named 'chief'", true
	for _, text := range []string{"one", "two"} {
		if _, err := ib.SendChat("", text); err != nil {
			t.Fatal(err)
		}
	}
	fake.sessions = am.Sessions{session("a", "chief", "idle"), session("b", "chief", "idle")}
	fake.chiefErr, fake.chiefDown = "2 live sessions named 'chief': a, b", false
	out := pass(t, ib)
	if len(out) != 1 || !out[0].Failed || out[0].Text != "message c1 to chief failed: 2 live sessions named 'chief': a, b" {
		t.Fatalf("%+v", out)
	}
	if one, two := chat(t, ib, 1), chat(t, ib, 2); one.Status != "failed" || two.Status != "waiting" {
		t.Fatalf("%+v %+v", one, two)
	}
	// A failure that is not chief being down is never queued.
	if c, err := ib.SendChat("", "direct"); err != nil || c.Status != "waiting" {
		t.Fatalf("%+v %v", c, err)
	}
	if err := ib.exec("UPDATE chats SET status = 'failed' WHERE status = 'waiting'"); err != nil {
		t.Fatal(err)
	}
	if c, err := ib.SendChat("", "direct"); err != nil || c.Status != "failed" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestStaleChatClaimIsTakenOverAFreshOneIsNot(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	at(t, "2026-01-01T00:00:00+00:00")
	// AddChat claims the chat for a send that then never happens.
	if c, err := ib.AddChat("", "lost"); err != nil || c.Status != "sending" {
		t.Fatalf("%+v %v", c, err)
	}
	at(t, "2026-01-01T00:01:00+00:00")
	if out := pass(t, ib); len(out) != 0 || len(fake.chiefs) != 0 {
		t.Fatalf("a 60 s claim was taken over: %+v", out)
	}
	at(t, "2026-01-01T00:01:01+00:00")
	if out := pass(t, ib); len(out) != 1 || !out[0].Sent || !slices.Equal(fake.chiefs, []string{"lost"}) {
		t.Fatalf("%+v %q", out, fake.chiefs)
	}
	if c := chat(t, ib, 1); c.Status != "queued" || c.ClaimedAt != "2026-01-01T00:01:01+00:00" {
		t.Fatalf("%+v", c)
	}
}

// A chat written while another is mid-send must not overtake it.
func TestChatQueuesBehindOneBeingSent(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	if c, err := ib.AddChat("", "in flight"); err != nil || c.Status != "sending" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err := ib.SendChat("", "next")
	if err != nil || c.Status != "waiting" || c.Error != "queued behind c1" || len(fake.chiefs) != 0 {
		t.Fatalf("%+v %v sent %q", c, err, fake.chiefs)
	}
}

func TestReplyWaitsForItsSenderThenThePassSendsIt(t *testing.T) {
	ib, fake, _ := open(t)
	addNote(t, ib, "707", "Task merged")
	fake.sessions = am.Sessions{session("c1", "chief", "dead")}
	res, err := ib.Reply(1, "thanks")
	if err != nil || res.OK || !res.Waiting || len(fake.sends) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	n, _ := ib.Note(1)
	if n.Delivery != "waiting" || n.DeliveryError != "no live session named chief" || !n.Unread() ||
		!strings.HasPrefix(n.String(), "n1\tunread/reply waiting\t") {
		t.Fatalf("%+v", n)
	}
	if failed, _ := ib.FailedNotes(); len(failed) != 0 {
		t.Fatalf("a waiting reply is listed as failed: %+v", failed)
	}
	_, err = ib.Reply(1, "again")
	if want := "n1 has a reply waiting for chief; it is sent when chief is back"; err == nil || err.Error() != want || code(err) != 1 {
		t.Fatalf("%v", err)
	}
	if out := pass(t, ib); len(fake.sends) != 0 || len(out) != 1 || out[0].Sent {
		t.Fatalf("%+v", out)
	}
	fake.sessions = am.Sessions{session("c9", "chief", "idle")}
	out := pass(t, ib)
	if len(out) != 1 || !out[0].Sent || out[0].Text != "reply n1 sent to chief" || len(fake.sends) != 1 || fake.sends[0][0] != "c9" {
		t.Fatalf("%+v %q", out, fake.sends)
	}
	if n, _ = ib.Note(1); n.Delivery != "sent" || n.Unread() || len(n.Replies) != 1 {
		t.Fatalf("%+v", n)
	}
	if strings.Contains(fake.sends[0][1], "written") {
		t.Fatalf("a fresh reply carries the marker: %q", fake.sends[0][1])
	}
}

func TestDelayMarkerFromTenMinutes(t *testing.T) {
	for _, c := range []struct {
		later  string
		marked bool
		age    string
	}{
		{"2026-01-01T00:09:59+00:00", false, ""},
		{"2026-01-01T00:10:00+00:00", true, "10m"},
		{"2026-01-01T03:00:00+00:00", true, "3h"},
	} {
		ib, fake, _ := open(t)
		at(t, "2026-01-01T00:00:00+00:00")
		addNote(t, ib, "707", "Task merged")
		if _, err := ib.SetBrief("s1", "843", p("Phone"), nil); err != nil {
			t.Fatal(err)
		}
		fake.sessions = am.Sessions{session("c1", "chief", "dead")}
		fake.chiefErr, fake.chiefDown = "no live session named 'chief'", true
		if _, err := ib.Reply(1, "thanks"); err != nil {
			t.Fatal(err)
		}
		if _, err := ib.SendChat("843", "ping"); err != nil {
			t.Fatal(err)
		}
		fake.sessions = am.Sessions{session("c1", "chief", "idle")}
		fake.chiefErr = ""
		at(t, c.later)
		pass(t, ib)
		pass(t, ib)
		reply, chatMsg := "[zen-inbox] Reply to your note n1 (#707 Task merged):\nthanks", "[#843 Phone] ping"
		if c.marked {
			reply = "[zen-inbox] Reply to your note n1 (#707 Task merged): (written " + c.age + " ago)\nthanks"
			chatMsg = "[#843 Phone] (written " + c.age + " ago, queued while chief was down) ping"
		}
		if len(fake.sends) != 1 || fake.sends[0][1] != reply || !slices.Equal(fake.chiefs, []string{chatMsg}) {
			t.Fatalf("%s: reply %q chat %q", c.later, fake.sends, fake.chiefs)
		}
	}
}

// A chat held only behind an earlier chat never waited for chief to be back.
func TestLateChatQueuedBehindAnotherDoesNotSayChiefWasDown(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	at(t, "2026-01-01T00:00:00+00:00")
	if c, err := ib.AddChat("", "in flight"); err != nil || c.Status != "sending" {
		t.Fatalf("%+v %v", c, err)
	}
	if c, err := ib.SendChat("", "next"); err != nil || c.Status != "waiting" {
		t.Fatalf("%+v %v", c, err)
	}
	at(t, "2026-01-01T03:00:00+00:00")
	pass(t, ib)
	pass(t, ib)
	want := []string{"(written 3h ago) in flight", "(written 3h ago) next"}
	if !slices.Equal(fake.chiefs, want) {
		t.Fatalf("%q", fake.chiefs)
	}
}

func TestRetryTakesWaitingRowsAndKeepsThemWaitingWhileDown(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "dead"), session("c1", "chief", "dead")}
	answerTo(t, ib, "7", "s1")
	addNote(t, ib, "", "a")
	if _, err := ib.Reply(1, "hi"); err != nil {
		t.Fatal(err)
	}
	res, err := ib.Retry("s1")
	if err != nil || res.OK || !res.Waiting || get(t, ib, "7.1").Delivery != "waiting" {
		t.Fatalf("%+v %v", res, err)
	}
	if rs, err := ib.RetryNotes("chief"); err != nil || len(rs) != 1 || !rs[0].Waiting {
		t.Fatalf("%+v %v", rs, err)
	}
	fake.sessions = am.Sessions{session("s1", "spec-s1", "idle"), session("c1", "chief", "idle")}
	if res, err = ib.Retry("spec-s1"); err != nil || !res.OK || get(t, ib, "7.1").Delivery != "sent" {
		t.Fatalf("%+v %v", res, err)
	}
	if r, err := ib.RetryNote(1); err != nil || !r.OK {
		t.Fatalf("%+v %v", r, err)
	}
	if len(fake.sends) != 2 {
		t.Fatalf("sends %q", fake.sends)
	}
}

// v12Tables are the tables a v12 db holds, with the columns a v12 binary reads.
var v12Tables = map[string]string{
	"decisions":        columns,
	"decision_options": "decision_id, letter, text",
	"decision_briefs":  "session_id, task, title, brief, updated_at",
	"prod_actions":     "decision_id, action, repo, branch, sha, used_at, used_by, cmd, pushed_sha, pushed_at, pushed_by",
	"decision_drafts":  "decision_id, kind, option, text, rev, updated_at",
	"notes":            noteColumns,
	"note_replies":     "id, note_id, text, replied_at",
	"chats":            "id, task, text, sent_at, message_id, status, error",
}

func TestV12DumpMigratesToLatest(t *testing.T) {
	dump, err := os.ReadFile("../../testdata/xver/v12.sql")
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
	before, rows := map[string]string{}, map[string]string{}
	for table, cols := range v12Tables {
		before[table] = createSQL(t, raw, table)
		if rows[table] = tableRows(t, raw, table, cols); rows[table] == "" {
			t.Fatalf("%s: fixture has no rows", table)
		}
	}
	_ = raw.Close()

	ib, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := ib.userVersion(); v != SchemaVersion {
		t.Fatalf("user_version %d", v)
	}
	for table := range v12Tables {
		want := before[table]
		if table == "chats" {
			want = strings.Replace(want, "\n)", "\n, claimed_at TEXT NOT NULL DEFAULT '')", 1)
		}
		if got := createSQL(t, ib.conn, table); got != want {
			t.Fatalf("%s changed:\n%s\n%s", table, got, want)
		}
	}
	th, err := ib.Thread("933")
	if err != nil || len(th.Items) != 5 {
		t.Fatalf("%+v %v", th, err)
	}
	if c := chat(t, ib, 1); c.Status != "delivered" || c.MessageID != "42" || c.ClaimedAt != "" {
		t.Fatalf("%+v", c)
	}
	if held := get(t, ib, "933.1"); held.Delivery != "held" || held.Note != "go" {
		t.Fatalf("%+v", held)
	}
	_ = ib.Close()

	// A v12 binary sees user_version 13 as current and reads its own columns.
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for table, cols := range v12Tables {
		if got := tableRows(t, raw, table, cols); got != rows[table] {
			t.Fatalf("%s rows changed:\n%s\n%s", table, got, rows[table])
		}
	}
	if _, err := raw.Exec("INSERT INTO chats (task, text, sent_at, status) VALUES ('', 'from v12', 'x', 'queued')"); err != nil {
		t.Fatalf("a v12 insert fails on a v13 db: %v", err)
	}
}

func TestFreshDBHasTheChatClaim(t *testing.T) {
	ib, _, _ := open(t)
	if v, _ := ib.userVersion(); v != SchemaVersion {
		t.Fatalf("user_version %d", v)
	}
	if !strings.Contains(createSQL(t, ib.conn, "chats"), "claimed_at TEXT NOT NULL DEFAULT ''") {
		t.Fatal(createSQL(t, ib.conn, "chats"))
	}
}
