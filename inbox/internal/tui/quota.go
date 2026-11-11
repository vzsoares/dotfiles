package tui

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

// QuotaStaleAfter is when the statusline's quota file counts as stale.
const QuotaStaleAfter = 15 * time.Minute

var (
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	quotaStyle = lipgloss.NewStyle()
)

// window is one rate-limit window as the clawd-status statusline writes it;
// nil fields were missing from Claude Code's input.
type window struct {
	Used     *float64 `json:"used_percentage"`
	ResetsAt *int64   `json:"resets_at"`
}

// quota is ~/.cache/zen/claude-quota.json; ok is false when it is missing or
// unreadable.
type quota struct {
	FiveHour  window `json:"five_hour"`
	SevenDay  window `json:"seven_day"`
	UpdatedAt int64  `json:"updated_at"`
	ok        bool
}

func quotaPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "zen", "claude-quota.json")
}

func readQuota(path string) quota {
	var q quota
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &q) != nil {
		return quota{}
	}
	q.ok = true
	return q
}

// quotaLevel styles a used percentage: error from 95, warning from 80.
func quotaLevel(pct float64) lipgloss.Style {
	switch {
	case pct >= 95:
		return failStyle
	case pct >= 80:
		return warnStyle
	}
	return quotaStyle
}

type quotaPart struct{ full, short string }

// part is "5h 42% · resets 15:20"; reset formats the reset time.
func (w window) part(name string, loc *time.Location, reset func(time.Time) string) (quotaPart, float64) {
	if w.Used == nil {
		return quotaPart{name + " ?", name + " ?"}, 0
	}
	pct := math.Floor(*w.Used)
	short := name + " " + strconv.Itoa(int(pct)) + "%"
	full := short
	if w.ResetsAt != nil && *w.ResetsAt > 0 {
		full += " · resets " + reset(time.Unix(*w.ResetsAt, 0).In(loc))
	}
	return quotaPart{full, short}, pct
}

// quotaText is the header's quota segment no wider than width, or "" when
// even the short form does not fit.
func (q quota) text(now time.Time, loc *time.Location, width int) string {
	if !q.ok {
		if width < len("quota n/a") {
			return ""
		}
		return readStyle.Render("quota n/a")
	}
	five, p5 := q.FiveHour.part("5h", loc, func(t time.Time) string { return t.Format("15:04") })
	week, p7 := q.SevenDay.part("week", loc, func(t time.Time) string { return t.Format("Mon") })
	updated := time.Unix(q.UpdatedAt, 0)
	stale := now.Sub(updated) > QuotaStaleAfter
	age := ""
	if stale {
		age = "  · " + view.Age(inbox.Stamp(updated), now) + " old"
	}
	paint := func(s string, pct float64) string {
		if stale {
			return readStyle.Render(s)
		}
		return quotaLevel(pct).Render(s)
	}
	for _, form := range [][2]string{{five.full, week.full}, {five.short, week.short}} {
		plain := form[0] + "  ·  " + form[1] + age
		if lipgloss.Width(plain) <= width {
			return paint(form[0], p5) + sepStyle.Render("  ·  ") + paint(form[1], p7) + readStyle.Render(age)
		}
	}
	return ""
}
