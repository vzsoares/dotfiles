// Package view turns decisions into text lines: the list rows, status lines,
// context blocks and question screens shared by `list` and the panel.
package view

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
func Age(stamp string, at time.Time) string { return inbox.Age(stamp, at) }

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
	if d.Kind == inbox.KindManual {
		tags += " manual"
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

// BodyLines is the ask's body as written, indented under the question.
func BodyLines(d inbox.Decision, width int) []string {
	if pyfmt.Blank(d.Body) {
		return nil
	}
	return Wrap(strings.TrimRight(d.Body, "\n"), width, "  ")
}

func DecisionLines(d inbox.Decision, width int) []string {
	out := QuestionLines(d, width)
	out = append(out, BodyLines(d, width)...)
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
			lines = append(lines, pyfmt.Clip(d.ID+tag+"  "+md.Plain(d.Question), max(width, MinWidth)))
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

// Painted renders text at width less the prefix width, then prefixes the
// first line with first and the others with rest.
func Painted(p Painter, k md.Key, text string, width int, first, rest string) []string {
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
		if code, ok := strings.CutPrefix(l, md.CodeMark); ok {
			l = code
			if i > 0 {
				lead = strings.Repeat(" ", lipgloss.Width(rest))
			}
		}
		if l == "" {
			lead = pyfmt.RStrip(lead)
		}
		out[i] = lead + l
	}
	return out
}

func PanelQuestionLines(d inbox.Decision, width int, p Painter) []string {
	return Painted(p, md.Key{ID: d.ID, Rev: d.Rev, Field: "question"}, d.Question, width,
		d.ID+QuestionTags(d)+"  ", "  ")
}

// PanelBodyLines is the ask's markdown body, indented under the question.
func PanelBodyLines(d inbox.Decision, width int, p Painter) []string {
	if pyfmt.Blank(d.Body) {
		return nil
	}
	return Painted(p, md.Key{ID: d.ID, Rev: d.Rev, Field: "body"}, d.Body, width, "  ", "  ")
}

// PanelCmdLines is a run ask's command as a code block, so it copies like one.
func PanelCmdLines(d inbox.Decision, width int, p Painter) []string {
	if d.Prod == nil || d.Prod.Cmd == "" {
		return nil
	}
	return Painted(p, md.Key{ID: d.ID, Rev: d.Rev, Field: "cmd"}, md.Fence(d.Prod.Cmd), width, "  ", "  ")
}

// PanelAskLines is the question with what belongs right under it: a run's
// command and the body.
func PanelAskLines(d inbox.Decision, width int, p Painter) []string {
	out := PanelQuestionLines(d, width, p)
	out = append(out, PanelCmdLines(d, width, p)...)
	return append(out, PanelBodyLines(d, width, p)...)
}

func PanelContextLines(d inbox.Decision, width int, p Painter) []string {
	if pyfmt.Blank(d.Context) {
		return nil
	}
	return Painted(p, md.Key{ID: d.ID, Rev: d.Rev, Field: "context"}, d.Context, width, "│ ", "│ ")
}

// PanelOptionLines puts RecMark after the recommended option's last line,
// or on a line of its own when it does not fit there.
func PanelOptionLines(d inbox.Decision, width int, p Painter) []string {
	var out []string
	for _, o := range d.Options {
		first := o.Letter + ") "
		rest := strings.Repeat(" ", lipgloss.Width(first))
		lines := Painted(p, md.Key{ID: d.ID, Rev: d.Rev, Field: "option " + o.Letter}, o.Text, width, first, rest)
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
	out := PanelAskLines(d, width, p)
	out = append(out, PanelContextLines(d, width, p)...)
	return append(out, PanelOptionLines(d, width, p)...)
}

// PanelBriefText is a brief's text, indented; nil when blank.
func PanelBriefText(b Brief, width int, p Painter) []string {
	if pyfmt.Blank(b.Text) {
		return nil
	}
	return Painted(p, md.Key{ID: "#" + b.Task, Field: "brief"}, b.Text, width, "  ", "  ")
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
	// First is the task of the oldest open decision: the row's thread.
	First string
	// Newest is when the session's newest open decision was asked.
	Newest string
	// Drafted is how many open decisions the panel holds a current draft for.
	Drafted int
	// Manual is whether one of its open decisions is a manual-step ask.
	Manual bool
	// AgentRuns is whether one is an --agent-runs ask.
	AgentRuns bool
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
		gate, manual, agent := false, false, false
		for _, d := range ds {
			gate = gate || d.Gate
			manual = manual || d.Kind == inbox.KindManual
			agent = agent || d.Kind == inbox.KindAgentRuns
		}
		rows = append(rows, Row{sid, ds[0].SessionName, tasks, title, len(ds), ds[0].CreatedAt,
			gate, am.Status(am.Find(sessions, sid)), ds[0].Task, ds[len(ds)-1].CreatedAt, 0, manual, agent})
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
		pyfmt.RStrip(strings.Join(tasks, ", ") + " " + md.Plain(r.Title)),
		fmt.Sprintf("%d open", r.Count),
	}
	if r.Drafted > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d drafted", r.Drafted, r.Count))
	}
	parts = append(parts, Age(r.Oldest, at), "["+r.Status+"]")
	if r.Gate {
		parts = append(parts, "gate")
	}
	return strings.Join(parts, " · ")
}

// StatusLines reports held answers still waiting and undelivered batches.
func StatusLines(held, open []inbox.Decision, undelivered []inbox.Batch) []string {
	var lines []string
	for _, h := range HeldLines(held, open) {
		lines = append(lines, "held · "+h)
	}
	for _, b := range undelivered {
		var bids []string
		for _, d := range b.Decisions {
			bids = append(bids, d.ID)
		}
		lines = append(lines, fmt.Sprintf("failed · %s: %s (%s); zen-inbox retry %s",
			b.Decisions[0].SessionName, strings.Join(bids, ", "), FailReason(b), b.SessionID))
	}
	return lines
}

// WaitingLines is one "waiting · name: ids (reason)" per batch kept for a
// session that is down.
func WaitingLines(waiting []inbox.Batch) []string {
	var lines []string
	for _, b := range waiting {
		var bids []string
		for _, d := range b.Decisions {
			bids = append(bids, d.ID)
		}
		lines = append(lines, fmt.Sprintf("waiting · %s: %s (%s)", b.Decisions[0].SessionName,
			strings.Join(bids, ", "), FailReason(b)))
	}
	return lines
}

// HeldWaiting maps each (session, task) whose answers are held to the open
// non-prod asks it waits on; prod asks never hold.
func HeldWaiting(held, open []inbox.Decision) map[[2]string][]string {
	out := map[[2]string][]string{}
	for _, h := range held {
		k := [2]string{h.SessionID, h.Task}
		if _, ok := out[k]; ok {
			continue
		}
		out[k] = nil
		for _, d := range open {
			if d.SessionID == h.SessionID && d.Task == h.Task && d.Prod == nil {
				out[k] = append(out[k], d.ID)
			}
		}
	}
	return out
}

// HeldLines is one "name: ids held until ids" per session and task
// whose answers wait for open asks of that same task.
func HeldLines(held, open []inbox.Decision) []string {
	var lines []string
	var order [][2]string
	heldIDs := map[[2]string][]string{}
	names := map[string]string{}
	for _, d := range held {
		k := [2]string{d.SessionID, d.Task}
		if _, ok := heldIDs[k]; !ok {
			order = append(order, k)
		}
		heldIDs[k] = append(heldIDs[k], d.ID)
		names[d.SessionID] = d.SessionName
	}
	waiting := HeldWaiting(held, open)
	for _, k := range order {
		if w := waiting[k]; len(w) > 0 {
			lines = append(lines, fmt.Sprintf("%s: %s held until %s", names[k[0]],
				strings.Join(heldIDs[k], ", "), strings.Join(w, ", ")))
		}
	}
	return lines
}

// FailReason is why b's delivery failed, else its delivery state.
func FailReason(b inbox.Batch) string {
	if r := b.Decisions[0].DeliveryError; r != "" {
		return r
	}
	return b.Decisions[0].Delivery
}

// Render is `zen-inbox list`'s summary: one row per session, then status.
func Render(open, held []inbox.Decision, undelivered, waiting []inbox.Batch, sessions am.Sessions, width int, at time.Time) string {
	var lines []string
	for _, r := range Rows(open, sessions) {
		lines = append(lines, WrapRest(RowLabel(r, at), width, "", "    ")...)
	}
	for _, s := range append(StatusLines(held, open, undelivered), WaitingLines(waiting)...) {
		lines = append(lines, WrapRest(s, width, "", "    ")...)
	}
	if len(lines) == 0 {
		return "inbox empty"
	}
	return strings.Join(lines, "\n")
}

// Notice is one banner: a decision or note not seen before.
// Push is its ntfy body: counts only, never question or note text.
type Notice struct{ Title, Body, Push string }

// Notices is one banner per open decision and unread note not in seen: first
// the decisions in open order, then the notes. A note takes its task's title
// from an open decision of that task.
func Notices(open []inbox.Decision, notes []inbox.Note, seen map[string]bool) []Notice {
	var out []Notice
	titles := map[string]string{}
	perTask := map[string]int{}
	for _, d := range open {
		perTask[d.Task]++
		if d.Title != "" {
			titles[d.Task] = d.Title
		}
	}
	unread := 0
	for _, n := range notes {
		if n.Unread() {
			unread++
		}
	}
	title := func(task string) string {
		if task == "" {
			return "zen-inbox"
		}
		return "zen-inbox · " + Brief{Task: task, Title: titles[task]}.Head()
	}
	for _, d := range open {
		if !seen[d.ID] {
			body, _, _ := strings.Cut(strings.TrimSpace(md.Plain(d.Question)), "\n")
			push := fmt.Sprintf("%d open", perTask[d.Task])
			if d.Task != "" {
				push = "#" + d.Task + " · " + push
			}
			if d.Prod != nil {
				push += " · PROD"
			}
			out = append(out, Notice{title(d.Task), strings.TrimSpace(body), push})
		}
	}
	for _, n := range notes {
		if n.Unread() && !seen[n.Label()] {
			out = append(out, Notice{title(n.Task), n.Title, fmt.Sprintf("✉ %d unread", unread)})
		}
	}
	return out
}

// Staged is an answer held in memory until Send.
type Staged struct {
	Kind, Option, Text string
	Rev                int64
}

// PanelReviewLines is d's question then " → " and its staged answer, which
// goes under the question unless both fit on one line.
func PanelReviewLines(d inbox.Decision, s *Staged, width int, p Painter) []string {
	out := PanelQuestionLines(d, width, p)
	k := md.Key{ID: d.ID, Rev: d.Rev, Field: "staged"}
	text := StagedText(d, s)
	if a := Painted(p, k, text, width, "", ""); len(out) == 1 && len(a) == 1 &&
		lipgloss.Width(out[0])+3+lipgloss.Width(a[0]) <= max(width, MinWidth) {
		out[0] += " → " + a[0]
		return out
	}
	return append(out, Painted(p, k, text, width, "  → ", "    ")...)
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

// NoteLabel is a note's list row: title, sender, task and age; ↩ marks a sent
// reply. The panel shows unread in bold, not in the text.
func NoteLabel(n inbox.Note, at time.Time) string {
	parts := []string{md.Plain(n.Title), n.SessionName}
	if n.Task != "" {
		parts = append(parts, "#"+n.Task)
	}
	parts = append(parts, Age(n.CreatedAt, at))
	label := strings.Join(parts, " · ")
	if len(n.Replies) > 0 || n.Reply != "" {
		label += " ↩"
	}
	return label
}

// NoteHead is the note screen's first line: n12 · #707 · chief · 5m.
func NoteHead(n inbox.Note, at time.Time) string {
	parts := []string{n.Label()}
	if n.Task != "" {
		parts = append(parts, "#"+n.Task)
	}
	return strings.Join(append(parts, n.SessionName, Age(n.CreatedAt, at)), " · ")
}

// PanelNoteLines is a note's title, painted body and its replies in order,
// each led by its age (and number, when there are several); a latest reply
// kept for a sender that is down says so.
func PanelNoteLines(n inbox.Note, width int, at time.Time, p Painter) []string {
	out := Wrap(n.Title, width, "")
	if !pyfmt.Blank(n.Body) {
		out = append(append(out, ""), Painted(p, md.Key{ID: n.Label(), Field: "note"}, n.Body, width, "", "")...)
	}
	for i, r := range n.Replies {
		lead := "↩ "
		if len(n.Replies) > 1 {
			lead += strconv.Itoa(i+1) + " · "
		}
		if i == len(n.Replies)-1 && n.Delivery == "waiting" {
			lead += "waiting for " + n.SessionName + " · "
		}
		lead += Age(r.RepliedAt, at) + ": "
		key := md.Key{ID: n.Label(), Field: "reply" + strconv.Itoa(i+1)}
		out = append(append(out, ""), Painted(p, key, r.Text, width, lead, "  ")...)
	}
	return out
}

// ThreadNoteLabel is NoteLabel without the task, for a row under its thread.
func ThreadNoteLabel(n inbox.Note, at time.Time) string {
	n.Task = ""
	return NoteLabel(n, at)
}

// fit builds a label that is at most room cells wide by cutting its title, so
// the age and counts after it stay. A label without a title is returned as is.
func fit(build func(title string) string, title string, room int) string {
	full := build(title)
	over := lipgloss.Width(full) - room
	if over <= 0 || title == "" {
		return full
	}
	return build(pyfmt.Clip(title, max(utf8.RuneCountInString(title)-over, 1)))
}

// RowLabelFit is RowLabel cut to room cells through its title.
func RowLabelFit(r Row, at time.Time, room int) string {
	return fit(func(title string) string { r.Title = title; return RowLabel(r, at) }, md.Plain(r.Title), room)
}

// NoteLabelFit is NoteLabel cut to room cells through its title.
func NoteLabelFit(n inbox.Note, at time.Time, room int) string {
	return fit(func(title string) string { n.Title = title; return NoteLabel(n, at) }, md.Plain(n.Title), room)
}

// ThreadHead is a thread's list header: what it holds and how fresh it is.
type ThreadHead struct {
	Task, Title         string
	Expanded            bool
	Open, Unread, Items int
	// Held is how many open asks hold this thread's answered ones back.
	Held   int
	Newest string
}

// ThreadLabel is ▸/▾ #task, the title, the counts and the age of the
// newest item; only the title is cut to fit room.
func ThreadLabel(h ThreadHead, at time.Time, room int) string {
	arrow := "▸ "
	if h.Expanded {
		arrow = "▾ "
	}
	var rest []string
	if h.Open > 0 {
		rest = append(rest, strconv.Itoa(h.Open)+" open")
	}
	if h.Held > 0 {
		rest = append(rest, "held: "+strconv.Itoa(h.Held)+" open")
	}
	if h.Unread > 0 {
		rest = append(rest, strconv.Itoa(h.Unread)+" unread")
	}
	rest = append(rest, strconv.Itoa(h.Items)+" items", Age(h.Newest, at))
	head, tail := arrow+"#"+h.Task, " · "+strings.Join(rest, " · ")
	title := md.Plain(strings.Join(strings.Fields(h.Title), " "))
	if left := room - lipgloss.Width(head) - lipgloss.Width(tail) - 3; title != "" && left > 1 {
		head += " · " + pyfmt.Clip(title, left)
	}
	return head + tail
}

var (
	boldText  = lipgloss.NewStyle().Bold(true)
	faintText = lipgloss.NewStyle().Faint(true)
)

func faint(lines []string) []string {
	for i := range lines {
		lines[i] = faintText.Render(lines[i])
	}
	return lines
}

// DecisionState is a decision's outcome on one line: open, withdrawn, talk,
// the picked option or the free text, with held when it waits to be sent.
func DecisionState(d inbox.Decision) string {
	state := ""
	switch {
	case d.Status == "open":
		return "open"
	case d.Status == "withdrawn":
		return "withdrawn"
	case d.AnswerKind == "talk":
		state = "talk"
	case d.AnswerKind == "pick":
		state = "→ " + d.AnswerOption + ": " + d.OptionText(d.AnswerOption)
		state += inbox.ConfirmNote(d)
	default:
		state = "free text: " + d.AnswerText + inbox.ConfirmNote(d)
	}
	if d.Delivery == "held" {
		state = "held · " + state
	}
	return state
}

// PanelThreadEntry is one timeline entry: a head line naming the item, then
// its text through md.
func PanelThreadEntry(it inbox.ThreadItem, width int, at time.Time, p Painter) []string {
	switch {
	case it.Note != nil:
		n := *it.Note
		head := faint(WrapRest(n.Label()+" · "+n.SessionName+" · "+Age(n.CreatedAt, at), width, "", "  "))
		lines := PanelNoteLines(n, width, at, p)
		title := len(Wrap(n.Title, width, ""))
		if n.Unread() {
			for i := range title {
				lines[i] = boldText.Render(lines[i])
			}
		}
		return append(head, lines...)
	case it.Decision != nil:
		d := *it.Decision
		head := d.ID + QuestionTags(d) + " · " + d.SessionName + " · " + Age(d.CreatedAt, at)
		if d.Prod != nil {
			head += " · approval"
		}
		out := faint(WrapRest(head, width, "", "  "))
		out = append(out, Painted(p, md.Key{ID: d.ID, Rev: d.Rev, Field: "question"}, d.Question, width, "", "")...)
		out = append(out, PanelCmdLines(d, width, p)...)
		out = append(out, PanelBodyLines(d, width, p)...)
		state := WrapRest(DecisionState(d), width, "", "  ")
		if d.Status == "withdrawn" {
			state = faint(state)
		}
		out = append(out, state...)
		if !pyfmt.Blank(d.Note) {
			out = append(out, WrapRest("Note: "+d.Note, width, "", "  ")...)
		}
		return out
	}
	c := *it.Chat
	head := "✉ you → chief · " + Age(c.SentAt, at) + " · " + ChatStatus(c.Status)
	switch {
	case c.Status == "waiting":
		head += " for chief"
	case c.Error != "":
		head += " · " + c.Error
	}
	out := faint(WrapRest(head, width, "", "  "))
	return append(out, Painted(p, md.Key{ID: c.Label(), Field: "chat"}, c.Text, width, "", "")...)
}

// PanelThreadLines is every entry of a thread, oldest first.
func PanelThreadLines(th inbox.Thread, width int, at time.Time, p Painter) [][]string {
	out := make([][]string, len(th.Items))
	for i, it := range th.Items {
		out[i] = PanelThreadEntry(it, width, at, p)
	}
	return out
}

// DeliveryKind is an entry's kind as shown.
func DeliveryKind(e inbox.Entry) string {
	if e.Kind == inbox.KindFalseDrop {
		return "false drop"
	}
	return e.Kind
}

// DeliveryPreview is the start of an entry's body on one line.
func DeliveryPreview(e inbox.Entry) string {
	return strings.Join(strings.Fields(e.Body), " ")
}

// DeliveryLine is one entry: kind, sender → recipient, age, reason, then a
// body preview; a closed one ends with how it closed.
func DeliveryLine(e inbox.Entry, at time.Time) string {
	parts := []string{DeliveryKind(e), e.Sender + " → " + e.Recipient(), Age(e.When(), at)}
	if e.Reason != "" {
		parts = append(parts, e.Reason)
	}
	if p := DeliveryPreview(e); p != "" {
		parts = append(parts, "“"+p+"”")
	}
	if !e.Open() {
		parts = append(parts, e.Resolution)
	}
	return strings.Join(parts, " · ")
}

// DeliveryRow is the cockpit's Delivery row for the open entries; lost is
// whether any of them is a loss.
func DeliveryRow(open []inbox.Entry) (label string, lost bool) {
	n := 0
	for _, e := range open {
		if e.Kind == inbox.KindLost {
			n++
		}
	}
	label = "Delivery · " + strconv.Itoa(len(open)) + " open"
	if n > 0 {
		label += " · " + strconv.Itoa(n) + " lost"
	}
	return label, n > 0
}

// ChatStatus is a chat's stored status as shown; a loss says how to resend.
func ChatStatus(status string) string {
	if status == inbox.StatusLost {
		return "lost · r in Delivery to resend"
	}
	return status
}
