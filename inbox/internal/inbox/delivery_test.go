package inbox

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/am/amtest"
)

// deliveryEnv points the scan at a fixture state.db and transcript dir.
func deliveryEnv(t *testing.T) (state, projects string) {
	t.Helper()
	dir := t.TempDir()
	state, projects = filepath.Join(dir, "state.db"), filepath.Join(dir, "projects")
	t.Setenv(am.StateEnv, state)
	t.Setenv(am.ProjectsEnv, projects)
	return state, projects
}

var fixtureSessions = []amtest.Session{
	{ID: "chief", Name: "chief", Cwd: "/w/chief", Agent: "a-chief"},
	{ID: "dialog", Name: "worker-d", Status: "waiting", Agent: "a-d"},
	{ID: "dead", Name: "worker-x", Status: "dead", Agent: "a-x"},
	{ID: "arch", Name: "worker-a", Archived: true, Agent: "a-a"},
	{ID: "pi", Name: "box", Tool: "pi"},
	{ID: "bare", Name: "worker-b", Agent: "a-none"},
}

func entries(t *testing.T, ib *Inbox, all bool) map[string]Entry {
	t.Helper()
	es, err := ib.Entries(all)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Entry{}
	for _, e := range es {
		out[e.Source+":"+e.SourceID] = e
	}
	return out
}

func scan(t *testing.T, ib *Inbox) ScanReport {
	t.Helper()
	rep, err := ib.ScanDelivery()
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestScanClassifiesAgentManagerRows(t *testing.T) {
	at(t, t0)
	state, projects := deliveryEnv(t)
	now := Now()
	sent := now.Add(-10 * time.Minute)
	amtest.Transcript(t, projects, "-w-chief", "a-chief", now, amtest.Delivered("worker", "synthetic false drop"))
	amtest.State(t, state, fixtureSessions, []amtest.Row{
		{ID: 1, To: "chief", FromName: "worker", Body: "synthetic false drop", Sent: sent, Delivered: sent, Dropped: sent},
		{ID: 2, To: "chief", FromName: "worker", Body: "synthetic lost", Sent: sent, Delivered: sent, Dropped: sent},
		{ID: 3, To: "dialog", FromName: "chief", Body: "synthetic held dialog", Sent: sent},
		{ID: 4, To: "arch", FromName: "chief", Body: "synthetic to archived", Sent: sent},
		{ID: 5, To: "dead", FromName: "chief", Body: "synthetic to dead", Sent: sent},
		{ID: 6, To: "pi", FromName: "chief", Body: "synthetic to pi", Sent: sent, Dropped: sent},
		{ID: 7, To: "bare", FromName: "chief", Body: "synthetic no transcript", Sent: sent, Dropped: sent},
		{ID: 8, To: "dialog", FromName: "chief", Body: "synthetic just sent", Sent: now.Add(-time.Minute)},
		{ID: 9, To: "chief", FromName: "worker", Body: "synthetic fine", Sent: sent, Delivered: sent},
		{ID: 10, To: "deleted", FromName: "chief", Body: "synthetic to deleted", Sent: sent},
	})
	ib, _, _ := open(t)
	rep := scan(t, ib)
	if rep.Unavailable != "" || rep.Rows != 10 || rep.Open != 7 || rep.Lost != 3 || rep.FalseDropsWeek != 1 {
		t.Fatalf("%+v", rep)
	}
	all := entries(t, ib, true)
	want := map[string]struct{ kind, state, reason, body string }{
		"am_inbox:1":  {KindFalseDrop, "resolved", "marked dropped", ""},
		"am_inbox:2":  {KindLost, "open", "not in its transcripts", "synthetic lost"},
		"am_inbox:3":  {KindHeld, "open", "a dialog on screen", "synthetic held dialog"},
		"am_inbox:4":  {KindLost, "open", "recipient is archived", "synthetic to archived"},
		"am_inbox:5":  {KindHeld, "open", "not running", "synthetic to dead"},
		"am_inbox:6":  {KindUnverified, "open", "recipient runs pi", "synthetic to pi"},
		"am_inbox:7":  {KindUnverified, "open", "no transcript found", "synthetic no transcript"},
		"am_inbox:10": {KindLost, "open", "recipient session is gone", "synthetic to deleted"},
	}
	if len(all) != len(want) {
		t.Fatalf("%d entries, want %d: %+v", len(all), len(want), all)
	}
	for key, w := range want {
		e := all[key]
		if e.Kind != w.kind || e.State != w.state || !strings.Contains(e.Reason, w.reason) || e.Body != w.body {
			t.Errorf("%s: %+v, want %+v", key, e, w)
		}
	}
	if e := all["am_inbox:1"]; e.Resolution != "auto" || e.ClosedAt == "" {
		t.Errorf("false drop not closed auto: %+v", e)
	}
	if e := all["am_inbox:3"]; e.Sender != "chief" || e.RecipientName != "worker-d" || e.SentAt != Stamp(sent) {
		t.Errorf("%+v", e)
	}

	// The held message lands: its entry closes as delivered_late.
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	amtest.State(t, state, fixtureSessions, []amtest.Row{
		{ID: 3, To: "dialog", FromName: "chief", Body: "synthetic held dialog", Sent: sent, Delivered: now},
	})
	scan(t, ib)
	if e := entries(t, ib, true)["am_inbox:3"]; e.State != "resolved" || e.Resolution != "delivered_late" {
		t.Fatalf("%+v", e)
	}
	// Rows that aged out of state.db leave their entries as they were.
	if e := entries(t, ib, true)["am_inbox:2"]; !e.Open() {
		t.Fatalf("%+v", e)
	}
}

func TestScanWithoutStateDBRecordsNothing(t *testing.T) {
	at(t, t0)
	deliveryEnv(t)
	ib, _, _ := open(t)
	rep := scan(t, ib)
	if !strings.HasPrefix(rep.Unavailable, "no agent-manager state.db at") || rep.Rows != 0 {
		t.Fatalf("%+v", rep)
	}
	if !strings.HasPrefix(rep.Text(), "unavailable: no agent-manager state.db") {
		t.Fatal(rep.Text())
	}
	if es := entries(t, ib, true); len(es) != 0 {
		t.Fatalf("%+v", es)
	}
}

func TestConcurrentScansKeepOneRowPerMessage(t *testing.T) {
	at(t, t0)
	state, _ := deliveryEnv(t)
	sent := Now().Add(-time.Hour)
	var rows []amtest.Row
	for i := range 40 {
		rows = append(rows, amtest.Row{ID: int64(i + 1), To: "dead", FromName: "chief", Body: "synthetic " + strconv.Itoa(i), Sent: sent})
	}
	amtest.State(t, state, fixtureSessions, rows)
	ib, _, path := open(t)
	other, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := ib
			if i%2 == 1 {
				h = other
			}
			_, errs[i] = h.ScanDelivery()
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n, distinct int
	if err := ib.conn.QueryRowContext(ctx, "SELECT COUNT(*), COUNT(DISTINCT source_id) FROM delivery_log").Scan(&n, &distinct); err != nil {
		t.Fatal(err)
	}
	if n != 40 || distinct != 40 {
		t.Fatalf("%d rows, %d distinct", n, distinct)
	}
}

func TestTransientRefusalOpensThenRetriedClosesIt(t *testing.T) {
	at(t, t0)
	deliveryEnv(t)
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "idle")}
	fake.failFirst = 2
	answerTo(t, ib, "7", "s1")
	open := entries(t, ib, false)
	if len(open) != 1 {
		t.Fatalf("%+v", open)
	}
	var e Entry
	for _, v := range open {
		e = v
	}
	if e.Kind != KindRefused || e.Source != SourceSend || e.RecipientID != "s1" || e.RecipientName != "spec-s1" ||
		e.Reason != "attempt 1: "+rateLimited || !strings.Contains(e.Body, "7.1") {
		t.Fatalf("%+v", e)
	}
	advance(t, 16*time.Second)
	pass(t, ib)
	if e, _ = ib.Entry(e.ID); !e.Open() || e.Reason != "attempt 2: "+rateLimited {
		t.Fatalf("second refusal: %+v", e)
	}
	advance(t, 31*time.Second)
	pass(t, ib)
	if e, _ = ib.Entry(e.ID); e.State != "resolved" || e.Resolution != "retried" {
		t.Fatalf("not closed by the send that landed: %+v", e)
	}
}

func TestGivenUpRefusalStaysOpen(t *testing.T) {
	at(t, t0)
	deliveryEnv(t)
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "idle")}
	fake.failFirst = 1000
	answerTo(t, ib, "7", "s1")
	for range 40 {
		advance(t, time.Minute)
		pass(t, ib)
	}
	es := entries(t, ib, true)
	if len(es) != 1 {
		t.Fatalf("one retry chain, one entry: %+v", es)
	}
	for _, e := range es {
		if !e.Open() || !strings.HasPrefix(e.Reason, GaveUp) {
			t.Fatalf("%+v", e)
		}
	}
}

func TestFailedChatOpensARefusal(t *testing.T) {
	at(t, t0)
	deliveryEnv(t)
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	fake.chiefErr = "message is over 8000 bytes"
	c, err := ib.SendChat("", "synthetic chat")
	if err != nil || c.Status != "failed" {
		t.Fatalf("%+v %v", c, err)
	}
	e := entries(t, ib, false)["zen_inbox_send:chat:"+c.Label()]
	if e.Kind != KindRefused || e.RecipientID != "c1" || e.RecipientName != "chief" || e.Body != "synthetic chat" ||
		e.Reason != "message is over 8000 bytes" {
		t.Fatalf("%+v", e)
	}
}

func TestFailedAnswersOpenARefusalThatARetryCloses(t *testing.T) {
	at(t, t0)
	deliveryEnv(t)
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "idle")}
	fake.fail = "bad request"
	answerTo(t, ib, "7", "s1")
	e := entries(t, ib, false)["zen_inbox_send:answers:s1:7.1"]
	if e.Kind != KindRefused || e.Reason != "bad request" {
		t.Fatalf("%+v", entries(t, ib, true))
	}
	fake.fail = ""
	if _, err := ib.Retry("s1"); err != nil {
		t.Fatal(err)
	}
	if e, _ = ib.Entry(e.ID); e.Resolution != "retried" {
		t.Fatalf("%+v", e)
	}
}

// toolCalls creates zen-telemetry's table in the inbox db with the given
// send_session calls: ok, sender, ago.
func toolCalls(t *testing.T, ib *Inbox, calls ...struct {
	ok     bool
	sender string
	ago    time.Duration
}) {
	t.Helper()
	if err := ib.Exec(`CREATE TABLE IF NOT EXISTS tool_calls (id INTEGER PRIMARY KEY, session_id TEXT NOT NULL,
		tool_use_id TEXT NOT NULL, am_session TEXT NOT NULL DEFAULT '', ts TEXT NOT NULL, tool TEXT NOT NULL,
		mcp_server TEXT NOT NULL DEFAULT '', ok INTEGER NOT NULL, error TEXT NOT NULL DEFAULT '', source TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i, c := range calls {
		errText := ""
		if !c.ok {
			errText = "too many messages to this session in the last minute"
		}
		if err := ib.Exec("INSERT INTO tool_calls (session_id, tool_use_id, am_session, ts, tool, mcp_server, ok, error, source)"+
			" VALUES ('c', ?, ?, ?, 'mcp__agent-manager__send_session', 'agent-manager', ?, ?, 'hook')",
			"u"+strconv.Itoa(i), c.sender, Now().Add(-c.ago).Format(telemetryTS), c.ok, errText); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTelemetryRefusalsCloseOnARetryWithinTenMinutes(t *testing.T) {
	at(t, t0)
	deliveryEnv(t)
	ib, _, _ := open(t)
	type call = struct {
		ok     bool
		sender string
		ago    time.Duration
	}
	toolCalls(t, ib,
		call{false, "w1", 30 * time.Minute}, call{true, "w1", 25 * time.Minute}, // retried in 5 min
		call{false, "w2", 30 * time.Minute}, call{true, "w2", 19 * time.Minute}, // 11 min later: too late
		call{false, "w3", 30 * time.Minute}, call{true, "w1", 29 * time.Minute}, // another sender's success
		call{false, "w4", 48 * time.Hour}, // outside the scan window
	)
	scan(t, ib)
	all := entries(t, ib, true)
	if len(all) != 3 {
		t.Fatalf("%+v", all)
	}
	if e := all["telemetry:1"]; e.State != "resolved" || e.Resolution != "retried" || e.Sender != "w1" || e.Kind != KindRefused {
		t.Fatalf("%+v", e)
	}
	for _, key := range []string{"telemetry:3", "telemetry:5"} {
		if e := all[key]; !e.Open() || !strings.Contains(e.Reason, "too many messages") {
			t.Fatalf("%s: %+v", key, e)
		}
	}
}

func TestPruneDropsClosedEntriesAfterThirtyDays(t *testing.T) {
	at(t, t0)
	deliveryEnv(t)
	ib, _, _ := open(t)
	now := Now()
	for i, c := range []struct {
		state string
		age   time.Duration
	}{{"resolved", 31 * 24 * time.Hour}, {"dismissed", 29 * 24 * time.Hour}, {"open", 90 * 24 * time.Hour}} {
		closed := ""
		if c.state != "open" {
			closed = Stamp(now.Add(-c.age))
		}
		if err := ib.Exec("INSERT INTO delivery_log (kind, source, source_id, first_seen, last_seen, state, closed_at)"+
			" VALUES ('lost', 'am_inbox', ?, ?, ?, ?, ?)", strconv.Itoa(i), Stamp(now.Add(-c.age)), Stamp(now), c.state, closed); err != nil {
			t.Fatal(err)
		}
	}
	scan(t, ib)
	all := entries(t, ib, true)
	if _, ok := all["am_inbox:0"]; ok || len(all) != 2 {
		t.Fatalf("%+v", all)
	}
}

func TestResendGoesToTheLiveNamesakeWithThePrefix(t *testing.T) {
	at(t, t0)
	deliveryEnv(t)
	ib, fake, _ := open(t)
	if err := ib.Exec("INSERT INTO delivery_log (kind, source, source_id, sender, recipient_id, recipient_name, body, sent_at,"+
		" first_seen, last_seen) VALUES ('lost', 'am_inbox', '2499', 'worker', 'old', 'chief', 'synthetic lost', ?, ?, ?)",
		Stamp(Now()), stamp(), stamp()); err != nil {
		t.Fatal(err)
	}
	e := entries(t, ib, false)["am_inbox:2499"]
	fake.sessions = am.Sessions{}
	if _, _, err := ib.Resend(e.ID); err == nil || !strings.Contains(err.Error(), "chief is down") {
		t.Fatalf("resend with chief down: %v", err)
	}
	fake.sessions = am.Sessions{session("new", "chief", "idle")}
	_, text, err := ib.Resend(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := "(resent from the cockpit: worker's message 2499 of " + Now().Local().Format("15:04") + ") synthetic lost"
	if len(fake.sends) != 1 || fake.sends[0] != [2]string{"new", want} || !strings.Contains(text, "to chief as message 42") {
		t.Fatalf("%q %q", fake.sends, text)
	}
	if e, _ = ib.Entry(e.ID); e.Resolution != "resent" {
		t.Fatalf("%+v", e)
	}
	if _, _, err := ib.Resend(e.ID); err == nil {
		t.Fatal("a closed entry was resent")
	}
}

func TestDismiss(t *testing.T) {
	at(t, t0)
	deliveryEnv(t)
	ib, _, _ := open(t)
	if err := ib.Exec("INSERT INTO delivery_log (kind, source, source_id, first_seen, last_seen) VALUES ('held', 'am_inbox', '5', ?, ?)",
		stamp(), stamp()); err != nil {
		t.Fatal(err)
	}
	e := entries(t, ib, false)["am_inbox:5"]
	if got, err := ib.Dismiss(e.ID); err != nil || got.State != "dismissed" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := ib.Dismiss(e.ID); err == nil || !strings.Contains(err.Error(), "already dismissed") {
		t.Fatal(err)
	}
	if _, err := ib.Dismiss(99); err == nil || !strings.Contains(err.Error(), "no delivery entry 99") {
		t.Fatal(err)
	}
}

func TestCheckMessageForAnySender(t *testing.T) {
	at(t, t0)
	state, projects := deliveryEnv(t)
	sent := Now().Add(-10 * time.Minute)
	amtest.Transcript(t, projects, "-w-chief", "a-chief", Now(), amtest.Delivered("w", "synthetic seen"))
	amtest.State(t, state, fixtureSessions, []amtest.Row{
		{ID: 11, To: "chief", From: "other", FromName: "w", Body: "synthetic seen", Sent: sent, Delivered: sent, Dropped: sent},
		{ID: 12, To: "chief", From: "other", FromName: "w", Body: "synthetic gone", Sent: sent, Delivered: sent, Dropped: sent},
		{ID: 13, To: "dialog", From: "other", FromName: "w", Body: "synthetic waits", Sent: sent},
		{ID: 14, To: "chief", From: "other", FromName: "w", Body: "synthetic fine", Sent: sent, Delivered: sent},
	})
	ib, _, _ := open(t)
	for id, want := range map[string]string{"11": "false drop", "12": "lost", "13": "held: a dialog on screen",
		"14": "delivered", "15": "unknown", "x": "unknown"} {
		c, err := ib.CheckMessage(id)
		if err != nil || c.Status != want {
			t.Errorf("%s: %+v %v, want %q", id, c, err, want)
		}
	}
	if e := entries(t, ib, false)["am_inbox:12"]; e.Kind != KindLost {
		t.Fatalf("check did not record the loss: %+v", e)
	}
	// Aged out of state.db, the log still answers.
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	if c, _ := ib.CheckMessage("12"); c.Status != "lost" {
		t.Fatalf("%+v", c)
	}
	if c, _ := ib.CheckMessage("14"); c.Status != "unknown" || !strings.Contains(c.Why, "no agent-manager state.db") {
		t.Fatalf("%+v", c)
	}
}

func TestV14DumpMigratesToLatest(t *testing.T) {
	dump, err := os.ReadFile("../../testdata/xver/v14.sql")
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
	var retries int
	if err := raw.QueryRow("SELECT COUNT(*) FROM send_retry").Scan(&retries); err != nil || retries != 1 {
		t.Fatalf("fixture send_retry: %d %v", retries, err)
	}
	_ = raw.Close()
	ib, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	defer ib.Close()
	if v, _ := ib.userVersion(); v != 15 {
		t.Fatalf("user_version %d", v)
	}
	if es := entries(t, ib, true); len(es) != 0 {
		t.Fatalf("%+v", es)
	}
	if th, err := ib.Thread("933"); err != nil || len(th.Items) != 5 {
		t.Fatalf("%+v %v", th, err)
	}
	var target string
	if err := ib.conn.QueryRowContext(ctx, "SELECT target FROM send_retry").Scan(&target); err != nil || target != "s1" {
		t.Fatalf("%q %v", target, err)
	}
}
