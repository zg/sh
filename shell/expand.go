package shell

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/zg/sh/parser"
)

// expandWord applies the expansions a word is eligible for, returning zero
// or more resulting fields.
//
// The order matters and mirrors a real shell: tilde, then variables, then
// (for unquoted words only) field splitting and globbing. A quoted word
// yields exactly one field no matter what it contains — which is the whole
// point of quoting.
func (s *Shell) expandWord(w parser.Word) []string {
	// Single quotes suppress every expansion.
	if w.Quoted == parser.SingleQuoted {
		return []string{w.Value}
	}

	value := w.Value
	if w.Quoted == parser.Unquoted {
		value = s.expandTilde(value)
	}
	value = s.expandVariables(value)

	if w.Quoted == parser.DoubleQuoted {
		// Still one field, even if the variable's value contains spaces.
		return []string{value}
	}

	fields := splitFields(value)
	var out []string
	for _, f := range fields {
		out = append(out, s.expandGlob(f)...)
	}
	// An unquoted variable that expanded to nothing contributes no field at
	// all, rather than an empty one.
	return out
}

// expandTilde replaces a leading ~ with the home directory.
func (s *Shell) expandTilde(v string) string {
	if !strings.HasPrefix(v, "~") {
		return v
	}
	if v == "~" || strings.HasPrefix(v, "~/") {
		home := s.Getenv("HOME")
		if home == "" {
			return v
		}
		return home + v[1:]
	}
	return v
}

// expandVariables replaces $NAME and ${NAME}. Unset variables expand to the
// empty string, as in a real shell.
func (s *Shell) expandVariables(v string) string {
	var sb strings.Builder
	i := 0
	for i < len(v) {
		if v[i] != '$' || i+1 >= len(v) {
			sb.WriteByte(v[i])
			i++
			continue
		}

		i++ // consume '$'
		if v[i] == '{' {
			end := strings.IndexByte(v[i:], '}')
			if end < 0 {
				// No closing brace: treat it literally rather than eating
				// the rest of the word.
				sb.WriteString("${")
				i++
				continue
			}
			name := v[i+1 : i+end]
			sb.WriteString(s.variable(name))
			i += end + 1
			continue
		}

		// $? is the previous exit status; $$ is the shell's pid.
		if v[i] == '?' {
			sb.WriteString(strconv.Itoa(s.LastStatus))
			i++
			continue
		}
		if v[i] == '$' {
			sb.WriteString(strconv.Itoa(os.Getpid()))
			i++
			continue
		}

		start := i
		for i < len(v) && isNameChar(v[i], i > start) {
			i++
		}
		if i == start {
			// A bare '$' not followed by a name stays literal.
			sb.WriteByte('$')
			continue
		}
		sb.WriteString(s.variable(v[start:i]))
	}
	return sb.String()
}

func isNameChar(c byte, notFirst bool) bool {
	if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
		return true
	}
	return notFirst && c >= '0' && c <= '9'
}

// splitFields splits on whitespace, dropping empty fields.
func splitFields(v string) []string {
	return strings.Fields(v)
}

// expandGlob expands a pattern against the filesystem. A pattern that
// matches nothing is returned unchanged, which is how most shells behave by
// default.
func (s *Shell) expandGlob(pattern string) []string {
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{pattern}
	}

	// Glob relative to the shell's working directory, which may differ
	// from the process's.
	abs := pattern
	if !filepath.IsAbs(pattern) {
		abs = filepath.Join(s.Dir, pattern)
	}
	matches, err := filepath.Glob(abs)
	if err != nil || len(matches) == 0 {
		return []string{pattern}
	}

	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if !filepath.IsAbs(pattern) {
			if rel, err := filepath.Rel(s.Dir, m); err == nil {
				m = rel
			}
		}
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// expandWordValue expands a raw value as if it were a word, quoted or not.
// It exists so tests can observe field splitting directly, which is
// otherwise only visible through a command's argument count.
func (s *Shell) expandWordValue(value string, quoted bool) []string {
	kind := parser.Unquoted
	if quoted {
		kind = parser.DoubleQuoted
	}
	return s.expandWord(parser.Word{Value: value, Quoted: kind})
}
