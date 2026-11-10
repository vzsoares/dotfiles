// Package pyfmt reproduces the Python string and JSON behaviour zen-inbox's
// output depended on (textwrap, repr, json.dumps, str.splitlines), so the
// Go binary prints byte for byte what inbox.py printed.
package pyfmt

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// IsSpace is Python's str.isspace for one code point.
func IsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// Strip is Python's str.strip().
func Strip(s string) string { return strings.TrimFunc(s, IsSpace) }

// RStrip is Python's str.rstrip().
func RStrip(s string) string { return strings.TrimRightFunc(s, IsSpace) }

// Blank reports whether s.strip() is empty.
func Blank(s string) bool { return Strip(s) == "" }

// Len is Python's len() of a str: code points, not bytes.
func Len(s string) int { return utf8.RuneCountInString(s) }

// Clip keeps text up to width code points, ending in "…" when cut.
func Clip(text string, width int) string {
	r := []rune(text)
	if len(r) <= width {
		return text
	}
	return string(r[:max(width-1, 0)]) + "…"
}

// Head returns the first n code points of s.
func Head(s string, n int) string {
	r := []rune(s)
	if n >= len(r) {
		return s
	}
	return string(r[:max(n, 0)])
}

func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

// SplitLines is Python's str.splitlines(): no trailing empty line, "" → [].
func SplitLines(s string) []string {
	var out []string
	r := []rune(s)
	start := 0
	for i := 0; i < len(r); i++ {
		if !isLineBreak(r[i]) {
			continue
		}
		out = append(out, string(r[start:i]))
		if r[i] == '\r' && i+1 < len(r) && r[i+1] == '\n' {
			i++
		}
		start = i + 1
	}
	if start < len(r) {
		out = append(out, string(r[start:]))
	}
	return out
}

func expandTabs(s string, size int) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			n := size - col%size
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case '\n', '\r':
			b.WriteRune(r)
			col = 0
		default:
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
}

func munge(s string) string {
	s = expandTabs(s, 8)
	return strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\v', '\f', '\r':
			return ' '
		}
		return r
	}, s)
}

func splitChunks(s string) [][]rune {
	var chunks [][]rune
	var cur []rune
	space := false
	for _, r := range s {
		isSp := r == ' '
		if len(cur) > 0 && isSp != space {
			chunks = append(chunks, cur)
			cur = nil
		}
		space = isSp
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

func blankRunes(r []rune) bool {
	for _, c := range r {
		if !IsSpace(c) {
			return false
		}
	}
	return true
}

// TextWrap is textwrap.wrap(text, width, initial_indent=initial,
// subsequent_indent=subsequent, break_on_hyphens=False) from Python 3.13.
func TextWrap(text string, width int, initial, subsequent string) []string {
	chunks := splitChunks(munge(text))
	// Reverse so the next chunk is at the end, as textwrap does.
	for i, j := 0, len(chunks)-1; i < j; i, j = i+1, j-1 {
		chunks[i], chunks[j] = chunks[j], chunks[i]
	}
	var lines []string
	for len(chunks) > 0 {
		var cur [][]rune
		curLen := 0
		indent := initial
		if len(lines) > 0 {
			indent = subsequent
		}
		w := width - Len(indent)
		if blankRunes(chunks[len(chunks)-1]) && len(lines) > 0 {
			chunks = chunks[:len(chunks)-1]
		}
		for len(chunks) > 0 {
			l := len(chunks[len(chunks)-1])
			if curLen+l > w {
				break
			}
			cur = append(cur, chunks[len(chunks)-1])
			chunks = chunks[:len(chunks)-1]
			curLen += l
		}
		if len(chunks) > 0 && len(chunks[len(chunks)-1]) > w {
			spaceLeft := w - curLen
			if w < 1 {
				spaceLeft = 1
			}
			if spaceLeft > 0 {
				last := chunks[len(chunks)-1]
				cur = append(cur, last[:spaceLeft])
				chunks[len(chunks)-1] = last[spaceLeft:]
			}
		}
		if len(cur) > 0 && blankRunes(cur[len(cur)-1]) {
			cur = cur[:len(cur)-1]
		}
		if len(cur) > 0 {
			var b strings.Builder
			b.WriteString(indent)
			for _, c := range cur {
				b.WriteString(string(c))
			}
			lines = append(lines, b.String())
		}
	}
	return lines
}

// Repr is Python's repr() of a str.
func Repr(s string) string {
	quote := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, r := range s {
		switch {
		case r == quote || r == '\\':
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < ' ' || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f || unicode.IsPrint(r):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x10000:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteRune(quote)
	return b.String()
}

// KV is one key of an ordered JSON object.
type KV struct {
	Key   string
	Value any
}

// Object is a JSON object that keeps its key order, like a Python dict.
type Object []KV

// Dumps is json.dumps(v) with Python's defaults: ", " / ": " separators and
// ensure_ascii. Values: string, bool, int, int64, nil, Object, []Object, []any.
func Dumps(v any) string {
	var b strings.Builder
	dump(&b, v)
	return b.String()
}

func dump(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case string:
		dumpString(b, x)
	case Object:
		b.WriteByte('{')
		for i, kv := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			dumpString(b, kv.Key)
			b.WriteString(": ")
			dump(b, kv.Value)
		}
		b.WriteByte('}')
	case []Object:
		b.WriteByte('[')
		for i, o := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			dump(b, o)
		}
		b.WriteByte(']')
	case []any:
		b.WriteByte('[')
		for i, o := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			dump(b, o)
		}
		b.WriteByte(']')
	default:
		panic(fmt.Sprintf("pyfmt.Dumps: unsupported %T", v))
	}
}

func dumpString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r >= ' ' && r <= '~':
				b.WriteRune(r)
			case r < 0x10000:
				fmt.Fprintf(b, `\u%04x`, r)
			default:
				r -= 0x10000
				fmt.Fprintf(b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			}
		}
	}
	b.WriteByte('"')
}

// Truthy is Python truthiness of a value decoded with json UseNumber.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, ok := new(big.Float).SetString(string(x))
		return !ok || f.Sign() != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// Str is Python's str() of a JSON-decoded value.
func Str(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	case json.Number:
		return pyNumber(string(x))
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = reprValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		return reprMap(x)
	}
	return fmt.Sprint(v)
}

func reprValue(v any) string {
	if s, ok := v.(string); ok {
		return Repr(s)
	}
	return Str(v)
}

func reprMap(m map[string]any) string {
	// Go maps lose JSON key order; sorted keys are the closest stable answer.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = Repr(k) + ": " + reprValue(m[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func pyNumber(s string) string {
	if !strings.ContainsAny(s, ".eE") {
		n, ok := new(big.Int).SetString(s, 10)
		if ok {
			return n.String()
		}
		return s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	r := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(r, ".eEn") {
		r += ".0"
	}
	return r
}
