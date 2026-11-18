package view

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestThreadLabelNamesTheTaskAndCutsOnlyTheTitle(t *testing.T) {
	h := ThreadHead{Task: "860", Title: "A `long` title\nthat will not fit in the row at all", Open: 1, Unread: 2, Items: 5}
	got := ThreadLabel(h, time.Now(), 60)
	if !strings.HasPrefix(got, "▸ #860 · A long title tha… · 1 open · 2 unread · 5 items") {
		t.Fatalf("label %q", got)
	}
	if w := lipgloss.Width(got); w > 60 {
		t.Fatalf("label is %d wide: %q", w, got)
	}
	if got := ThreadLabel(ThreadHead{Task: "860", Items: 2}, time.Now(), 92); strings.Contains(got, "· ·") || !strings.HasPrefix(got, "▸ #860 · 2 items") {
		t.Fatalf("untitled %q", got)
	}
}
