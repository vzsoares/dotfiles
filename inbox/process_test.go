package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// parallel starts every command before waiting on any.
func parallel(cmds []*exec.Cmd) []result {
	out := make([]result, len(cmds))
	var wg sync.WaitGroup
	for i, c := range cmds {
		wg.Go(func() { out[i] = run(c) })
	}
	wg.Wait()
	return out
}

func TestConcurrentAsksGetDistinctConsecutiveIDs(t *testing.T) {
	f := newFake(t)
	f.zen(nil, "list")
	var cmds []*exec.Cmd
	for i := range 6 {
		cmds = append(cmds, f.cmd([]string{"AGENT_MANAGER_SESSION_ID=s1"}, zenBin, "ask", "9", "-q", fmt.Sprintf("q%d", i)))
	}
	var ids []string
	for _, r := range parallel(cmds) {
		if r.code != 0 || strings.Contains(r.stderr, "locked") {
			t.Fatalf("%+v", r)
		}
		ids = append(ids, strings.TrimSpace(r.stdout))
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"9.1", "9.2", "9.3", "9.4", "9.5", "9.6"}) {
		t.Fatal(ids)
	}
}

func TestConcurrentAnswersOneWinsFlushOnce(t *testing.T) {
	f := newFake(t)
	f.zen(nil, "ask", "7", "-q", "Q?", "-o", "a", "-o", "b", "--session", "s1")
	rs := parallel([]*exec.Cmd{f.cmd(nil, zenBin, "answer", "7.1", "a"), f.cmd(nil, zenBin, "answer", "7.1", "b")})
	codes := []int{rs[0].code, rs[1].code}
	slices.Sort(codes)
	if !slices.Equal(codes, []int{0, 1}) || len(f.sends()) != 1 {
		t.Fatalf("%+v sends %v", rs, f.sends())
	}
}

func TestConcurrentAnswersOnLastTwoFlushOnce(t *testing.T) {
	f := newFake(t)
	for range 2 {
		f.zen(nil, "ask", "7", "-q", "Q?", "-o", "a", "-o", "b", "--session", "s1")
	}
	rs := parallel([]*exec.Cmd{f.cmd(nil, zenBin, "answer", "7.1", "a"), f.cmd(nil, zenBin, "answer", "7.2", "a")})
	if rs[0].code != 0 || rs[1].code != 0 {
		t.Fatalf("%+v", rs)
	}
	sends := f.sends()
	if len(sends) != 1 || !strings.Contains(sends[0][2], "7.1 ") || !strings.Contains(sends[0][2], "7.2 ") {
		t.Fatal(sends)
	}
}

// passes runs n concurrent `retry --waiting` and fails on any error exit.
func (f *fake) passes(n int) {
	f.t.Helper()
	var cmds []*exec.Cmd
	for range n {
		cmds = append(cmds, f.cmd(nil, zenBin, "retry", "--waiting"))
	}
	for _, r := range parallel(cmds) {
		if r.code != 0 || strings.Contains(r.stderr, "locked") {
			f.t.Fatalf("%+v", r)
		}
	}
}

const chiefUp = `[{"id": "c1", "name": "chief", "status": "idle", "running": true, "archived": false}]`

func TestConcurrentPassesSendAWaitingAnswerBatchOnce(t *testing.T) {
	f := newFake(t)
	f.sessions(session("s1", "dead", false))
	f.zen(nil, "ask", "7", "-q", "Q?", "-o", "a", "-o", "b", "--session", "s1")
	if r := f.zen(nil, "answer", "7.1", "a"); r.code != 3 || !strings.HasPrefix(r.stderr, "waiting: session s1 is down") {
		t.Fatalf("%+v", r)
	}
	f.sessions(session("s1", "idle", true))
	f.passes(6)
	if s := f.sends(); len(s) != 1 || s[0][1] != "s1" {
		t.Fatalf("sends %q", s)
	}
	if got := f.sqlite("SELECT delivery, message_id FROM decisions WHERE id = '7.1'"); got != "sent|42\n" {
		t.Fatalf("%q", got)
	}
}

func TestConcurrentPassesSendAWaitingReplyOnce(t *testing.T) {
	f := newFake(t)
	f.write("sessions.json", chiefUp)
	if r := f.zen(nil, "note", "--title", "Done", "--session", "c1"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	now := "strftime('%Y-%m-%dT%H:%M:%S+00:00', 'now')"
	f.sqlite("UPDATE notes SET reply = 'thanks', replied_at = " + now + ", claimed_at = " + now + "," +
		" delivery = 'waiting', delivery_error = 'no live session named chief' WHERE id = 1;" +
		" INSERT INTO note_replies (note_id, text, replied_at) VALUES (1, 'thanks', " + now + ")")
	f.passes(6)
	s := f.sends()
	if len(s) != 1 || s[0][1] != "c1" || s[0][2] != "[zen-inbox] Reply to your note n1 (Done):\nthanks" {
		t.Fatalf("sends %q", s)
	}
	if got := f.sqlite("SELECT delivery, read_at != '' FROM notes WHERE id = 1"); got != "sent|1\n" {
		t.Fatalf("%q", got)
	}
}

// The chat goes through the real zen-chief; only agent-manager is fake.
func TestConcurrentPassesSendAWaitingChatOnce(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal("python3 is required: zen-chief is a python script")
	}
	f := newFake(t)
	script, err := filepath.Abs("../scripts/chief.py")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(script, filepath.Join(f.root, "bin", "zen-chief")); err != nil {
		t.Fatal(err)
	}
	f.write("sessions.json", chiefUp)
	f.zen(nil, "list")
	f.sqlite("INSERT INTO chats (task, text, sent_at, status, error) VALUES ('', 'ping'," +
		" strftime('%Y-%m-%dT%H:%M:%S+00:00', 'now'), 'waiting', 'no live session named ''chief''')")
	f.passes(6)
	s := f.sends()
	if len(s) != 1 || !slices.Equal(s[0], []string{"send", "c1", "ping", "--json", "--from", "zenha"}) {
		t.Fatalf("sends %q", s)
	}
	if got := f.sqlite("SELECT status, message_id FROM chats WHERE id = 1"); got != "queued|42\n" {
		t.Fatalf("%q", got)
	}
}

func (f *fake) load(dump string) {
	f.t.Helper()
	sql, err := os.ReadFile(dump)
	if err != nil {
		f.t.Fatal(err)
	}
	c := exec.Command("sqlite3", f.db)
	c.Stdin = strings.NewReader(string(sql))
	if out, err := c.CombinedOutput(); err != nil {
		f.t.Fatalf("%v\n%s", err, out)
	}
}

func TestConcurrentFirstOpensOfV1DB(t *testing.T) {
	f := newFake(t)
	f.load("testdata/xver/v1.sql")
	var cmds []*exec.Cmd
	for range 6 {
		cmds = append(cmds, f.cmd(nil, zenBin, "list", "--json"))
	}
	for _, r := range parallel(cmds) {
		if r.code != 0 || !strings.HasPrefix(r.stdout, `[{"id": "7.1"`) {
			t.Fatalf("%+v", r)
		}
	}
	if v := strings.TrimSpace(f.sqlite("PRAGMA user_version")); v != "16" {
		t.Fatal(v)
	}
}

// The fixtures and their expected outputs were made by inbox.py.
func TestPythonDBsOpenListAndAnswer(t *testing.T) {
	now := []string{"ZEN_INBOX_NOW=2026-01-01T12:00:00+00:00"}
	for _, v := range []string{"v1", "v2"} {
		f := newFake(t)
		f.load("testdata/xver/" + v + ".sql")
		expect := func(name string) {
			t.Helper()
			want, err := os.ReadFile("testdata/xver/" + v + "." + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			if r := f.zen(now, "list", "--all", "--json"); r.stdout != string(want) {
				t.Fatalf("%s %s:\n got %s\nwant %s", v, name, r.stdout, want)
			}
		}
		expect("open")
		if v := strings.TrimSpace(f.sqlite("PRAGMA user_version")); v != "16" {
			t.Fatalf("user_version %s", v)
		}
		f.zen(now, "answer", "7.1", "b")
		f.zen(now, "answer", "7.2", "--text", "done")
		expect("answered")
		if len(f.sends()) != 1 {
			t.Fatalf("%s: sends %v", v, f.sends())
		}
	}
}

func TestGoRowsMatchPythonColumnFormats(t *testing.T) {
	f := newFake(t)
	f.load("testdata/xver/v2.sql")
	f.zen(nil, "ask", "9", "-q", "Go?", "-o", "a", "-o", "b", "--title", "T", "--brief", "B", "--session", "s2")
	f.zen(nil, "answer", "9.1", "a", "--note", "n")
	cols := "typeof(id), typeof(task), typeof(seq), typeof(gate), typeof(session_id), typeof(session_name)," +
		" typeof(question), typeof(context), typeof(rec), typeof(status), typeof(amended), typeof(answer_kind)," +
		" typeof(answer_option), typeof(answer_text), typeof(note), typeof(created_at), typeof(updated_at)," +
		" typeof(answered_at), typeof(delivery), typeof(message_id), typeof(delivery_error), typeof(rev)"
	py := f.sqlite("SELECT " + cols + " FROM decisions WHERE id = '7.g'")
	gorow := f.sqlite("SELECT " + cols + " FROM decisions WHERE id = '9.1'")
	if py != gorow {
		t.Fatalf("column types differ:\npython %s\ngo     %s", py, gorow)
	}
	stamp := "'[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]+00:00'"
	got := f.sqlite("SELECT created_at GLOB " + stamp + ", updated_at GLOB " + stamp + ", answered_at GLOB " + stamp +
		", message_id, delivery FROM decisions WHERE id = '9.1'")
	if got != "1|1|1|42|sent\n" {
		t.Fatalf("%q", got)
	}
	bcols := "typeof(session_id), typeof(task), typeof(title), typeof(brief), typeof(updated_at)"
	if py, gorow := f.sqlite("SELECT "+bcols+" FROM decision_briefs WHERE task = '7'"),
		f.sqlite("SELECT "+bcols+" FROM decision_briefs WHERE task = '9'"); py != gorow {
		t.Fatalf("brief types differ: %s / %s", py, gorow)
	}
	if got := f.sqlite("SELECT letter, text FROM decision_options WHERE decision_id = '9.1' ORDER BY letter"); got != "a|a\nb|b\n" {
		t.Fatalf("%q", got)
	}
}

// #582: an agent withdrawing its own last open decision gets the held
// answers on stdout; agent-manager is never called from its process tree.
func TestSelfWithdrawPrintsHeldAnswers(t *testing.T) {
	f := newFake(t)
	s1 := []string{"AGENT_MANAGER_SESSION_ID=s1"}
	f.zen(s1, "ask", "7", "-q", "Answered?", "-o", "left", "-o", "right")
	f.zen(s1, "ask", "7", "-q", "Withdrawn?")
	f.zen(nil, "answer", "7.1", "b", "--note", "ship it")
	r := f.zen(s1, "withdraw", "7.2")
	want := "withdrew 7.2\n[zen-inbox] Answers to your decisions:\n7.1 Answered? → b: right\n  Note: ship it\n"
	if r.code != 0 || r.stdout != want || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	if len(f.sends()) != 0 {
		t.Fatalf("sent %v", f.sends())
	}
	if got := f.sqlite("SELECT delivery, message_id, delivery_error FROM decisions WHERE id = '7.1'"); got != "sent||\n" {
		t.Fatalf("%q", got)
	}
	if r := f.zen(nil, "retry", "s1"); r.code != 1 {
		t.Fatalf("handed-back answers must not be retryable: %+v", r)
	}
}

func TestForcedWithdrawByOtherSessionStillSends(t *testing.T) {
	f := newFake(t)
	s1 := []string{"AGENT_MANAGER_SESSION_ID=s1"}
	f.zen(s1, "ask", "7", "-q", "Answered?", "-o", "a", "-o", "b")
	f.zen(s1, "ask", "7", "-q", "Withdrawn?")
	f.zen(nil, "answer", "7.1", "b")
	r := f.zen([]string{"AGENT_MANAGER_SESSION_ID=chief"}, "withdraw", "7.2", "--force")
	if r.code != 0 || r.stdout != "withdrew 7.2\nsent 7.1 to s1\n" {
		t.Fatalf("%+v", r)
	}
	if s := f.sends(); len(s) != 1 || s[0][1] != "s1" || !slices.Contains(s[0], "--from") {
		t.Fatal(s)
	}
}
