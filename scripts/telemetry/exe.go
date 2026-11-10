package main

import (
	"path"
	"regexp"
	"strings"
)

// segment is one simple command: its executable (basename) and the words after it.
type segment struct {
	exe  string
	args []string
}

var (
	assignRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	durationRe = regexp.MustCompile(`^[0-9.]+[smhd]?$`)
	wrappers   = map[string]bool{"sudo": true, "env": true, "timeout": true, "nice": true, "command": true, "exec": true, "time": true}
	// Per-wrapper flags that take a separate value, e.g. `sudo -u root`, `nice -n 5`.
	wrapperArgFlags = map[string]map[string]bool{
		"sudo":    {"-u": true, "-g": true, "-C": true, "-D": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true},
		"env":     {"-u": true, "-C": true},
		"timeout": {"-s": true, "-k": true},
		"nice":    {"-n": true},
		"exec":    {"-a": true},
	}
)

// parseCommand splits a shell command into simple commands. It skips env
// assignments (`export` segments included), `cd` segments and the wrappers sudo, env, timeout, nice, command,
// exec and time; it does not unwrap `uv run` or `bun run`.
func parseCommand(cmd string) []segment {
	var out []segment
	for _, words := range splitSegments(cmd) {
		if s, ok := segmentOf(words); ok {
			out = append(out, s)
		}
	}
	return out
}

func segmentOf(words []string) (segment, bool) {
	i := 0
	for i < len(words) && assignRe.MatchString(words[i]) {
		i++
	}
	for i < len(words) && wrappers[path.Base(words[i])] {
		valueFlags := wrapperArgFlags[path.Base(words[i])]
		i++
		for i < len(words) {
			w := words[i]
			switch {
			case assignRe.MatchString(w), durationRe.MatchString(w), w == "--":
				i++
			case strings.HasPrefix(w, "-"):
				i++
				if valueFlags[w] && i < len(words) {
					i++
				}
			default:
				goto done
			}
		}
	done:
	}
	if i >= len(words) {
		return segment{}, false
	}
	exe := path.Base(words[i])
	if exe == "cd" || exe == "export" {
		return segment{}, false
	}
	return segment{exe: exe, args: words[i+1:]}, true
}

// splitSegments is a small shell lexer: quotes group words, operators
// (; & | && || newline ( ) { }) end a segment, redirections are dropped and
// heredoc bodies are skipped.
func splitSegments(cmd string) [][]string {
	var (
		segs     [][]string
		words    []string
		word     strings.Builder
		inWord   bool
		heredocs []string
		skipNext bool
	)
	flushWord := func() {
		if !inWord {
			return
		}
		w := word.String()
		word.Reset()
		inWord = false
		if skipNext {
			skipNext = false
			return
		}
		words = append(words, w)
	}
	endSeg := func() {
		flushWord()
		if len(words) > 0 {
			segs = append(segs, words)
		}
		words = nil
	}
	n := len(cmd)
	for i := 0; i < n; i++ {
		c := cmd[i]
		switch {
		case c == '\'':
			inWord = true
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				word.WriteString(cmd[i+1:])
				i = n
				break
			}
			word.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			inWord = true
			for i++; i < n && cmd[i] != '"'; i++ {
				if cmd[i] == '\\' && i+1 < n {
					i++
				}
				word.WriteByte(cmd[i])
			}
		case c == '\\' && i+1 < n:
			i++
			if cmd[i] != '\n' {
				inWord = true
				word.WriteByte(cmd[i])
			}
		case c == '#' && !inWord:
			for i < n && cmd[i] != '\n' {
				i++
			}
			i--
		case c == ' ' || c == '\t':
			flushWord()
		case c == '\n':
			endSeg()
			for _, delim := range heredocs {
				i = skipHeredoc(cmd, i+1, delim) - 1
			}
			heredocs = nil
		case c == ';' || c == '&' || c == '|' || c == '(' || c == ')' || c == '{' || c == '}':
			if c == '&' && i+1 < n && cmd[i+1] == '>' {
				flushWord()
				skipNext = true
				i++
				if i+1 < n && cmd[i+1] == '>' {
					i++
				}
				break
			}
			if (c == '{' || c == '}') && inWord {
				word.WriteByte(c)
				break
			}
			endSeg()
		case c == '<' || c == '>':
			if inWord && isDigits(word.String()) {
				word.Reset()
				inWord = false
			}
			flushWord()
			if c == '<' && i+1 < n && cmd[i+1] == '<' {
				i++
				if i+1 < n && cmd[i+1] == '<' { // here-string
					i++
					skipNext = true
					break
				}
				if i+1 < n && cmd[i+1] == '-' {
					i++
				}
				j := i + 1
				for j < n && (cmd[j] == ' ' || cmd[j] == '\t') {
					j++
				}
				k := j
				for k < n && !strings.ContainsRune(" \t\n;&|<>()", rune(cmd[k])) {
					k++
				}
				heredocs = append(heredocs, strings.Trim(cmd[j:k], `'"\`))
				i = k - 1
				break
			}
			for i+1 < n && (cmd[i+1] == '>' || cmd[i+1] == '&' || cmd[i+1] == '|') {
				i++
			}
			if i+1 < n && cmd[i+1] >= '0' && cmd[i+1] <= '9' && cmd[i] == '&' {
				for i+1 < n && cmd[i+1] >= '0' && cmd[i+1] <= '9' {
					i++
				}
				break
			}
			skipNext = true
		default:
			inWord = true
			word.WriteByte(c)
		}
	}
	endSeg()
	return segs
}

// skipHeredoc returns the index just past the line that equals delim.
func skipHeredoc(cmd string, start int, delim string) int {
	i := start
	for i < len(cmd) {
		j := strings.IndexByte(cmd[i:], '\n')
		line := cmd[i:]
		next := len(cmd)
		if j >= 0 {
			line = cmd[i : i+j]
			next = i + j + 1
		}
		if strings.TrimSpace(line) == delim {
			return next
		}
		i = next
	}
	return len(cmd)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// commandExes returns the main executable and every executable, in order.
func commandExes(cmd string) (string, []string) {
	segs := parseCommand(cmd)
	if len(segs) == 0 {
		return "", nil
	}
	exes := make([]string, len(segs))
	for i, s := range segs {
		exes[i] = s.exe
	}
	return exes[0], exes
}
