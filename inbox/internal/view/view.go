// Package view turns decisions into text lines: the list rows, status lines,
// context blocks and question screens shared by `list` and the panel.
package view

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/md"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

const (
	MinWidth = 20
	RecMark  = "  ← rec"
	More     = "… (View full)"
)

// Wrap word-wraps at width with a hanging indent the width of first.
func Wrap(text string, width int, first string) []string {
	return WrapRest(text, width, first, strings.Repeat(" ", pyfmt.Len(first)))
}

// WrapRest word-wraps with an explicit indent for every line after the first;
// words longer than a line are split.
func WrapRest(text string, width int, first, rest string) []string {
	width = max(width, MinWidth)
	var out []string
	paras := pyfmt.SplitLines(text)
	if len(paras) == 0 {
		paras = []string{""}
	}
	for _, para := range paras {
		lead := first
		if len(out) > 0 {
			lead = rest
		}
		lines := pyfmt.TextWrap(para, width, lead, rest)
		if len(lines) == 0 {
			lines = []string{pyfmt.RStrip(lead)}
		}
		out = append(out, lines...)
	}
	return out
}

// Age is the time since stamp as 5s, 3m, 2h or 4d.
func Age(stamp string, at time.Time) string {
	then, ok := inbox.ParseStamp(stamp)
	if !ok {
		return "?"
	}
	secs := int(at.Sub(then).Seconds())
	for _, u := range []struct {
		unit string
		size int
	}{{"d", 86400}, {"h", 3600}, {"m", 60}} {
		if secs >= u.size {
			return fmt.Sprintf("%d%s", secs/u.size, u.unit)
		}
	}
	return fmt.Sprintf("%ds", max(secs, 0))
}

// Brief is one task's context block.
type Brief struct {
	Task, Title, Text, AskedBy string
}

func (b Brief) Head() string { return pyfmt.RStrip("#" + b.Task + " " + b.Title) }

func AskedBy(name string, s am.Session, oldest string, at time.Time) string {
	var meta []string
	if s != nil {
		if pyfmt.Truthy(s["group"]) {
			meta = append(meta, pyfmt.Str(s["group"]))
		}
		dir := ""
		if pyfmt.Truthy(s["directory"]) {
			dir = pyfmt.Str(s["directory"])
		}
		if n := am.DirName(dir); n != "" {
			meta = append(meta, n)
		}
	}
	who := name
	if len(meta) > 0 {
		who = fmt.Sprintf("%s (%s)", name, strings.Join(meta, ", "))
	}
	return fmt.Sprintf("asked by %s · %s · %s", who, am.Status(s), Age(oldest, at))
}

func sorted(ds []inbox.Decision) []inbox.Decision {
	out := append([]inbox.Decision(nil), ds...)
	inbox.SortByID(out)
	return out
}

// BriefsFor is one context block per task, in ID order.
func BriefsFor(ds []inbox.Decision, sessions am.Sessions, at time.Time) []Brief {
	var out []Brief
	seen := map[string]bool{}
	for _, d := range sorted(ds) {
		if seen[d.Task] {
			continue
		}
		seen[d.Task] = true
		oldest := ""
		first := true
		for _, x := range ds {
			if x.Task == d.Task && (first || x.CreatedAt < oldest) {
				oldest, first = x.CreatedAt, false
			}
		}
		line := AskedBy(d.SessionName, am.Find(sessions, d.SessionID), oldest, at)
		out = append(out, Brief{d.Task, d.Title, d.Brief, line})
	}
	return out
}

func ContextBlock(b Brief, width int) []string {
	var text []string
	if !pyfmt.Blank(b.Text) {
		text = Wrap(b.Text, width, "  ")
	}
	out := WrapRest(b.Head(), width, "", "  ")
	out = append(out, text...)
	return append(out, Wrap(b.AskedBy, width, "")...)
}

func QuestionTags(d inbox.Decision) string {
	tags := ""
	if d.Gate {
		tags += " gate"
	}
	if d.Amended {
		tags += " (amended)"
	}
	return tags
}

func QuestionLines(d inbox.Decision, width int) []string {
	return WrapRest(d.ID+QuestionTags(d)+"  "+d.Question, width, "", "  ")
}

func ContextLines(d inbox.Decision, width int) []string {
	if pyfmt.Blank(d.Context) {
		return nil
	}
	return Wrap(d.Context, width, "│ ")
}

func OptionLines(d inbox.Decision, width int) []string {
	var out []string
	for _, o := range d.Options {
		text := o.Text
		if o.Letter == d.Rec {
			text += RecMark
		}
		out = append(out, Wrap(text, width, o.Letter+") ")...)
	}
	return out
}

func DecisionLines(d inbox.Decision, width int) []string {
	out := QuestionLines(d, width)
	out = append(out, ContextLines(d, width)...)
	return append(out, OptionLines(d, width)...)
}

// ClipLines keeps rows lines, ending the last kept one with "… (View full)".
// Lines may be painted: they are measured and cut in cells, escapes aside.
func ClipLines(lines []string, rows, width int) []string {
	if len(lines) <= rows {
		return lines
	}
	if rows <= 0 {
		return nil
	}
	room := max(width, MinWidth) - lipgloss.Width(More) - 1
	out := append([]string(nil), lines[:rows-1]...)
	last := ansi.Truncate(lines[rows-1], room, "")
	last = ansi.Truncate(last, lipgloss.Width(strings.TrimRight(ansi.Strip(last), " ")), "")
	if strings.Contains(last, "\x1b") {
		last += "\x1b[0m"
	}
	return append(out, last+" "+More)
}

// SessionLines is a session's context blocks, then its questions in full or
// one line each when compact.
func SessionLines(ds []inbox.Decision, sessions am.Sessions, width int, at time.Time, compact bool) []string {
	var lines []string
	for _, b := range BriefsFor(ds, sessions, at) {
		lines = append(lines, ContextBlock(b, width)...)
	}
	for _, d := range sorted(ds) {
		if compact {
			tag := ""
			if d.Gate {
				tag = " gate"
			}
			lines = append(lines, pyfmt.Clip(d.ID+tag+"  "+d.Question, max(width, MinWidth)))
		} else {
			lines = append(lines, "")
			lines = append(lines, DecisionLines(d, width)...)
		}
	}
	return lines
}

// -------------------------------------------------------------------- panel --

// Painter renders one field's markdown at k.Width for the panel.
type Painter func(k md.Key, text string) []string

// painted renders text at width less the prefix width, then prefixes the
// first line with first and the others with rest.
func painted(p Painter, k md.Key, text string, width int, first, rest string) []string {
	width = max(width, MinWidth)
	k.Width = max(width-max(lipgloss.Width(first), lipgloss.Width(rest)), 1)
	lines := p(k, text)
	if len(lines) == 0 {
		return []string{pyfmt.RStrip(first)}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		lead := rest
		if i == 0 {
			lead = first
		}
		if l == "" {
			lead = pyfmt.RStrip(lead)
		}
		out[i] = lead + l
	}
	return out
}

func PanelQuestionLines(d inbox.Decision, width int, p Painter) []string {
	return painted(p, md.Key{ID: d.ID, Rev: d.Rev, Field: "question"}, d.Question, width,
		d.ID+QuestionTags(d)+"  ", "  ")
}

func PanelContextLines(d inbox.Decision, width int, p Painter) []string {
	if pyfmt.Blank(d.Context) {
		return nil
	}
	return painted(p, md.Key{ID: d.ID, Rev: d.Rev, Field: "context"}, d.Context, width, "│ ", "│ ")
}

// PanelOptionLines puts RecMark after the recommended option's last line,
// or on a line of its own when it does not fit there.
func PanelOptionLines(d inbox.Decision, width int, p Painter) []string {
	var out []string
	for _, o := range d.Options {
		first := o.Letter + ") "
		rest := strings.Repeat(" ", lipgloss.Width(first))
		lines := painted(p, md.Key{ID: d.ID, Rev: d.Rev, Field: "option " + o.Letter}, o.Text, width, first, rest)
		if o.Letter == d.Rec {
			last := len(lines) - 1
			if lipgloss.Width(lines[last])+lipgloss.Width(RecMark) <= max(width, MinWidth) {
				lines[last] += RecMark
			} else {
				lines = append(lines, rest+strings.TrimLeft(RecMark, " "))
			}
		}
		out = append(out, lines...)
	}
	return out
}

func PanelDecisionLines(d inbox.Decision, width int, p Painter) []string {
	out := PanelQuestionLines(d, width, p)
	out = append(out, PanelContextLines(d, width, p)...)
	return append(out, PanelOptionLines(d, width, p)...)
}

// PanelBriefText is a brief's text, indented; nil when blank.
func PanelBriefText(b Brief, width int, p Painter) []string {
	if pyfmt.Blank(b.Text) {
		return nil
	}
	return painted(p, md.Key{ID: "#" + b.Task, Field: "brief"}, b.Text, width, "  ", "  ")
}

func PanelContextBlock(b Brief, width int, p Painter) []string {
	out := WrapRest(b.Head(), width, "", "  ")
	out = append(out, PanelBriefText(b, width, p)...)
	return append(out, Wrap(b.AskedBy, width, "")...)
}

// PanelSessionLines is SessionLines in full, with text painted.
func PanelSessionLines(ds []inbox.Decision, sessions am.Sessions, width int, at time.Time, p Painter) []string {
	var lines []string
	for _, b := range BriefsFor(ds, sessions, at) {
		lines = append(lines, PanelContextBlock(b, width, p)...)
	}
	for _, d := range sorted(ds) {
		lines = append(lines, "")
		lines = append(lines, PanelDecisionLines(d, width, p)...)
	}
	return lines
}

// ---------------------------------------------------------------------- list --

// Row is one asking session in the list.
type Row struct {
	SessionID string
	Name      string
	Tasks     []string
	Title     string
	Count     int
	Oldest    string
	Gate      bool
	Status    string
}

// Rows is one row per asking session, oldest open decision first.
func Rows(open []inbox.Decision, sessions am.Sessions) []Row {
	byAge := append([]inbox.Decision(nil), open...)
	sort.SliceStable(byAge, func(i, j int) bool {
		if byAge[i].CreatedAt != byAge[j].CreatedAt {
			return byAge[i].CreatedAt < byAge[j].CreatedAt
		}
		return inbox.CompareID(byAge[i].ID, byAge[j].ID) < 0
	})
	var order []string
	groups := map[string][]inbox.Decision{}
	for _, d := range byAge {
		if _, ok := groups[d.SessionID]; !ok {
			order = append(order, d.SessionID)
		}
		groups[d.SessionID] = append(groups[d.SessionID], d)
	}
	var rows []Row
	for _, sid := range order {
		ds := groups[sid]
		var tasks []string
		seen := map[string]bool{}
		for _, d := range sorted(ds) {
			if !seen[d.Task] {
				seen[d.Task] = true
				tasks = append(tasks, d.Task)
			}
		}
		title := ""
		if len(tasks) == 1 {
			for _, d := range ds {
				if d.Title != "" {
					title = d.Title
					break
				}
			}
		}
		gate := false
		for _, d := range ds {
			gate = gate || d.Gate
		}
		rows = append(rows, Row{sid, ds[0].SessionName, tasks, title, len(ds), ds[0].CreatedAt,
			gate, am.Status(am.Find(sessions, sid))})
	}
	return rows
}

func RowLabel(r Row, at time.Time) string {
	tasks := make([]string, len(r.Tasks))
	for i, t := range r.Tasks {
		tasks[i] = "#" + t
	}
	parts := []string{
		r.Name,
		pyfmt.RStrip(strings.Join(tasks, ", ") + " " + r.Title),
		fmt.Sprintf("%d open", r.Count),
		Age(r.Oldest, at),
		"[" + r.Status + "]",
	}
	if r.Gate {
		parts = append(parts, "gate")
	}
	return strings.Join(parts, " · ")
}

// StatusLines reports held answers still waiting and undelivered batches.
func StatusLines(held, open []inbox.Decision, undelivered []inbox.Batch) []string {
	var lines []string
	var order []string
	heldIDs := map[string][]string{}
	names := map[string]string{}
	for _, d := range held {
		if _, ok := heldIDs[d.SessionID]; !ok {
			order = append(order, d.SessionID)
		}
		heldIDs[d.SessionID] = append(heldIDs[d.SessionID], d.ID)
		names[d.SessionID] = d.SessionName
	}
	for _, sid := range order {
		var waiting []string
		for _, d := range open {
			if d.SessionID == sid {
				waiting = append(waiting, d.ID)
			}
		}
		if len(waiting) > 0 {
			lines = append(lines, fmt.Sprintf("held · %s: %s held until %s", names[sid],
				strings.Join(heldIDs[sid], ", "), strings.Join(waiting, ", ")))
		}
	}
	for _, b := range undelivered {
		first := b.Decisions[0]
		var bids []string
		for _, d := range b.Decisions {
			bids = append(bids, d.ID)
		}
		reason := first.DeliveryError
		if reason == "" {
			reason = first.Delivery
		}
		lines = append(lines, fmt.Sprintf("failed · %s: %s (%s); zen-inbox retry %s",
			first.SessionName, strings.Join(bids, ", "), reason, b.SessionID))
	}
	return lines
}

// Render is `zen-inbox list`'s summary: one row per session, then status.
func Render(open, held []inbox.Decision, undelivered []inbox.Batch, sessions am.Sessions, width int, at time.Time) string {
	var lines []string
	for _, r := range Rows(open, sessions) {
		lines = append(lines, WrapRest(RowLabel(r, at), width, "", "    ")...)
	}
	for _, s := range StatusLines(held, open, undelivered) {
		lines = append(lines, WrapRest(s, width, "", "    ")...)
	}
	if len(lines) == 0 {
		return "inbox empty"
	}
	return strings.Join(lines, "\n")
}

// BellMessages is one message per session with decisions not seen before.
func BellMessages(open []inbox.Decision, seen map[string]bool) []string {
	var order []string
	fresh := map[string][]inbox.Decision{}
	for _, d := range open {
		if seen[d.ID] {
			continue
		}
		if _, ok := fresh[d.SessionID]; !ok {
			order = append(order, d.SessionID)
		}
		fresh[d.SessionID] = append(fresh[d.SessionID], d)
	}
	out := make([]string, 0, len(order))
	for _, sid := range order {
		ds := fresh[sid]
		out = append(out, fmt.Sprintf("%s: %d new decision(s)", ds[0].SessionName, len(ds)))
	}
	return out
}

// Staged is an answer held in memory until Send.
type Staged struct {
	Kind, Option, Text string
	Rev                int64
}

func StagedText(d inbox.Decision, s *Staged) string {
	switch {
	case s == nil:
		return "skipped"
	case s.Kind == "pick":
		return s.Option + ": " + d.OptionText(s.Option)
	}
	return "free text: " + s.Text
}
