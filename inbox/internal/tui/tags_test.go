package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

func TestTypeOfNamesTheKindOfAsk(t *testing.T) {
	cases := []struct {
		d    inbox.Decision
		want string
	}{
		{inbox.Decision{Prod: &inbox.ProdAction{}}, "approval"},
		{inbox.Decision{Kind: inbox.KindManual}, "human action"},
		{inbox.Decision{Gate: true}, "gate"},
		{inbox.Decision{Kind: inbox.KindAgentRuns}, "decision"},
		{inbox.Decision{}, "decision"},
	}
	for _, c := range cases {
		if got := typeOf(c.d); got != c.want {
			t.Errorf("%+v: type %q, want %q", c.d, got, c.want)
		}
	}
}

func TestLeadWithoutProjectIsTheTypeAlone(t *testing.T) {
	for _, typ := range []string{typeNote, typeDecision, typeHuman, typeApproval, typeGate} {
		if got := lead("", typ); got != typ || strings.Contains(got, "●") {
			t.Errorf("%q: %q", typ, got)
		}
		if got := lead("Fakeproj", typ); got != "Fakeproj ● "+typ {
			t.Errorf("%q: %q", typ, got)
		}
	}
}

func TestTypeColoursAreDistinctAndKeepTheirHueAcrossModes(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev); lipgloss.SetHasDarkBackground(true) })
	lipgloss.SetColorProfile(termenv.ANSI256)
	var out []string
	for _, dark := range []bool{true, false} {
		lipgloss.SetHasDarkBackground(dark)
		mode := map[bool]string{true: "dark", false: "light"}[dark]
		seen := map[string]string{}
		for _, typ := range []string{typeNote, typeDecision, typeHuman, typeApproval, typeGate} {
			l := lead("", typ)
			if !strings.Contains(l, "\x1b[") {
				t.Fatalf("%s %s: no colour in %q", mode, typ, l)
			}
			if other, dup := seen[l]; dup {
				t.Fatalf("%s and %s render alike", typ, other)
			}
			seen[l] = typ
			out = append(out, mode+" "+typ+": "+esc(l))
		}
		for _, name := range []string{"Fakeproj", "Otherproj", "Third", "fakeproj"} {
			if lead(name, typeNote) != lead(name, typeNote) {
				t.Fatalf("%s changes between calls", name)
			}
			out = append(out, mode+" "+name+": "+esc(lead(name, typeNote)))
		}
	}
	golden(t, "lead-colours-dark-light", strings.Join(out, "\n"))
}

func TestWalkPinsTheLeadOverEachCase(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"manual", "human action Run it yourself, then pick Done and type y."},
		{"agent", "decision The agent runs this after you answer. Don't run it yourself."},
		{"prod", "approval Approve and the agent runs it."},
		{"plain", "decision"},
	} {
		d := newDrvWith(t, 92, 29, userPanel)
		switch c.name {
		case "manual":
			d.askManual(manualBody)
		case "agent":
			d.askBody()
		case "prod":
			d.askProd("s1")
		default:
			d.ask("7", "s2", "Which way?", "left", "right")
		}
		d.openWalk()
		v := d.text()
		if !strings.Contains(v, c.want) {
			t.Fatalf("%s: %q missing:\n%s", c.name, c.want, v)
		}
		for _, old := range []string{"YOU RUN THIS", "AGENT RUNS THIS", "PROD "} {
			if strings.Contains(v, old) {
				t.Fatalf("%s: old tag %q on screen:\n%s", c.name, old, v)
			}
		}
	}
}
