package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

// world relists s1 and, unless chief is "", the one chief session with those
// statuses; a "dead" chief makes zen-chief report no live chief.
func (d *drv) world(s1, chief string) {
	d.am.mu.Lock()
	defer d.am.mu.Unlock()
	one := sess("s1")
	one["status"], one["running"] = s1, s1 != "dead"
	d.am.sessions = am.Sessions{one, sess("s2")}
	d.am.chiefErr, d.am.chiefDown = "", false
	if chief != "" {
		d.am.sessions = append(d.am.sessions,
			am.Session{"id": "c1", "name": inbox.NoteSender, "status": chief, "running": chief != "dead"})
	}
	if chief == "dead" {
		d.am.chiefErr, d.am.chiefDown = "no live session named 'chief'", true
	}
}

func (d *drv) chiefIs(status string) { d.world("idle", status) }

func (d *drv) sessionIs(status string) { d.world(status, "") }

func (d *drv) chat(id int64) inbox.Chat {
	d.t.Helper()
	c, err := d.ib.Chat(id)
	if err != nil {
		d.t.Fatal(err)
	}
	return c
}

// later moves the pinned clock, which the store and the panel both read.
func later(t *testing.T, by time.Duration) {
	at, _ := time.Parse(time.RFC3339, pinnedNow)
	t.Setenv("ZEN_INBOX_NOW", at.Add(by).Format(time.RFC3339))
}

// A chat held only behind a chat being sent is not chief being down.
func TestChatQueuedBehindOneBeingSentIsNotShownAsChiefDown(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.chiefIs("idle")
	if c, err := d.ib.AddChat("", "in flight"); err != nil || c.Status != "sending" {
		t.Fatalf("%+v %v", c, err)
	}
	d.keys("c", "next", "ctrl+d")
	if c := d.chat(2); c.Status != "waiting" || c.Error != "queued behind c1" {
		t.Fatalf("%+v", c)
	}
	if h := d.head(); h != "cockpit · ◷ queued behind c1 · 1 held" {
		t.Fatalf("header %q", h)
	}
	later(t, 20*time.Minute)
	d.pollPass()
	if h := d.head(); strings.Contains(h, "down") || len(d.am.notices) != 0 {
		t.Fatalf("header %q notices %q", h, d.am.notices)
	}
}

func TestChiefDownQueuesTheChatAndAPassSendsIt(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.chiefIs("dead")
	d.keys("c", "ping")
	d.keys("ctrl+d")
	if d.m.scr != scList || d.m.chiefTA.Value() != "" || !d.m.flashOK {
		t.Fatalf("box not cleared or not back on the list:\n%s", d.view())
	}
	if c := d.chat(1); c.Status != "waiting" || c.Error != "no live session named 'chief'" {
		t.Fatalf("%+v", c)
	}
	d.has("queued for chief: sent when chief is back", "Waiting · sent when the session is back",
		"waiting chief: 1 message(s) · no live session named 'chief' · 0s")
	if d.head() != "cockpit · ◷ chief is down · 1 queued · sends when back" {
		t.Fatalf("header %q", d.head())
	}
	d.keys("c", "pong", "ctrl+d")
	d.fits()
	golden(t, "waiting-chief-92x29", d.view())
	if len(d.am.chiefs) != 0 || len(d.am.notices) != 0 || len(d.am.bells) != 0 {
		t.Fatalf("queueing sent, rang or bannered: %q %q %q", d.am.chiefs, d.am.notices, d.am.bells)
	}
	d.pollPass()
	if len(d.am.chiefs) != 0 {
		t.Fatalf("sent while chief is down: %q", d.am.chiefs)
	}
	d.chiefIs("idle")
	d.pollPass()
	d.has("sent to chief: 1 message", "waiting chief: 1 message(s) · queued behind c1")
	if !d.m.flashOK || d.chat(1).Status != "queued" {
		t.Fatalf("%+v\n%s", d.chat(1), d.view())
	}
	d.pollPass()
	if len(d.am.chiefs) != 2 || d.am.chiefs[0] != "ping" || d.am.chiefs[1] != "pong" {
		t.Fatalf("sent %q", d.am.chiefs)
	}
	if strings.Contains(d.view(), "Waiting ·") || strings.Contains(d.head(), "is down") {
		t.Fatalf("the waiting header or group stayed:\n%s", d.view())
	}
}

func TestSeveralChiefsKeepTheBoxAndFailTheChat(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.am.chiefErr = "2 live sessions named 'chief': a, b"
	d.keys("c", "ping")
	d.keys("ctrl+d")
	if d.m.scr != scChief || d.m.chiefTA.Value() != "ping" || !strings.Contains(d.text(), "2 live sessions named 'chief': a, b") {
		t.Fatalf("lost the box, the draft or the error:\n%s", d.view())
	}
	if c := d.chat(1); c.Status != "failed" || c.Error != "2 live sessions named 'chief': a, b" {
		t.Fatalf("%+v", c)
	}
	d.keys("esc")
	d.poll()
	if strings.Contains(d.view(), "Waiting ·") {
		t.Fatalf("a failed chat is shown as waiting:\n%s", d.view())
	}
}

func TestReplyToADownSenderQueuesIt(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.addNote("707", "Task 707 merged", "Tag v1.2.3.")
	d.chiefIs("dead")
	d.poll()
	d.cursorTo("Task 707")
	d.keys("f", "thanks", "ctrl+d")
	if d.m.scr != scList || !d.m.flashOK {
		t.Fatalf("not back on the list:\n%s", d.view())
	}
	d.has("reply queued: sent when chief is back", "waiting reply n1 to chief · no live session named chief · 0s")
	if strings.Contains(d.view(), "Failed delivery") || d.head() != "cockpit · ◷ chief is down · 1 queued · sends when back" {
		t.Fatalf("header %q\n%s", d.head(), d.view())
	}
	if n := d.noteRow(1); n.Delivery != "waiting" || !n.Unread() || len(d.am.sends) != 0 {
		t.Fatalf("%+v sends %q", n, d.am.sends)
	}
	d.cursorTo("● Task 707")
	d.keys("enter")
	d.has("↩ waiting for chief · 0s: thanks")
	d.fits()
	golden(t, "waiting-note-92x29", d.view())
	d.keys("f")
	if d.m.scr != scNote || !strings.Contains(d.text(), "n1 has a reply waiting for chief; it is sent when chief is back") {
		t.Fatalf("a second reply opened:\n%s", d.view())
	}
	d.keys("esc")
	d.chiefIs("idle")
	d.pollPass()
	d.has("sent to chief: 1 reply")
	if n := d.noteRow(1); n.Delivery != "sent" || n.Unread() || len(d.am.sends) != 1 || d.am.sends[0][0] != "c1" {
		t.Fatalf("%+v sends %q", n, d.am.sends)
	}
}

// answerDown answers s1's one question while s1 is down.
func (d *drv) answerDown() {
	d.t.Helper()
	d.ask("7", "s1", "Which way?", "left", "right")
	d.sessionIs("dead")
	d.poll()
	d.cursorTo("spec-s1 ·")
	d.rawKeys("enter", "enter", "a", "enter")
}

func TestSendToADownSessionWaitsInsteadOfFailing(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.answerDown()
	d.has("waiting: session s1 is down", "Waiting · sent when the session is back",
		"waiting spec-s1: 1 answer(s) · session s1 is down · 0s")
	if strings.Contains(d.view(), "Failed delivery") || len(d.am.sends) != 0 || d.get("7.1").Delivery != "waiting" || !d.m.flashOK {
		t.Fatalf("%+v\n%s", d.get("7.1"), d.view())
	}
	d.sessionIs("idle")
	d.pollPass()
	d.has("sent to spec-s1: 1 answer")
	if d.get("7.1").Delivery != "sent" || len(d.am.sends) != 1 || strings.Contains(d.view(), "Waiting ·") {
		t.Fatalf("%+v\n%s", d.get("7.1"), d.view())
	}
}

func TestWaitingAndFailedTogether(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.ask("8", "s2", "Other?", "a", "b")
	d.am.fail = "send refused"
	d.poll()
	d.cursorTo("spec-s2")
	d.rawKeys("enter", "enter", "a", "enter")
	d.am.fail = ""
	d.answerDown()
	d.world("dead", "dead")
	d.keys("c", "ping", "ctrl+d")
	d.poll()
	v := d.view()
	failed, waiting, threads := strings.Index(v, "Failed delivery · 1"), strings.Index(v, "Waiting · sent when"), strings.Index(v, "Inbox")
	if failed < 0 || waiting < failed || threads > failed {
		t.Fatalf("groups missing or out of order:\n%s", v)
	}
	if d.head() != "cockpit · ✕ delivery failed · spec-s2 · r retry" {
		t.Fatalf("header %q", d.head())
	}
	d.has("retry spec-s2: 1 answer(s) · send refused", "waiting spec-s1: 1 answer(s)", "waiting chief: 1 message(s)")
	d.fits()
	golden(t, "waiting-and-failed-92x29", v)
}

func TestEnterAndRRetryAWaitingRowNow(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	for _, k := range []string{"enter", "r"} {
		d := newDrv(t, 92, 29)
		d.answerDown()
		d.chiefIs("dead")
		d.am.sessions[0]["status"], d.am.sessions[0]["running"] = "dead", false
		d.keys("c", "ping", "ctrl+d")
		d.poll()
		d.cursorTo("waiting spec-s1")
		if !strings.Contains(d.last(), "enter retry") {
			t.Fatalf("footer %q", d.last())
		}
		d.rawKeys(k)
		d.has("waiting: session s1 is down")
		if len(d.am.sends) != 0 || d.get("7.1").Delivery != "waiting" {
			t.Fatalf("%s sent to a down session: %q", k, d.am.sends)
		}
		d.chiefIs("idle")
		d.poll()
		d.cursorTo("waiting spec-s1")
		d.rawKeys(k)
		if d.get("7.1").Delivery != "sent" || len(d.am.sends) != 1 || !d.m.flashOK {
			t.Fatalf("%s did not retry the answers:\n%s", k, d.view())
		}
		d.poll()
		d.cursorTo("waiting chief")
		d.rawKeys(k)
		if len(d.am.chiefs) != 1 || d.chat(1).Status != "queued" {
			t.Fatalf("%s did not send the chat: %+v\n%s", k, d.chat(1), d.view())
		}
		d.has("message c1 sent to chief")
	}
}

func TestRWithoutAFailureRetriesTheFirstWaitingRow(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.answerDown()
	d.sessionIs("idle")
	d.poll()
	d.cursorTo("Inbox")
	d.rawKeys("r")
	if d.get("7.1").Delivery != "sent" || len(d.am.sends) != 1 {
		t.Fatalf("%+v\n%s", d.get("7.1"), d.view())
	}
}

func TestDownFifteenMinutesTurnsLoudOncePerOutage(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	t.Setenv("SSH_CONNECTION", "")
	d := newDrv(t, 92, 29)
	d.chiefIs("dead")
	d.keys("c", "ping", "ctrl+d")
	d.keys("c", "pong", "ctrl+d")
	later(t, 15*time.Minute-time.Second)
	d.pollPass()
	if d.head() != "cockpit · ◷ chief is down · 2 queued · sends when back" || len(d.am.notices) != 0 {
		t.Fatalf("loud too early: %q %q", d.head(), d.am.notices)
	}
	if _, st := d.m.header(); st.GetForeground() != bannerStyle.GetForeground() {
		t.Fatalf("quiet header is not in the banner style")
	}
	later(t, 15*time.Minute)
	d.pollPass()
	d.pollPass()
	if d.head() != "cockpit · ✕ chief down 15m · 2 queued · r retry" {
		t.Fatalf("header %q", d.head())
	}
	if _, st := d.m.header(); st.GetForeground() != failStyle.GetForeground() {
		t.Fatalf("loud header is not in the fail style")
	}
	want := [2]string{"zen-inbox · chief still down", "2 queued · oldest 15m"}
	if len(d.am.notices) != 1 || d.am.notices[0] != want {
		t.Fatalf("banners %q", d.am.notices)
	}
	// Back, drained, then down again: a new outage banners again.
	d.chiefIs("idle")
	d.pollPass()
	d.pollPass()
	d.poll()
	if strings.Contains(d.head(), "down") {
		t.Fatalf("header %q", d.head())
	}
	d.chiefIs("dead")
	d.keys("c", "again", "ctrl+d")
	later(t, 31*time.Minute)
	d.pollPass()
	if len(d.am.notices) != 2 || d.am.notices[1][1] != "1 queued · oldest 16m" {
		t.Fatalf("banners %q", d.am.notices)
	}
}

func TestSSHPanelPostsNoDownBanner(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	t.Setenv("SSH_CONNECTION", "1.2.3.4 5 6.7.8.9 22")
	d := newDrv(t, 92, 29)
	d.chiefIs("dead")
	d.keys("c", "ping", "ctrl+d")
	later(t, 20*time.Minute)
	d.pollPass()
	if d.head() != "cockpit · ✕ chief down 20m · 1 queued · r retry" || len(d.am.notices) != 0 {
		t.Fatalf("%q %q", d.head(), d.am.notices)
	}
}

func TestThreadShowsAWaitingChat(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.openThread843()
	d.chiefIs("dead")
	d.keys("c", "status?", "ctrl+d")
	if d.m.scr != scThread || d.chat(1).Status != "waiting" {
		t.Fatalf("screen %d %+v", d.m.scr, d.chat(1))
	}
	d.poll()
	d.keys("G")
	d.fits()
	d.has("✉ you → chief · 0s · waiting for chief", "status?")
	golden(t, "waiting-thread-92x29", d.view())
	d.chiefIs("idle")
	d.pollPass()
	if len(d.am.chiefs) != 1 || d.am.chiefs[0] != "[#843 Phone cockpit setup] status?" {
		t.Fatalf("%q", d.am.chiefs)
	}
}

func TestPhoneWaitingGolden(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, phoneW, phoneH)
	d.answerDown()
	d.world("dead", "dead")
	d.keys("c", "ping", "ctrl+d")
	d.poll()
	d.has("Waiting · sent when the session is back")
	d.fits()
	golden(t, "phone-waiting-44x24", d.view())
}
