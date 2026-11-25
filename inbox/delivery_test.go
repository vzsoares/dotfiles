package main

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/am/amtest"
)

// deliveryFixture writes a state.db where another session (w9) sent s1 a
// false drop (21), a lost message (22) and one still held (23).
func (f *fake) deliveryFixture() {
	f.t.Helper()
	sent := time.Now().Add(-10 * time.Minute)
	projects := filepath.Join(f.root, "projects")
	amtest.Transcript(f.t, projects, "-w-s1", "a-s1", time.Now(), amtest.Delivered("worker", "synthetic arrived"))
	amtest.State(f.t, filepath.Join(f.root, "state.db"),
		[]amtest.Session{{ID: "s1", Name: "spec-s1", Cwd: "/w/s1", Agent: "a-s1"}, {ID: "s2", Name: "spec-s2", Status: "waiting"}},
		[]amtest.Row{
			{ID: 21, To: "s1", From: "w9", FromName: "worker", Body: "synthetic arrived", Sent: sent, Delivered: sent, Dropped: sent},
			{ID: 22, To: "s1", From: "w9", FromName: "worker", Body: "synthetic vanished", Sent: sent, Delivered: sent, Dropped: sent},
			{ID: 23, To: "s2", From: "w9", FromName: "worker", Body: "synthetic waiting", Sent: sent},
		})
}

func TestDeliveryCheckAnySender(t *testing.T) {
	f := newFake(t)
	f.deliveryFixture()
	for id, want := range map[string]struct {
		out  string
		code int
	}{"21": {"false drop\n", 0}, "22": {"lost\n", 0}, "23": {"held: a dialog on screen\n", 0}, "99": {"unknown\n", 1}} {
		if r := f.zen(nil, "delivery", "check", id); r.stdout != want.out || r.code != want.code {
			t.Errorf("%s: %+v", id, r)
		}
	}
	r := f.zen(nil, "delivery", "check", "22", "--json")
	var got struct {
		MessageID string `json:"message_id"`
		Status    string
		Entry     struct {
			Kind, Body, Sender, State string
			RecipientID               string `json:"recipient_id"`
		}
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil || got.Status != "lost" || got.MessageID != "22" ||
		got.Entry.Kind != "lost" || got.Entry.Body != "synthetic vanished" || got.Entry.Sender != "worker" || got.Entry.RecipientID != "s1" {
		t.Fatalf("%+v %v %q", got, err, r.stdout)
	}
}

func TestDeliveryScanListDismissResend(t *testing.T) {
	f := newFake(t)
	f.deliveryFixture()
	r := f.zen(nil, "delivery", "scan", "--json")
	var rep struct {
		Unavailable    string
		Rows, Open     int
		Lost           int
		FalseDropsWeek int `json:"false_drops_week"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil || rep.Rows != 3 || rep.Open != 2 || rep.Lost != 1 || rep.FalseDropsWeek != 1 {
		t.Fatalf("%+v %v %+v", rep, err, r)
	}
	r = f.zen(nil, "delivery", "list", "--json")
	var es []struct {
		ID         int64
		Kind       string
		SourceID   string `json:"source_id"`
		State      string
		Resolution string
	}
	if err := json.Unmarshal([]byte(r.stdout), &es); err != nil || len(es) != 2 {
		t.Fatalf("%v %q", err, r.stdout)
	}
	lost, held := es[1], es[0]
	if lost.SourceID != "22" || held.SourceID != "23" {
		t.Fatalf("%+v", es)
	}
	if r = f.zen(nil, "delivery", "list"); !strings.Contains(r.stdout, "lost · worker → spec-s1 · 10m · dropped; not in its transcripts") {
		t.Fatalf("%q", r.stdout)
	}
	if r = f.zen(nil, "delivery", "list", "--all"); !strings.Contains(r.stdout, "false drop · worker → spec-s1") {
		t.Fatalf("%q", r.stdout)
	}

	id := func(n int64) string { return strconv.FormatInt(n, 10) }
	if r = f.zen(nil, "delivery", "dismiss", id(held.ID)); r.code != 0 || r.stdout != "dismissed entry "+id(held.ID)+"\n" {
		t.Fatalf("%+v", r)
	}
	if r = f.zen(nil, "delivery", "dismiss", id(held.ID)); r.code != 1 {
		t.Fatalf("%+v", r)
	}
	if r = f.zen(nil, "delivery", "dismiss", "x"); r.code != 2 {
		t.Fatalf("%+v", r)
	}

	f.sessions(session("s2", "idle", true))
	if r = f.zen(nil, "delivery", "resend", id(lost.ID)); r.code != 3 || !strings.Contains(r.stderr, "spec-s1 is down") {
		t.Fatalf("resend to a down recipient: %+v", r)
	}
	f.sessions(session("s1", "idle", true))
	if r = f.zen(nil, "delivery", "resend", id(lost.ID)); r.code != 0 || r.stdout != "resent entry "+id(lost.ID)+" to spec-s1 as message 42\n" {
		t.Fatalf("%+v", r)
	}
	sends := f.sends()
	if len(sends) != 1 || sends[0][1] != "s1" || !strings.HasPrefix(sends[0][2], "(resent from the cockpit: worker's message 22 of ") ||
		!strings.HasSuffix(sends[0][2], ") synthetic vanished") {
		t.Fatalf("%q", sends)
	}
	if n := f.sqlite("SELECT COUNT(*) FROM send_log WHERE target = 's1'"); n != "1\n" {
		t.Fatalf("resend skipped pacing: %q", n)
	}
	if got := f.sqlite("SELECT state || ' ' || resolution FROM delivery_log WHERE id = " + id(lost.ID)); got != "resolved resent\n" {
		t.Fatalf("%q", got)
	}
	if r = f.zen(nil, "delivery", "resend", id(lost.ID)); r.code != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestDeliveryScanWithoutStateDB(t *testing.T) {
	f := newFake(t)
	r := f.zen(nil, "delivery", "scan")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "unavailable: no agent-manager state.db at ") || strings.Count(r.stdout, "\n") != 1 {
		t.Fatalf("%+v", r)
	}
	if got := f.sqlite("SELECT COUNT(*) FROM delivery_log"); got != "0\n" {
		t.Fatalf("%q", got)
	}
}
