package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

// CutOrder is what a question screen gives up, in order, as rows run out.
var CutOrder = []string{"brief", "context", "asked-by", "help", "menu", "title"}

type item struct{ label, value string }

// questionMenu is the walk's menu; a locked prod ask offers no answer.
func questionMenu(d inbox.Decision, locked bool) []item {
	if locked {
		return []item{{"Skip", "skip"}, {"Talk", "talk"}, {"View full", "full"}, {"Back", "back"}}
	}
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
	// scroll means body overflows the pane and scrolls.
	scroll bool
}

// fitWalk cuts in CutOrder until the question, its options and the menu
// fit; when the question and options alone overflow, everything (context
// included) goes into a scrolling body so no option text is ever clipped.
func fitWalk(d inbox.Decision, b view.Brief, banner []string, notice string, width, height, items, footer, helpRows int,
	p view.Painter) walkFit {
	head := view.WrapRest(b.Head(), width, "", "  ")
	text := view.PanelBriefText(b, width, p)
	var asked []string
	if b.AskedBy != "" {
		asked = view.Wrap(b.AskedBy, width, "")
	}
	question := view.PanelQuestionLines(d, width, p)
	if notice != "" {
		question = append(question, view.WrapRest(notice, width, "", "  ")...)
	}
	ctx := view.PanelContextLines(d, width, p)
	opts := view.PanelOptionLines(d, width, p)
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
		{0, slices.Concat(banner, head, text, asked, []string{""}), false, true, full},
		{1, slices.Concat(banner, head, asked, []string{""}), false, true, full},
		{2, slices.Concat(banner, head, asked), true, true, full},
		{3, slices.Concat(banner, head), true, true, full},
		{4, slices.Concat(banner, head), true, false, full},
		{5, slices.Concat(banner, head), true, false, paged},
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
			return walkFit{top: st.top, body: slices.Concat(question, shown, opts), menuH: h,
				help: st.help, cuts: append([]string(nil), CutOrder[:st.cuts]...)}
		}
	}
	// The title stays pinned while the rest scrolls, when that leaves room.
	top := slices.Concat(banner, head)
	if height-footer-1-len(top) < 3 {
		top = nil
	}
	// Leave the body three rows, its two more-markers and a footer row for
	// the scroll hint.
	menuH := max(min(items, height-footer-1-len(top)-5), 1)
	return walkFit{top: top, body: slices.Concat(question, ctx, opts), menuH: menuH, help: true,
		cuts: append([]string(nil), CutOrder...), scroll: true}
}

// fitText picks the first variant that fits above a full menu, else above a
// paged one; failing both, the last variant goes above a one-row menu.
func fitText(variants [][]string, items, rows int) (text []string, menuH int) {
	for _, v := range variants {
		room := rows - len(v)
		if room >= items {
			return v, items
		}
		if room >= 1 {
			return v, room
		}
	}
	return variants[len(variants)-1], 1
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

// spare hints are dropped when the footer would take more than two rows;
// both sit in the walk's menu too.
var spare = map[string]bool{"t talk": true, "v full": true}

// Footer labels stay short so the walk's keys fit in two rows at 40 columns;
// ? adds the longer explanation. The list builds its own from the cursor.
var footers = map[screen][]key{
	scSession:   {{"↑↓", ""}, {"enter", ""}, {"t", "talk"}, {"esc", "back"}, {"?", "help"}},
	scWalk:      {{"↑↓", ""}, {"enter", ""}, {"a-d", "pick"}, {"f", "free"}, {"s", "skip"}, {"t", "talk"}, {"v", "full"}, {"←→", "move"}, {"l", "list"}, {"esc", "back"}, {"?", "help"}},
	scReview:    {{"↑↓", ""}, {"enter", ""}, {"←", "prev"}, {"l", "list"}, {"esc", "cancel"}, {"?", "help"}},
	scJump:      {{"↑↓", ""}, {"enter", "jump"}, {"esc", "back"}},
	scFree:      {{"ctrl+d", "send"}, {"esc", "keep"}},
	scFreeBlank: {{"ctrl+d", "send"}, {"esc", "back"}},
	scFull:      {{"↑↓", "scroll"}, {"esc", "back"}},
	scTalkPick:  {{"↑↓", ""}, {"space", "mark"}, {"enter", "done"}, {"esc", "none"}},
	scTalkNote:  {{"enter", "done"}, {"esc", "no note"}},
	scConfirm:   {{"y", "approve"}, {"n/esc", "back"}},
	scNote:      {{"↑↓", ""}, {"m", "mark"}, {"f", "reply"}, {"esc", "back"}, {"?", "help"}},
	scReply:     {{"ctrl+d", "send"}, {"esc", "back"}},
	scChief:     {{"enter", "send"}, {"ctrl+j", "newline"}, {"esc", "cancel"}},
	scLocked:    {{"↑↓", ""}, {"enter", ""}, {"s", "skip"}, {"t", "talk"}, {"v", "full"}, {"←→", "move"}, {"l", "list"}, {"esc", "back"}, {"?", "help"}},
}

var helps = map[screen]string{
	scList: "Failed deliveries, asks (prod first), notes from chief and held answers, most urgent first." +
		" enter opens the row under the cursor, a answers a session's asks, f replies to a note," +
		" m marks a note read or unread, M marks them all read, r retries a failed delivery, c writes to chief.",
	scSession: "Answer walks the questions one by one; Talk opens the agent in a popup.",
	scWalk:    "a-d picks, f writes free text, s skips, t talks to the agent, v shows everything. ← or shift+tab goes back a question, → forward, l lists them all. Answers are kept as drafts, even across esc and restarts, and recorded only at Send. Approve on a prod ask asks y/N first.",
	scReview:  "Send records the staged answers; Change one or l lists the questions to jump to one; ← goes back to the last.",
	scLocked:  "This panel runs inside an agent session, so it cannot answer a prod ask; answer it from the cockpit terminal.",
	scConfirm: "y stages Approve for this exact sha; any other key goes back without staging it.",
	scNote:    "m marks this note read or unread; opening it does not. f writes one reply to the sender and marks it read.",
	scChief:   "enter sends the message to the live chief session, ctrl+j or alt+enter adds a line. esc cancels and keeps the draft for the next c.",
	scReply:   "ctrl+d sends the reply to the note's sender; an empty reply sends nothing.",
}

func (k key) text() string { return strings.TrimSpace(k.key + " " + k.label) }

// paintKeys is one footer row: keys bold, labels plain, faint separators.
func paintKeys(keys []key) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = keyStyle.Render(k.key)
		if k.label != "" {
			parts[i] += " " + k.label
		}
	}
	return strings.Join(parts, sepStyle.Render(" · "))
}

func keysWidth(keys []key) int {
	n := 0
	for i, k := range keys {
		if i > 0 {
			n += lipgloss.Width(" · ")
		}
		n += lipgloss.Width(k.text())
	}
	return n
}

// packKeys fills rows of width with whole keys, never splitting one.
func packKeys(keys []key, width int) [][]key {
	var rows [][]key
	var row []key
	for _, k := range keys {
		if len(row) > 0 && keysWidth(append(slices.Clone(row), k)) > width {
			rows = append(rows, row)
			row = nil
		}
		row = append(row, k)
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// scrollKey is the scroll hint; ctrl+u/d edit text on the input screens,
// so only pgup/dn scroll there.
func scrollKey(s screen) key {
	if s == scFree || s == scFreeBlank || s == scTalkNote || s == scReply || s == scChief {
		return key{"pgup/dn", "scroll"}
	}
	return key{"^u/^d", "scroll"}
}

func footerLines(s screen, width int, scroll bool) []string {
	keys := footers[s]
	if scroll {
		keys = append(slices.Clone(keys), scrollKey(s))
	}
	rows := packKeys(keys, width)
	if len(rows) > 2 {
		keys = slices.DeleteFunc(slices.Clone(keys), func(k key) bool { return spare[k.text()] })
		rows = packKeys(keys, width)
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = paintKeys(r)
	}
	return out
}

// ranked is a list footer key; the highest rank is dropped first when the
// row is too narrow.
type ranked struct {
	key
	rank int
}

// fitKeys keeps keys in order, dropping the highest-ranked (the last of a
// tie) until they fit one row.
func fitKeys(keys []ranked, width int) []key {
	keys = slices.Clone(keys)
	for {
		plain := make([]key, len(keys))
		for i, k := range keys {
			plain[i] = k.key
		}
		if len(keys) <= 1 || keysWidth(plain) <= width {
			return plain
		}
		drop := 0
		for i, k := range keys {
			if k.rank >= keys[drop].rank {
				drop = i
			}
		}
		keys = slices.Delete(keys, drop, drop+1)
	}
}

func helpLines(s screen, width int) []string {
	if helps[s] == "" {
		return nil
	}
	return view.Wrap(helps[s], width, "")
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
