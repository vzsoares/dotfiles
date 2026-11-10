package tui

import (
	"strconv"
	"strings"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

// CutOrder is what a question screen gives up, in order, as rows run out.
var CutOrder = []string{"brief", "context", "asked-by", "help", "menu", "title"}

type item struct{ label, value string }

func questionMenu(d inbox.Decision) []item {
	var out []item
	for _, o := range d.Options {
		label := o.Letter
		if o.Letter == d.Rec {
			label += view.RecMark
		}
		out = append(out, item{label, o.Letter})
	}
	return append(out, item{"Free text", "free"}, item{"Skip", "skip"}, item{"Talk", "talk"},
		item{"View full", "full"}, item{"Back", "back"})
}

// walkFit is a question screen fitted to the pane.
type walkFit struct {
	top   []string
	body  []string
	menuH int
	help  bool
	cuts  []string
	// scroll means body overflows and is shown through a window of rows.
	scroll bool
	rows   int
}

// fitWalk cuts in CutOrder until the question, its options and the menu
// fit; when the question and options alone overflow, everything (context
// included) goes into a scrolling body so no option text is ever clipped.
func fitWalk(d inbox.Decision, b view.Brief, banner []string, width, height, items, footer, helpRows int) walkFit {
	head := view.WrapRest(b.Head(), width, "", "  ")
	var text []string
	if !pyfmt.Blank(b.Text) {
		text = view.Wrap(b.Text, width, "  ")
	}
	var asked []string
	if b.AskedBy != "" {
		asked = view.Wrap(b.AskedBy, width, "")
	}
	question := view.QuestionLines(d, width)
	ctx := view.ContextLines(d, width)
	opts := view.OptionLines(d, width)
	cat := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	full := []int{items}
	var paged []int
	for h := items - 1; h >= 1; h-- {
		paged = append(paged, h)
	}
	stages := []struct {
		cuts    int
		top     []string
		clip    bool
		help    bool
		heights []int
	}{
		{0, cat(banner, head, text, asked, []string{""}), false, true, full},
		{1, cat(banner, head, asked, []string{""}), false, true, full},
		{2, cat(banner, head, asked), true, true, full},
		{3, cat(banner, head), true, true, full},
		{4, cat(banner, head), true, false, full},
		{5, cat(banner, head), true, false, paged},
		{6, nil, true, false, paged},
	}
	for _, st := range stages {
		help := 0
		if st.help {
			help = helpRows
		}
		for _, h := range st.heights {
			room := height - footer - help - h - len(st.top) - len(question) - len(opts)
			need := len(ctx)
			if st.clip {
				need = min(len(ctx), 1)
			}
			if room < need {
				continue
			}
			shown := ctx
			if st.clip {
				shown = view.ClipLines(ctx, room, width)
			}
			return walkFit{top: st.top, body: cat(question, shown, opts), menuH: h,
				help: st.help, cuts: append([]string(nil), CutOrder[:st.cuts]...)}
		}
	}
	rows := max(height-footer-1, 1)
	return walkFit{body: cat(question, ctx, opts), menuH: 1, cuts: append([]string(nil), CutOrder...),
		scroll: true, rows: rows}
}

// fitText picks the first variant that fits above a full menu, else above a
// paged one; failing both, the last variant scrolls above a one-row menu.
func fitText(variants [][]string, items, rows int) (text []string, menuH int, scroll bool) {
	for _, v := range variants {
		room := rows - len(v)
		if room >= items {
			return v, items, false
		}
		if room >= 1 {
			return v, room, false
		}
	}
	return variants[len(variants)-1], 1, true
}

func reviewLines(entries []entry, width int, questionWidth int) []string {
	out := []string{"Review: Send records these answers.", ""}
	for _, e := range entries {
		q := e.d.Question
		if questionWidth >= 0 {
			q = pyfmt.Clip(q, questionWidth)
		}
		head := e.d.ID
		if q != "" {
			head += "  " + q
		}
		out = append(out, view.WrapRest(head+" → "+view.StagedText(e.d, e.staged), width, "", "    ")...)
	}
	return out
}

type key struct{ key, label string }

// Footer labels stay short so the walk's keys fit in two rows at 40 columns;
// ? adds the longer explanation.
var footers = map[screen][]key{
	scList:     {{"↑↓", ""}, {"enter", "open"}, {"q", "quit"}, {"?", "help"}},
	scSession:  {{"↑↓", ""}, {"enter", ""}, {"t", "talk"}, {"esc", "back"}, {"?", "help"}},
	scWalk:     {{"↑↓", ""}, {"enter", ""}, {"a-d", "pick"}, {"f", "free"}, {"s", "skip"}, {"t", "talk"}, {"v", "full"}, {"esc", "back"}, {"?", "help"}},
	scReview:   {{"↑↓", ""}, {"enter", ""}, {"esc", "cancel"}, {"?", "help"}},
	scChange:   {{"↑↓", ""}, {"enter", ""}, {"esc", "back"}},
	scFree:     {{"ctrl+d", "send"}, {"esc", "cancel"}},
	scFull:     {{"↑↓", "scroll"}, {"esc", "back"}},
	scTalkPick: {{"↑↓", ""}, {"space", "mark"}, {"enter", "done"}, {"esc", "none"}},
	scTalkNote: {{"enter", "done"}, {"esc", "no note"}},
}

var helps = map[screen]string{
	scList:    "One row per asking session, oldest first. Open one to see its tasks and questions.",
	scSession: "Answer walks the questions one by one; Talk opens the agent in a popup.",
	scWalk:    "a-d picks, f writes free text, s skips, t talks to the agent, v shows everything; nothing is recorded until Send on the review screen.",
	scReview:  "Send records the staged answers; Change one walks a single question again.",
}

func footerLines(s screen, width int, scroll bool) []string {
	keys := footers[s]
	if scroll {
		keys = append(append([]key(nil), keys...), key{"pgup/dn", "scroll"})
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = strings.TrimSpace(k.key + " " + k.label)
	}
	return view.WrapRest(strings.Join(parts, " · "), width, "", "")
}

func helpLines(s screen, width int) []string {
	if helps[s] == "" {
		return nil
	}
	return view.Wrap(helps[s], width, "")
}

// window is the slice of n rows starting at off, clamped.
func window(lines []string, off, n int) ([]string, int) {
	off = max(min(off, len(lines)-n), 0)
	end := min(off+n, len(lines))
	return lines[off:end], off
}

// menuWindow renders n rows of items around the cursor.
func menuWindow(items []item, cursor, n, width int) []string {
	start := 0
	if cursor >= n {
		start = cursor - n + 1
	}
	end := min(start+n, len(items))
	var out []string
	for i := start; i < end; i++ {
		prefix := "  "
		if i == cursor {
			prefix = "› "
		}
		label := items[i].label
		if i == start && n < len(items) {
			label += "  (" + strconv.Itoa(cursor+1) + "/" + strconv.Itoa(len(items)) + ")"
		}
		out = append(out, prefix+pyfmt.Clip(label, max(width-2, 1)))
	}
	return out
}
