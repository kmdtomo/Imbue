package redact

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

type Redactor struct {
	patterns []*regexp.Regexp
	paths    []string
}

func New(patterns, paths []string) (*Redactor, error) {
	rules := []string{
		`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`,
		`\b(?:sk-[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[A-Z0-9]{16}|xox[baprs]-[A-Za-z0-9-]{10,})\b`,
		`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`,
		`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9_./+=-]{6,}`,
		`(?i)(?:password|passwd|api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|authorization)\s*["']?\s*[:=]\s*["']?[^\s"',;}]+`,
		`(?i)\b[a-z][a-z0-9+.-]*://[^\s/:]+:[^\s/@]+@`,
		`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`,
	}
	r := &Redactor{paths: paths}
	for _, s := range append(rules, patterns...) {
		p, e := regexp.Compile(s)
		if e != nil {
			return nil, e
		}
		r.patterns = append(r.patterns, p)
	}
	return r, nil
}
func (r *Redactor) Text(s string) string {
	for _, p := range r.patterns {
		s = p.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}
func (r *Redactor) Excluded(s string) bool {
	for _, token := range strings.FieldsFunc(s, func(c rune) bool { return strings.ContainsRune(" \n\t\r\"'`()[]{}:,;=", c) }) {
		token = strings.Trim(token, "<>+")
		for _, p := range r.paths {
			if ok, _ := filepath.Match(p, filepath.Base(token)); ok {
				return true
			}
			if strings.Contains(p, "/") && strings.Contains(token, p) {
				return true
			}
		}
	}
	return false
}
func (r *Redactor) JSON(b []byte) ([]byte, error) {
	var v any
	if e := json.Unmarshal(b, &v); e != nil {
		return nil, e
	}
	return json.Marshal(r.walk(v))
}

var secretKey = regexp.MustCompile(`(?i)^(password|passwd|api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|authorization|cookie|set-cookie|encrypted_content)$`)

func (r *Redactor) walk(v any) any {
	switch x := v.(type) {
	case string:
		return r.Text(x)
	case []any:
		for i := range x {
			x[i] = r.walk(x[i])
		}
	case map[string]any:
		for k, b := range x {
			if secretKey.MatchString(k) {
				x[k] = "[REDACTED]"
			} else {
				x[k] = r.walk(b)
			}
		}
	}
	return v
}
