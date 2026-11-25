package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/am/amtest"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

var deliverySessions = []amtest.Session{
	{ID: "s1", Name: "spec-s1", Cwd: "/w/s1", Agent: "a-s1"},
	{ID: "s2", Name: "spec-s2", Status: "waiting", Agent: "a-s2"},
	{ID: "c1", Name: "chief", Cwd: "/w/chief", Agent: "a-chief"},
}

// deliveryWorld writes a state.db holding rows, and a transcript for s1
// and chief holding the delivered bodies.
func (d *drv) deliveryWorld(rows []amtest.Row, delivered ...string) {
	d.t.Helper()
	var turns []string
	for _, b := range delivered {
		turns = append(turns, amtest.Delivered("worker", b))
	}
	at := inbox.Now()
	amtest.Transcript(d.t, d.projects, "-w-s1", "a-s1", at, append([]string{"unrelated"}, turns...)...)
	amtest.Transcript(d.t, d.projects, "-w-chief", "a-chief", at, append([]string{"unrelated"}, turns...)...)
	amtest.State(d.t, d.state, deliverySessions, rows)
}

func heldRow(id int64, body string) amtest.Row {
	sent := inbox.Now().Add(-12 * time.Minute)
	return amtest.Row{ID: id, To: "s2", From: "w9", FromName: "worker", Body: body, Sent: sent}
}

func droppedRow(id int64, to, body string) amtest.Row {
	sent := inbox.Now().Add(-20 * time.Minute)
	return amtest.Row{ID: id, To: to, From: "w9", FromName: "worker", Body: body, Sent: sent, Delivered: sent, Dropped: sent}
}

func (d *drv) rowStyle(value string) (listRow, bool) {
	for _, r := range d.m.listRows() {
		if r.value == value {
			return r, true
		}
	}
	return listRow{}, false
}

func TestDeliveryRowGoldens(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	cases := []struct {
		name  string
		rows  []amtest.Row
		seen  []string
		label string
		red   bool
	}{
		{"cockpit-delivery-zero-92x29", []amtest.Row{droppedRow(1, "s1", "synthetic arrived")}, []string{"synthetic arrived"}, "", false},
		{"cockpit-delivery-open-92x29", []amtest.Row{heldRow(2, "synthetic waits")}, nil, "Delivery · 1 open", false},
		{"cockpit-delivery-lost-92x29", []amtest.Row{heldRow(2, "synthetic waits"), droppedRow(3, "s1", "synthetic vanished")}, nil,
			"Delivery · 2 open · 1 lost", true},
	}
	for _, c := range cases {
		d := newDrvWith(t, 92, 29, func(d *drv) { d.deliveryWorld(c.rows, c.seen...) })
		d.ask("7", "s1", "Q?", "a", "b")
		d.poll()
		d.fits()
		v := d.view()
		r, ok := d.rowStyle("dl:")
		switch {
		case c.label == "" && (ok || strings.Contains(v, "Delivery ·")):
			t.Errorf("%s: a Delivery row with nothing open:\n%s", c.name, v)
		case c.label != "" && (!ok || r.label != c.label):
			t.Errorf("%s: row %+v, want %q:\n%s", c.name, r, c.label, v)
		case ok && (r.style == &failStyle) != c.red:
			t.Errorf("%s: red %v, want %v", c.name, r.style == &failStyle, c.red)
		}
		golden(t, c.name, v)
	}
}

func TestDeliveryScreenResendAndDismiss(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrvWith(t, 92, 29, func(d *drv) {
		d.deliveryWorld([]amtest.Row{heldRow(2, "synthetic waits\nsecond line"), droppedRow(3, "s1", "synthetic vanished"),
			droppedRow(4, "s1", "synthetic arrived")}, "synthetic arrived")
	})
	d.poll()
	d.cursorTo("Delivery · 2 open")
	d.rawKeys("enter")
	if d.m.scr != scDelivery {
		t.Fatalf("enter did not open the Delivery screen:\n%s", d.view())
	}
	d.fits()
	golden(t, "delivery-screen-92x29", d.view())
	d.has("Delivery · 2 open · 1 false drop this week", "lost · worker → spec-s1 · 20m", "held · worker → spec-s2 · 12m · a dialog on screen")

	d.rawKeys("a")
	d.has("false drop · worker → spec-s1", "auto")
	d.rawKeys("a")

	// The lost one is newest, so it is first.
	d.rawKeys("enter")
	if d.m.scr != scDeliveryEntry {
		t.Fatalf("enter did not open the entry:\n%s", d.view())
	}
	golden(t, "delivery-entry-92x29", d.view())
	d.has("synthetic vanished", "am_inbox 3")

	d.am.sessions = am.Sessions{sess("s1"), sess("s2")}
	d.rawKeys("R")
	if len(d.am.sends) != 1 || d.am.sends[0][0] != "s1" ||
		!strings.HasPrefix(d.am.sends[0][1], "(resent from the cockpit: worker's message 3 of ") ||
		!strings.HasSuffix(d.am.sends[0][1], ") synthetic vanished") {
		t.Fatalf("%q", d.am.sends)
	}
	if d.m.scr != scDelivery || !strings.Contains(d.view(), "» resent entry") {
		t.Fatalf("R did not go back with a flash:\n%s", d.view())
	}
	d.has("Delivery · 1 open")

	d.rawKeys("x")
	d.has("Delivery · 0 open", "Nothing open")
	es, err := d.ib.Entries(true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range es {
		got[e.SourceID] = e.State + "/" + e.Resolution
	}
	if got["2"] != "dismissed/dismissed" || got["3"] != "resolved/resent" || got["4"] != "resolved/auto" {
		t.Fatalf("%+v", got)
	}
	d.rawKeys("esc")
	if _, ok := d.rowStyle("dl:"); ok || d.m.scr != scList {
		t.Fatalf("Delivery row left with nothing open:\n%s", d.view())
	}
}

func TestChiefStatusTrustsTheTranscriptOverDropped(t *testing.T) {
	for _, c := range []struct {
		name, want string
		seen       []string
	}{
		{"false drop", "✉ chief · message 42 delivered", []string{"ping"}},
		{"lost", "✉ chief · message 42 lost · R in Delivery to resend", nil},
	} {
		d := newDrvWith(t, 92, 29, func(d *drv) {
			d.deliveryWorld([]amtest.Row{droppedRow(42, "c1", "ping")}, c.seen...)
		})
		d.am.sessions = am.Sessions{sess("s1"), {"id": "c1", "name": "chief", "status": "idle", "running": true}}
		d.am.chiefStatus = "dropped"
		d.keys("c", "ping", "ctrl+d")
		d.poll()
		if got := d.head(); got != "cockpit · "+c.want {
			t.Errorf("%s: header %q, want %q", c.name, got, c.want)
		}
		chats, err := d.ib.PendingChats(nil)
		if err != nil || len(chats) != 1 {
			t.Fatalf("%+v %v", chats, err)
		}
		if want := map[string]string{"false drop": "delivered", "lost": inbox.StatusLost}[c.name]; chats[0].Status != want {
			t.Errorf("%s: chat status %q, want %q", c.name, chats[0].Status, want)
		}
	}
}

func TestChiefStatusDropWithoutStateDBIsLoggedUnverified(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.am.sessions = am.Sessions{sess("s1"), {"id": "c1", "name": "chief", "status": "idle", "running": true}}
	d.am.chiefStatus = "dropped"
	d.keys("c", "ping", "ctrl+d")
	d.poll()
	if got := d.head(); got != "cockpit · ✉ chief · message 42 dropped" {
		t.Fatalf("header %q", got)
	}
	es, err := d.ib.Entries(false)
	if err != nil || len(es) != 1 || es[0].Source != inbox.SourceStatus || es[0].Kind != inbox.KindUnverified || es[0].Body != "ping" {
		t.Fatalf("%+v %v", es, err)
	}
}
