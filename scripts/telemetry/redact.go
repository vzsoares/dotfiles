package main

import "regexp"

const redacted = "[REDACTED]"

type redaction struct {
	re   *regexp.Regexp
	repl string
}

// Order matters: assignments and flags run before the bare-token patterns so
// the key name survives and only the value is replaced.
var redactions = []redaction{
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@'"]+:[^/\s@'"]+@`), "${1}" + redacted + "@"},
	{regexp.MustCompile(`(?i)(authorization:\s*)[^"'\n]+`), "${1}" + redacted},
	{regexp.MustCompile(`(?i)\b([a-z-]*(?:api-?key|token|secret|password)[a-z-]*:\s*)[^\s"']+`), "${1}" + redacted},
	{regexp.MustCompile(`(?i)("[a-z0-9_-]*(?:token|secret|password|passwd|key)[a-z0-9_-]*"\s*:\s*)"[^"]*"`), "${1}\"" + redacted + "\""},
	{regexp.MustCompile(`(?i)\b(bearer\s+)[a-z0-9._~+/=-]+`), "${1}" + redacted},
	{regexp.MustCompile(`(?i)\b([a-z0-9_]*(?:token|secret|password|passwd|key)[a-z0-9_]*\s*=\s*)("[^"]*"|'[^']*'|[^\s;&|]+)`), "${1}" + redacted},
	{regexp.MustCompile(`(?i)(--?(?:password|passwd|pass|token|secret|api-?key)(?:=|\s+))("[^"]*"|'[^']*'|[^\s;&|]+)`), "${1}" + redacted},
	{regexp.MustCompile(`(\s-u\s+)[^\s:'"]+:[^\s'"]+`), "${1}" + redacted},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), redacted},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`), redacted},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), redacted},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`), redacted},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), redacted},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`), redacted},
}

func redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// truncate cuts to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
