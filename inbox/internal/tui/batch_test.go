package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

// submit stages a pick for each id and sends them the way the review does.
func (d *drv) submit(ids ...string) {
	d.t.Helper()
	d.m.sid, d.m.sname = "s1", "spec-s1"
	d.m.p = &pass{staged: map[string]view.Staged{}}
	for _, id := range ids {
		d.m.p.staged[id] = view.Staged{Kind: "pick", Option: "a", Rev: d.get(id).Rev}
	}
	msg, ok := d.m.sendCmd("")().(sentMsg)
	if !ok || len(msg.recorded) != len(ids) {
		d.t.Fatalf("%+v", msg)
	}
}

func (d *drv) askTasks(tasks []string, q func(int) string) []string {
	var ids []string
	for i, task := range tasks {
		ids = append(ids, d.ask(task, "s1", q(i), "a", "b"))
	}
	return ids
}

func TestAnswersSubmittedTogetherLeaveAsOneMessage(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	ids := d.askTasks([]string{"955", "969", "978", "979"}, func(int) string { return "Go?" })
	d.submit(ids...)
	if len(d.am.sends) != 1 {
		t.Fatalf("%d sends: %q", len(d.am.sends), d.am.sends)
	}
	for _, id := range ids {
		if !strings.Contains(d.am.sends[0][1], id) || d.get(id).Delivery != "sent" {
			t.Fatalf("%s: %+v in %q", id, d.get(id), d.am.sends[0][1])
		}
	}
}

func TestAnswersOverTheSizeBudgetSplitIntoTheFewestMessages(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	ids := d.askTasks([]string{"1", "2", "3", "4"}, func(i int) string { return strings.Repeat(string(rune('A'+i)), 3000) })
	d.submit(ids...)
	if len(d.am.sends) != 2 {
		t.Fatalf("%d sends", len(d.am.sends))
	}
	for i, s := range d.am.sends {
		if strings.Contains(s[1], "truncated") || strings.Count(s[1], "\n") != 2 {
			t.Fatalf("send %d clipped or uneven: %.200q", i, s[1])
		}
	}
	for _, id := range ids {
		if d.get(id).Delivery != "sent" {
			t.Fatalf("%+v", d.get(id))
		}
	}
}

func TestTransientRefusalOfABatchRetriesAllTogether(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	ids := d.askTasks([]string{"955", "969", "978", "979"}, func(int) string { return "Go?" })
	d.am.fail = "too many messages to this session in the last minute"
	d.submit(ids...)
	if len(d.am.sends) != 1 {
		t.Fatalf("%d sends: %q", len(d.am.sends), d.am.sends)
	}
	for _, id := range ids {
		if dec := d.get(id); dec.Delivery != "waiting" || !strings.Contains(d.am.sends[0][1], id) {
			t.Fatalf("%s: %+v", id, dec)
		}
	}
	d.am.fail = ""
	later(t, time.Minute)
	if _, err := d.ib.Pass(); err != nil {
		t.Fatal(err)
	}
	if len(d.am.sends) != 2 {
		t.Fatalf("%d sends", len(d.am.sends))
	}
	for _, id := range ids {
		if dec := d.get(id); dec.Delivery != "sent" || !strings.Contains(d.am.sends[1][1], id) {
			t.Fatalf("%s: %+v", id, dec)
		}
	}
	if _, err := d.ib.Pass(); err != nil || len(d.am.sends) != 2 {
		t.Fatalf("resent: %d sends %v", len(d.am.sends), err)
	}
}
