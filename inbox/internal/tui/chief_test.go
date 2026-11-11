package tui

import (
	"strings"
	"testing"
)

func (d *drv) newline() { d.keys("enter") }

func TestChiefComposeSendsAndTracksStatus(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.am.chiefStatus = "queued"
	d.keys("c")
	if d.m.scr != scChief {
		t.Fatalf("c did not open the compose box:\n%s", d.view())
	}
	d.keys("first line")
	d.newline()
	d.keys("second line")
	d.fits()
	golden(t, "chief-compose-92x29", d.view())
	d.keys("ctrl+d")
	if want := []string{"first line\nsecond line"}; len(d.am.chiefs) != 1 || d.am.chiefs[0] != want[0] {
		t.Fatalf("sent %q", d.am.chiefs)
	}
	if d.m.scr != scList || !d.m.flashOK {
		t.Fatalf("not back on the list:\n%s", d.view())
	}
	if !strings.Contains(d.view(), "✉ chief · message 42 queued") {
		t.Fatalf("no queued status:\n%s", d.view())
	}
	golden(t, "chief-queued-92x29", d.view())
	d.am.chiefStatus = "delivered"
	d.poll()
	if !strings.Contains(d.view(), "✉ chief · message 42 delivered") {
		t.Fatalf("no delivered status:\n%s", d.view())
	}
	d.keys("c")
	if d.m.chiefTA.Value() != "" {
		t.Fatalf("sent text came back: %q", d.m.chiefTA.Value())
	}
}

func TestChiefStatusUnavailableStopsPollingAfterThreeMisses(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.am.chiefStatus = ""
	d.keys("c", "hi", "ctrl+d")
	for range 2 {
		if d.m.chief.final() || !strings.Contains(d.view(), "message 42 queued") {
			t.Fatalf("gave up after a miss:\n%s", d.view())
		}
		d.poll()
	}
	if !strings.Contains(d.view(), "✉ chief · message 42 sent") || !d.m.chief.final() {
		t.Fatalf("%s", d.view())
	}
}

func TestChiefEscKeepsTheDraftAndEmptyDoesNotSend(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.keys("c", "ctrl+d")
	if d.m.scr != scChief || len(d.am.chiefs) != 0 {
		t.Fatal("an empty message sent or left the box")
	}
	d.keys("half a thou")
	d.keys("esc")
	if d.m.scr != scList || len(d.am.chiefs) != 0 {
		t.Fatal("esc sent or stayed")
	}
	d.keys("c")
	if got := d.m.chiefTA.Value(); got != "half a thou" {
		t.Fatalf("draft lost: %q", got)
	}
}

func TestChiefFailureKeepsTheBoxAndDraft(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.am.chiefErr = "no live session named 'chief'"
	d.keys("c", "ping")
	d.keys("ctrl+d")
	if d.m.scr != scChief || d.m.chiefTA.Value() != "ping" {
		t.Fatalf("lost the box or the draft:\n%s", d.view())
	}
	if !strings.Contains(d.text(), "no live session named 'chief'") {
		t.Fatalf("error not shown:\n%s", d.view())
	}
	if d.m.chief.id != "" {
		t.Fatal("tracked a message that never sent")
	}
	d.fits()
}

func TestChiefEnterAddsALineAndCtrlDSends(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.keys("c", "one", "enter", "two", "enter")
	if len(d.am.chiefs) != 0 || d.m.scr != scChief {
		t.Fatalf("enter sent or left the box: %q", d.am.chiefs)
	}
	if got := d.m.chiefTA.Value(); got != "one\ntwo\n" {
		t.Fatalf("enter did not add lines: %q", got)
	}
	if !strings.Contains(d.last(), "ctrl+d send") {
		t.Fatalf("footer %q", d.last())
	}
	d.keys("ctrl+d")
	if len(d.am.chiefs) != 1 || d.am.chiefs[0] != "one\ntwo\n" {
		t.Fatalf("ctrl+d sent %q", d.am.chiefs)
	}
}
