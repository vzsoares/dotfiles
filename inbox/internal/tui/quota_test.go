package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// writeQuota writes the statusline's quota file.
func (d *drv) writeQuota(body string) {
	d.t.Helper()
	if err := os.WriteFile(d.m.quotaFile, []byte(body), 0o644); err != nil {
		d.t.Fatal(err)
	}
}

// pinned: Tue 2026-11-10 12:00 UTC; 15:20 the same day, Fri the 13th.
const quotaFresh = `{"five_hour":{"used_percentage":42.7,"resets_at":1794324000},` +
	`"seven_day":{"used_percentage":63,"resets_at":1794560400},"updated_at":1794311940}`

func TestReadQuota(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "q.json")
	if q := readQuota(path); q.ok {
		t.Fatal("missing file read as ok")
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if q := readQuota(path); q.ok {
		t.Fatal("corrupt file read as ok")
	}
	if err := os.WriteFile(path, []byte(`{"five_hour":{"used_percentage":7,"resets_at":null},"updated_at":5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	q := readQuota(path)
	if !q.ok || *q.FiveHour.Used != 7 || q.FiveHour.ResetsAt != nil || q.SevenDay.Used != nil || q.UpdatedAt != 5 {
		t.Fatalf("%+v", q)
	}
	now := time.Unix(10, 0)
	if got := q.text(now, time.UTC, 92); got != "5h 7%  ·  week ?" {
		t.Fatalf("partial file renders %q", got)
	}
}

func TestQuotaLevels(t *testing.T) {
	for _, c := range []struct {
		pct  float64
		want lipgloss.Style
	}{{0, quotaStyle}, {79.9, quotaStyle}, {80, warnStyle}, {94.9, warnStyle}, {95, failStyle}, {100, failStyle}} {
		if got := quotaLevel(c.pct); got.GetForeground() != c.want.GetForeground() {
			t.Errorf("%v%% → %v, want %v", c.pct, got.GetForeground(), c.want.GetForeground())
		}
	}
}

func TestQuotaHeaderGoldens(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	stale := strings.Replace(quotaFresh, "1794311940", "1794309600", 1)
	cases := []struct {
		name, body, want string
	}{
		{"quota-fresh-92x29", quotaFresh, "5h 42% · resets 15:20  ·  week 63% · resets Fri"},
		{"quota-stale-92x29", stale, "5h 42% · resets 15:20  ·  week 63% · resets Fri  · 40m old"},
		{"quota-missing-92x29", "", "quota n/a"},
		{"quota-reset-92x29", strings.Replace(quotaFresh, "1794324000", "1794311000", 1), "5h reset  ·  week 63% · resets Fri"},
		{"quota-unseen-92x29", `{"five_hour":{"used_percentage":null,"resets_at":null},` +
			`"seven_day":{"used_percentage":63,"resets_at":1794560400},"updated_at":1794311940}`, "5h ?  ·  week 63% · resets Fri"},
	}
	for _, c := range cases {
		d := newDrv(t, 92, 29)
		if c.body != "" {
			d.writeQuota(c.body)
		}
		d.ask("7", "s1", "Q?", "a", "b")
		d.poll()
		d.fits()
		if head := d.line(0); !strings.HasPrefix(head, "cockpit · 1 open") || !strings.HasSuffix(head, c.want) {
			t.Errorf("%s header %q, want …%q", c.name, head, c.want)
		}
		golden(t, c.name, d.view())
	}
}

func TestQuotaShrinksThenDropsOnANarrowHeader(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.writeQuota(quotaFresh)
	d.ask("7", "s1", "Q?", "a", "b")
	d.addNote("", "one", "")
	d.poll()
	d.send(tea.WindowSizeMsg{Width: 70, Height: 29})
	if head := d.line(0); !strings.HasSuffix(head, "  5h 42%  ·  week 63%") || lipgloss.Width(head) != 70 {
		t.Fatalf("long status did not get the short quota: %q", head)
	}
	d.send(tea.WindowSizeMsg{Width: 40, Height: 15})
	if head := d.line(0); strings.Contains(head, "5h") || head != "cockpit · 1 open · ✉ 1 unread" {
		t.Fatalf("narrow header kept the quota: %q", head)
	}
}
