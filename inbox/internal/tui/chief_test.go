package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func (d *drv) newline() { d.send(tea.KeyMsg{Type: tea.KeyCtrlJ}) }

func TestChiefComposeSendsAndTracksStatus(t *testing.T) {
	d := newDrv(t, 92, 30)
	d.am.chiefStatus = "queued"
	d.keys("c")
	if d.m.scr != scChief {
		t.Fatalf("c did not open the compose box:\n%s", d.view())
	}
	d.keys("first line")
	d.newline()
	d.keys("second line")
	d.fits()
	golden(t, "chief-compose-92x30", d.view())
	d.keys("enter")
	if want := []string{"first line\nsecond line"}; len(d.am.chiefs) != 1 || d.am.chiefs[0] != want[0] {
		t.Fatalf("sent %q", d.am.chiefs)
	}
	if d.m.scr != scList || !d.m.flashOK {
		t.Fatalf("not back on the list:\n%s", d.view())
	}
	if !strings.Contains(d.view(), "✉ chief · message 42 queued") {
		t.Fatalf("no queued status:\n%s", d.view())
	}
	golden(t, "chief-queued-92x30", d.view())
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

func TestChiefStatusUnavailableStopsPolling(t *testing.T) {
	d := newDrv(t, 92, 30)
	d.am.chiefStatus = ""
	d.keys("c", "hi", "enter")
	if !strings.Contains(d.view(), "✉ chief · message 42 sent") {
		t.Fatalf("%s", d.view())
	}
	if !d.m.chief.final() {
		t.Fatal("kept polling a status agent-manager cannot give")
	}
}

func TestChiefEscKeepsTheDraftAndEmptyDoesNotSend(t *testing.T) {
	d := newDrv(t, 92, 30)
	d.keys("c", "enter")
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
	d := newDrv(t, 92, 30)
	d.am.chiefErr = "no live session named 'chief'"
	d.keys("c", "ping")
	d.keys("enter")
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
