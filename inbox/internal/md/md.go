// Package md renders agent text as markdown for the panel: glamour with an
// ANSI-16 style, hard-wrapped so no line is wider than asked.
package md

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
	gansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	gtext "github.com/yuin/goldmark/text"
)

// Key names one rendered field: a decision's question, context or option at
// a revision, or a brief.
type Key struct {
	ID    string
	Rev   int64
	Field string
	Width int
}

type entry struct {
	text  string
	lines []string
}

// maxEntries bounds the cache; it is cleared when full.
const maxEntries = 512

// Renderer keeps one glamour renderer per width for the current colour
// profile, and the rendered lines per Key. Not safe for concurrent use.
type Renderer struct {
	lr      *lipgloss.Renderer
	profile termenv.Profile
	trs     map[int]*glamour.TermRenderer
	cache   map[Key]entry
	fail    error // set by tests to force the raw-text fallback
	// srcs are the raw code blocks seen so far, by id; ids[src] dedups.
	srcs []string
	ids  map[string]int
}

func New(lr *lipgloss.Renderer) *Renderer {
	return &Renderer{lr: lr, profile: lr.ColorProfile(), trs: map[int]*glamour.TermRenderer{}, cache: map[Key]entry{}, ids: map[string]int{}}
}

func (r *Renderer) sync() {
	if p := r.lr.ColorProfile(); p != r.profile {
		r.profile, r.trs, r.cache = p, map[int]*glamour.TermRenderer{}, map[Key]entry{}
	}
}

// Cached is Lines for k, rendered again only when k or its text changes.
func (r *Renderer) Cached(k Key, text string) []string {
	r.sync()
	if e, ok := r.cache[k]; ok && e.text == text {
		return e.lines
	}
	lines := r.Lines(text, k.Width)
	if len(r.cache) >= maxEntries {
		clear(r.cache)
	}
	r.cache[k] = entry{text, lines}
	return lines
}

// Lines renders text at width: blank edges dropped, every line at most width
// cells and ending reset, and no escape bytes at all under the Ascii profile.
// Text that fails to render comes back raw, still wrapped.
func (r *Renderer) Lines(text string, width int) []string {
	r.sync()
	width = max(width, 1)
	// Cells are counted per rune, so a tab would overrun the width; the fence
	// sentinels (Nerd Font glyphs too) would open a box mid-text.
	srcs := rawBlocks(text)
	text = inputFix.Replace(text)
	out, err := r.render(text, width)
	if err != nil {
		out = text
	}
	ascii := r.profile == termenv.Ascii
	var lines []string
	n := 0
	raw := strings.Split(out, "\n")
	for i := 0; i < len(raw); i++ {
		if lead, _, ok := strings.Cut(raw[i], fenceOpen); ok {
			var code []string
			i, code = fenced(raw, i)
			leadW := lipgloss.Width(lead)
			for j, l := range code {
				code[j] = ansi.TruncateLeft(l, leadW, "")
			}
			id := -1
			if n < len(srcs) {
				id = r.register(srcs[n].src)
				if whole := r.unwrapped(srcs[n]); whole != nil {
					code = whole
				}
			}
			n++
			lines = append(lines, r.block(lead, code, width, ascii, id)...)
			continue
		}
		lines = append(lines, wrap(raw[i], width, ascii)...)
	}
	lines = slices.CompactFunc(lines, func(a, b string) bool { return a == "" && b == "" })
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// fenced is the code lines of the block opening at raw[i] and the index of
// its closing line.
func fenced(raw []string, i int) (int, []string) {
	var code []string
	for i++; i < len(raw); i++ {
		body, _, closed := strings.Cut(raw[i], fenceClose)
		if !closed || strings.TrimSpace(ansi.Strip(body)) != "" {
			code = append(code, body)
		}
		if closed {
			break
		}
	}
	return i, code
}

// unwrapped renders one code block alone, wide enough that glamour's word
// wrap never splits a line, so block breaks long lines only at the pane edge.
// nil when that render fails.
func (r *Renderer) unwrapped(b rawBlock) []string {
	src := inputFix.Replace(b.src)
	wide := 256
	for _, l := range strings.Split(src, "\n") {
		for wide < lipgloss.Width(l)+8 {
			wide *= 2
		}
	}
	open, rest, _ := strings.Cut(Fence(src), "\n")
	out, err := r.render(open+b.lang+"\n"+rest, wide)
	if err != nil {
		return nil
	}
	raw := strings.Split(out, "\n")
	for i := range raw {
		if strings.Contains(raw[i], fenceOpen) {
			_, code := fenced(raw, i)
			return code
		}
	}
	return nil
}

// render is glamour's output; a panic inside it is an error too.
func (r *Renderer) render(text string, width int) (out string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("md: render panicked: %v", p)
		}
	}()
	if r.fail != nil {
		return "", r.fail
	}
	return r.term(width).Render(text)
}

// wrap hard-wraps one rendered line to width, each piece keeping the colour
// open at its start.
func wrap(raw string, width int, ascii bool) []string {
	var out []string
	active := ""
	for _, piece := range strings.Split(ansi.Hardwrap(raw, width, true), "\n") {
		line := active + piece
		active = carry(active, piece)
		out = append(out, finish(line, ascii))
	}
	return out
}

// Fenced code blocks reach Lines between these private-use runes, so box can
// frame them after glamour has laid out the rest.
const (
	fenceOpen  = "\ue000"
	fenceClose = "\ue001"
)

var (
	inputKeepTabs = strings.NewReplacer(fenceOpen, "\ufffd", fenceClose, "\ufffd")
	inputFix      = strings.NewReplacer("\t", "    ", fenceOpen, "\ufffd", fenceClose, "\ufffd")
)

// CodeMark leads every line of a code block, header included, so a caller
// can leave its own gutter off them; it is zero meaning, one cell wide.
const CodeMark = "\ue004"

const (
	markOpen  = "\ue002"
	markClose = "\ue003"
	codeBG    = "\x1b[100m"
)

var marker = regexp.MustCompile(markOpen + `(\d+)` + markClose)

func (r *Renderer) register(src string) int {
	if id, ok := r.ids[src]; ok {
		return id
	}
	if len(r.srcs) >= maxEntries*8 {
		// Cached lines carry ids into srcs; drop them with the table.
		r.srcs, r.ids = nil, map[string]int{}
		clear(r.cache)
	}
	r.srcs = append(r.srcs, src)
	r.ids[src] = len(r.srcs) - 1
	return len(r.srcs) - 1
}

// rawBlock is one code block's source as written and its fence language.
type rawBlock struct{ src, lang string }

// rawBlocks are the code blocks of text in document order, the same blocks
// glamour renders between the fence runes.
func rawBlocks(text string) []rawBlock {
	src := []byte(inputKeepTabs.Replace(text))
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(gtext.NewReader(src))
	var out []rawBlock
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			lang := ""
			if f, ok := n.(*ast.FencedCodeBlock); ok {
				lang = string(f.Language(src))
			}
			var b strings.Builder
			ls := n.Lines()
			for i := range ls.Len() {
				seg := ls.At(i)
				b.Write(seg.Value(src))
			}
			out = append(out, rawBlock{strings.TrimSuffix(b.String(), "\n"), lang})
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return out
}

// Number labels each code block header in lines "[n] copy", n continuing
// from len(blocks), and returns the lines with blocks plus their raw source.
func (r *Renderer) Number(lines, blocks []string) ([]string, []string) {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = marker.ReplaceAllStringFunc(l, func(m string) string {
			id, err := strconv.Atoi(marker.FindStringSubmatch(m)[1])
			if err != nil || id >= len(r.srcs) {
				return ""
			}
			blocks = append(blocks, r.srcs[id])
			return "[" + strconv.Itoa(len(blocks)) + "] copy"
		})
	}
	return out, blocks
}

// Unlabel drops the copy labels from lines, for a screen where y does not copy.
func (r *Renderer) Unlabel(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = marker.ReplaceAllString(l, "")
	}
	return out
}

// block lays code lines (lead already cut off) at lead's indent on a tinted background, padded to
// the widest line, with a copy label above. No border characters sit beside
// the code, so a drag-select copies only the code.
func (r *Renderer) block(lead string, code []string, width int, ascii bool, id int) []string {
	leadW := lipgloss.Width(lead)
	inner := max(width-leadW-3, 1)
	var body []string
	for _, l := range code {
		body = append(body, wrap(finish(l, ascii), inner, ascii)...)
	}
	for len(body) > 0 && strings.TrimSpace(ansi.Strip(body[len(body)-1])) == "" {
		body = body[:len(body)-1]
	}
	cw := 0
	for _, l := range body {
		cw = max(cw, lipgloss.Width(l))
	}
	pad := strings.Repeat(" ", leadW)
	var out []string
	if id >= 0 {
		head := markOpen + strconv.Itoa(id) + markClose
		if !ascii {
			head = r.lr.NewStyle().Foreground(lipgloss.Color("8")).Render(head)
		}
		out = append(out, CodeMark+pad+head)
	}
	for _, l := range body {
		line := " " + l + strings.Repeat(" ", cw-lipgloss.Width(l)) + " "
		if !ascii {
			line = codeBG + sgr.ReplaceAllStringFunc(line, func(m string) string {
				if p := sgr.FindStringSubmatch(m)[1]; p == "" || p == "0" {
					return m + codeBG
				}
				return m
			}) + reset
		}
		out = append(out, CodeMark+pad+line)
	}
	return out
}

const reset = "\x1b[0m"

// finish strips the right padding glamour adds up to the wrap width and
// closes any colour, or drops every escape under Ascii.
func finish(line string, ascii bool) string {
	visible := strings.TrimRight(ansi.Strip(line), " ")
	if ascii || !strings.Contains(line, "\x1b") {
		return visible
	}
	if visible == "" {
		return ""
	}
	return trailingSGR.ReplaceAllString(ansi.Truncate(line, lipgloss.Width(visible), ""), "") + reset
}

var (
	sgr         = regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	trailingSGR = regexp.MustCompile(`(\x1b\[[0-9;]*m)+$`)
)

// carry is the SGR state still open after s, starting from active, so a line
// split from a styled one keeps its colour.
func carry(active, s string) string {
	for _, m := range sgr.FindAllStringSubmatch(s, -1) {
		if strings.Trim(m[1], "0;") == "" {
			active = ""
		} else {
			active += m[0]
		}
	}
	return active
}

func (r *Renderer) term(width int) *glamour.TermRenderer {
	if tr, ok := r.trs[width]; ok {
		return tr
	}
	tr, err := glamour.NewTermRenderer(
		glamour.WithStyles(Style()),
		glamour.WithColorProfile(r.profile),
		glamour.WithWordWrap(width),
		glamour.WithPreservedNewLines(),
		glamour.WithChromaFormatter("terminal16"),
	)
	if err != nil {
		// Only invalid options fail, and these are fixed.
		panic(err)
	}
	r.trs[width] = tr
	return tr
}

func ptr[T any](v T) *T { return &v }

// Chroma entries are hex values the terminal16 formatter maps exactly to
// SGR 30-37/90-97.
const (
	hexGreen   = "#007f00"
	hexYellow  = "#7f7fe0"
	hexMagenta = "#7f007f"
	hexCyan    = "#007f7f"
	hexGrey    = "#555555"
)

// Style is a crush-like dark style cut down to ANSI 16 indexes: magenta
// headings and bullets, tinted inline code, no margins that pad lines.
func Style() gansi.StyleConfig {
	return gansi.StyleConfig{
		Document: gansi.StyleBlock{Margin: ptr(uint(0))},
		BlockQuote: gansi.StyleBlock{
			StylePrimitive: gansi.StylePrimitive{Italic: ptr(true)},
			Indent:         ptr(uint(1)), IndentToken: ptr("│ "),
		},
		List: gansi.StyleList{LevelIndent: 2},
		Heading: gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{
			Color: ptr("5"), Bold: ptr(true),
		}},
		H1: gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{
			Prefix: " ", Suffix: " ", Color: ptr("0"), BackgroundColor: ptr("5"),
		}},
		H2:             gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "## "}},
		H3:             gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "### "}},
		H4:             gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "#### "}},
		H5:             gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "##### "}},
		H6:             gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Prefix: "###### "}},
		Strikethrough:  gansi.StylePrimitive{CrossedOut: ptr(true)},
		Emph:           gansi.StylePrimitive{Italic: ptr(true)},
		Strong:         gansi.StylePrimitive{Bold: ptr(true)},
		HorizontalRule: gansi.StylePrimitive{Color: ptr("8"), Format: "\n────────\n"},
		Item:           gansi.StylePrimitive{Prefix: "• ", Color: ptr("5")},
		Enumeration:    gansi.StylePrimitive{BlockPrefix: ". "},
		Task:           gansi.StyleTask{Ticked: "[✓] ", Unticked: "[ ] "},
		Link:           gansi.StylePrimitive{Color: ptr("8"), Underline: ptr(true)},
		LinkText:       gansi.StylePrimitive{Color: ptr("4"), Bold: ptr(true), Underline: ptr(true)},
		Image:          gansi.StylePrimitive{Color: ptr("8"), Underline: ptr(true)},
		ImageText:      gansi.StylePrimitive{Color: ptr("4"), Format: "Image: {{.text}} →"},
		Code: gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{
			Color: ptr("6"), BackgroundColor: ptr("8"),
		}},
		CodeBlock: gansi.StyleCodeBlock{
			StyleBlock: gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{
				Color: ptr("6"), BlockPrefix: fenceOpen + "\n", BlockSuffix: fenceClose + "\n",
			}, Margin: ptr(uint(0))},
			Chroma: &gansi.Chroma{
				Text:                gansi.StylePrimitive{Color: ptr(hexCyan)},
				Comment:             gansi.StylePrimitive{Color: ptr(hexGrey)},
				CommentPreproc:      gansi.StylePrimitive{Color: ptr(hexMagenta)},
				Keyword:             gansi.StylePrimitive{Color: ptr(hexMagenta)},
				KeywordReserved:     gansi.StylePrimitive{Color: ptr(hexMagenta)},
				KeywordNamespace:    gansi.StylePrimitive{Color: ptr(hexMagenta)},
				KeywordType:         gansi.StylePrimitive{Color: ptr(hexMagenta)},
				Name:                gansi.StylePrimitive{Color: ptr(hexCyan)},
				NameBuiltin:         gansi.StylePrimitive{Color: ptr(hexCyan)},
				NameFunction:        gansi.StylePrimitive{Color: ptr(hexCyan)},
				LiteralNumber:       gansi.StylePrimitive{Color: ptr(hexYellow)},
				LiteralString:       gansi.StylePrimitive{Color: ptr(hexGreen)},
				LiteralStringEscape: gansi.StylePrimitive{Color: ptr(hexGreen)},
				GenericDeleted:      gansi.StylePrimitive{Color: ptr("#7f0000")},
				GenericInserted:     gansi.StylePrimitive{Color: ptr(hexGreen)},
				GenericEmph:         gansi.StylePrimitive{Italic: ptr(true)},
				GenericStrong:       gansi.StylePrimitive{Bold: ptr(true)},
			},
		},
		Table: gansi.StyleTable{
			CenterSeparator: ptr("┼"), ColumnSeparator: ptr("│"), RowSeparator: ptr("─"),
		},
	}
}

// Fence wraps code in a fenced block whose fence outruns any backtick run
// inside it, so the block's source is code exactly.
func Fence(code string) string {
	n, run := 2, 0
	for _, r := range code {
		if r != '`' {
			run = 0
			continue
		}
		run++
		n = max(n, run)
	}
	fence := strings.Repeat("`", n+1)
	return fence + "\n" + code + "\n" + fence
}

var plainMarks = []*regexp.Regexp{
	regexp.MustCompile("`+([^`]+)`+"),
	regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`),
	regexp.MustCompile(`\*\*([^*\s](?:[^*]*[^*\s])?)\*\*`),
	regexp.MustCompile(`~~([^~\s](?:[^~]*[^~\s])?)~~`),
	regexp.MustCompile(`(^|[^*\w])\*([^*\s](?:[^*]*[^*\s])?)\*`),
}

// Plain drops inline markdown markers (code ticks, bold, italic, strike,
// link targets) for one-line rows; the text inside stays.
func Plain(s string) string {
	for i, re := range plainMarks {
		repl := "$1"
		if i == len(plainMarks)-1 {
			repl = "$1$2"
		}
		s = re.ReplaceAllString(s, repl)
	}
	return s
}
