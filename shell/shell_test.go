package shell

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newTest returns a shell writing into buffers, rooted at a temp dir, with
// a minimal deterministic environment.
func newTest(t *testing.T) (*Shell, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	s := &Shell{
		Dir:    dir,
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		vars:   map[string]string{"PATH": os.Getenv("PATH"), "HOME": dir},
	}
	return s, &stdout, &stderr
}

func run(t *testing.T, s *Shell, line string) int {
	t.Helper()
	return s.Run(context.Background(), line)
}

func TestEcho(t *testing.T) {
	s, out, _ := newTest(t)
	if got := run(t, s, "echo hello world"); got != 0 {
		t.Errorf("status = %d", got)
	}
	if got := out.String(); got != "hello world\n" {
		t.Errorf("got %q", got)
	}
}

func TestEchoNoNewline(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "echo -n no newline")
	if got := out.String(); got != "no newline" {
		t.Errorf("got %q", got)
	}
}

func TestVariables(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "FOO=bar")
	run(t, s, "echo $FOO")
	if got := out.String(); got != "bar\n" {
		t.Errorf("got %q, want bar", got)
	}
}

func TestVariableBraceForm(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "FOO=bar")
	run(t, s, "echo ${FOO}baz")
	if got := out.String(); got != "barbaz\n" {
		t.Errorf("got %q, want barbaz", got)
	}
}

// An unset variable expands to nothing, not to its own name.
func TestUnsetVariableExpandsEmpty(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "echo [$NOPE]")
	if got := out.String(); got != "[]\n" {
		t.Errorf("got %q, want []", got)
	}
}

// $? must carry the previous command's exit status.
func TestExitStatusVariable(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "true")
	run(t, s, "echo $?")
	run(t, s, "false")
	run(t, s, "echo $?")
	if got := out.String(); got != "0\n1\n" {
		t.Errorf("got %q, want 0 then 1", got)
	}
}

// Single quotes suppress expansion; double quotes do not.
func TestQuotingAndExpansion(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "FOO=bar")
	run(t, s, `echo '$FOO' "$FOO"`)
	if got := out.String(); got != "$FOO bar\n" {
		t.Errorf("got %q, want '$FOO bar'", got)
	}
}

// A quoted variable containing spaces stays one argument.
func TestQuotedExpansionIsOneField(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, `FOO="a b c"`)
	run(t, s, `echo "[$FOO]"`)
	if got := out.String(); got != "[a b c]\n" {
		t.Errorf("got %q", got)
	}
	out.Reset()
	// Unquoted, it splits into three fields.
	run(t, s, `echo [$FOO]`)
	if got := out.String(); got != "[a b c]\n" {
		// echo rejoins with spaces, so the observable difference is in
		// argument count; check that separately below.
		t.Logf("unquoted expansion produced %q", got)
	}
}

func TestUnquotedExpansionSplitsFields(t *testing.T) {
	s, _, _ := newTest(t)
	s.Setenv("FOO", "a b c")
	got := s.expandWordValue("$FOO", false)
	if len(got) != 3 {
		t.Errorf("unquoted expansion produced %d fields, want 3: %v", len(got), got)
	}
	quoted := s.expandWordValue("$FOO", true)
	if len(quoted) != 1 {
		t.Errorf("quoted expansion produced %d fields, want 1: %v", len(quoted), quoted)
	}
}

func TestCdAndPwd(t *testing.T) {
	s, out, _ := newTest(t)
	sub := filepath.Join(s.Dir, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := run(t, s, "cd subdir"); got != 0 {
		t.Fatalf("cd failed: %d", got)
	}
	run(t, s, "pwd")
	// The temp dir may be a symlink (/var vs /private/var on macOS), so
	// compare the resolved paths.
	want, _ := filepath.EvalSymlinks(sub)
	got := strings.TrimSpace(out.String())
	if got != want {
		t.Errorf("pwd = %q, want %q", got, want)
	}
}

func TestCdErrors(t *testing.T) {
	s, _, errOut := newTest(t)
	if got := run(t, s, "cd /nonexistent/path/xyz"); got == 0 {
		t.Error("cd to a missing directory should fail")
	}
	if !strings.Contains(errOut.String(), "cd:") {
		t.Errorf("no error reported: %q", errOut.String())
	}
}

// cd - must return to the previous directory.
func TestCdDash(t *testing.T) {
	s, out, _ := newTest(t)
	start := s.Dir
	sub := filepath.Join(s.Dir, "sub")
	os.Mkdir(sub, 0o755)

	run(t, s, "cd sub")
	run(t, s, "cd -")
	run(t, s, "pwd")

	wantResolved, _ := filepath.EvalSymlinks(start)
	if got := strings.TrimSpace(out.String()); got != wantResolved && got != start {
		t.Errorf("cd - returned to %q, want %q", got, start)
	}
}

func TestExportAndUnset(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "export FOO=bar")
	if s.Getenv("FOO") != "bar" {
		t.Error("export did not set the variable")
	}
	run(t, s, "echo $FOO")
	if got := out.String(); got != "bar\n" {
		t.Errorf("got %q", got)
	}
	run(t, s, "unset FOO")
	if s.Getenv("FOO") != "" {
		t.Error("unset did not remove the variable")
	}
}

func TestExitBuiltin(t *testing.T) {
	s, _, _ := newTest(t)
	run(t, s, "exit 3")
	exit, code := s.ShouldExit()
	if !exit {
		t.Error("exit did not request termination")
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
}

func TestTrueFalse(t *testing.T) {
	s, _, _ := newTest(t)
	if got := run(t, s, "true"); got != 0 {
		t.Errorf("true returned %d", got)
	}
	if got := run(t, s, "false"); got != 1 {
		t.Errorf("false returned %d", got)
	}
}

// && and || must short-circuit on the previous status.
func TestAndOrShortCircuit(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"true && echo yes", "yes\n"},
		{"false && echo yes", ""},
		{"false || echo yes", "yes\n"},
		{"true || echo yes", ""},
		{"true && echo a && echo b", "a\nb\n"},
		{"false && echo a && echo b", ""},
		{"false || echo a || echo b", "a\n"},
		{"true; echo always", "always\n"},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			s, out, _ := newTest(t)
			run(t, s, tt.line)
			if got := out.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedirectOutput(t *testing.T) {
	s, _, _ := newTest(t)
	path := filepath.Join(s.Dir, "out.txt")

	run(t, s, "echo first > out.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first\n" {
		t.Errorf("got %q", string(data))
	}

	// > truncates.
	run(t, s, "echo second > out.txt")
	data, _ = os.ReadFile(path)
	if string(data) != "second\n" {
		t.Errorf("after truncating redirect, got %q", string(data))
	}

	// >> appends.
	run(t, s, "echo third >> out.txt")
	data, _ = os.ReadFile(path)
	if string(data) != "second\nthird\n" {
		t.Errorf("after appending redirect, got %q", string(data))
	}
}

func TestRedirectInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a unix cat")
	}
	s, out, _ := newTest(t)
	path := filepath.Join(s.Dir, "in.txt")
	os.WriteFile(path, []byte("file contents\n"), 0o644)

	run(t, s, "cat < in.txt")
	if got := out.String(); got != "file contents\n" {
		t.Errorf("got %q", got)
	}
}

func TestRedirectMissingInputFile(t *testing.T) {
	s, _, errOut := newTest(t)
	if got := run(t, s, "cat < nonexistent.txt"); got == 0 {
		t.Error("redirect from a missing file should fail")
	}
	if errOut.Len() == 0 {
		t.Error("no error reported")
	}
}

func TestPipeline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs unix tools")
	}
	s, out, _ := newTest(t)
	run(t, s, "echo hello | tr a-z A-Z")
	if got := strings.TrimSpace(out.String()); got != "HELLO" {
		t.Errorf("got %q, want HELLO", got)
	}
}

func TestLongPipeline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs unix tools")
	}
	s, out, _ := newTest(t)
	run(t, s, "echo -n abc | cat | cat | cat")
	if got := out.String(); got != "abc" {
		t.Errorf("got %q, want abc", got)
	}
}

// A pipeline's status is its last stage's.
func TestPipelineStatusIsLastCommand(t *testing.T) {
	s, _, _ := newTest(t)
	if got := run(t, s, "false | true"); got != 0 {
		t.Errorf("status = %d, want 0 (the last command's)", got)
	}
	if got := run(t, s, "true | false"); got != 1 {
		t.Errorf("status = %d, want 1 (the last command's)", got)
	}
}

func TestCommandNotFound(t *testing.T) {
	s, _, errOut := newTest(t)
	if got := run(t, s, "definitely_not_a_real_command_xyz"); got != 127 {
		t.Errorf("status = %d, want 127", got)
	}
	if !strings.Contains(errOut.String(), "not found") {
		t.Errorf("error = %q", errOut.String())
	}
}

func TestExternalCommandExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a unix shell")
	}
	s, _, _ := newTest(t)
	if got := run(t, s, "sh -c 'exit 42'"); got != 42 {
		t.Errorf("status = %d, want 42", got)
	}
}

// Assignments before a command apply to that command only.
func TestCommandScopedAssignment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a unix shell")
	}
	s, out, _ := newTest(t)
	run(t, s, `FOO=scoped sh -c 'echo $FOO'`)
	if got := strings.TrimSpace(out.String()); got != "scoped" {
		t.Errorf("got %q, want scoped", got)
	}
	// It must not leak into the shell.
	if s.Getenv("FOO") != "" {
		t.Errorf("assignment leaked into the shell: %q", s.Getenv("FOO"))
	}
}

func TestTildeExpansion(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "echo ~")
	if got := strings.TrimSpace(out.String()); got != s.Getenv("HOME") {
		t.Errorf("got %q, want %q", got, s.Getenv("HOME"))
	}
	out.Reset()
	run(t, s, "echo ~/sub")
	if got := strings.TrimSpace(out.String()); got != s.Getenv("HOME")+"/sub" {
		t.Errorf("got %q", got)
	}
}

// Tilde only expands at the start of a word.
func TestTildeOnlyAtWordStart(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "echo a~b")
	if got := strings.TrimSpace(out.String()); got != "a~b" {
		t.Errorf("got %q, want a~b", got)
	}
}

func TestGlobExpansion(t *testing.T) {
	s, out, _ := newTest(t)
	for _, name := range []string{"a.txt", "b.txt", "c.md"} {
		os.WriteFile(filepath.Join(s.Dir, name), nil, 0o644)
	}

	run(t, s, "echo *.txt")
	got := strings.Fields(strings.TrimSpace(out.String()))
	if len(got) != 2 || got[0] != "a.txt" || got[1] != "b.txt" {
		t.Errorf("got %v, want [a.txt b.txt]", got)
	}
}

// A glob matching nothing is left alone, as most shells do by default.
func TestGlobNoMatchIsLiteral(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "echo *.nomatch")
	if got := strings.TrimSpace(out.String()); got != "*.nomatch" {
		t.Errorf("got %q, want the pattern unchanged", got)
	}
}

// A quoted glob is not expanded.
func TestQuotedGlobNotExpanded(t *testing.T) {
	s, out, _ := newTest(t)
	os.WriteFile(filepath.Join(s.Dir, "a.txt"), nil, 0o644)
	run(t, s, `echo "*.txt"`)
	if got := strings.TrimSpace(out.String()); got != "*.txt" {
		t.Errorf("got %q, want the pattern unchanged", got)
	}
}

func TestComments(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "echo visible # hidden")
	if got := out.String(); got != "visible\n" {
		t.Errorf("got %q", got)
	}
}

func TestTypeBuiltin(t *testing.T) {
	s, out, _ := newTest(t)
	run(t, s, "type cd")
	if !strings.Contains(out.String(), "builtin") {
		t.Errorf("got %q", out.String())
	}
}

func TestBuiltinNamesSorted(t *testing.T) {
	names := BuiltinNames()
	if len(names) < 5 {
		t.Fatalf("got %d builtins", len(names))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("not sorted: %v", names)
		}
	}
}

func TestSyntaxErrorReported(t *testing.T) {
	s, _, errOut := newTest(t)
	if got := run(t, s, "echo >"); got == 0 {
		t.Error("a syntax error should be non-zero")
	}
	if !strings.Contains(errOut.String(), "sh:") {
		t.Errorf("error = %q", errOut.String())
	}
}

func TestEmptyInput(t *testing.T) {
	s, out, errOut := newTest(t)
	for _, line := range []string{"", "   ", "# comment"} {
		if got := run(t, s, line); got != 0 {
			t.Errorf("%q returned %d", line, got)
		}
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("empty input produced output: %q %q", out.String(), errOut.String())
	}
}

func TestEnvironIsSorted(t *testing.T) {
	s, _, _ := newTest(t)
	s.Setenv("ZZZ", "1")
	s.Setenv("AAA", "2")
	env := s.Environ()
	for i := 1; i < len(env); i++ {
		if env[i-1] > env[i] {
			t.Fatalf("Environ is not sorted: %v", env)
		}
	}
}

func TestReadBuiltin(t *testing.T) {
	s, _, _ := newTest(t)
	s.Stdin = strings.NewReader("a line of input\nsecond\n")
	if got := run(t, s, "read LINE"); got != 0 {
		t.Fatalf("read returned %d", got)
	}
	if got := s.Getenv("LINE"); got != "a line of input" {
		t.Errorf("got %q", got)
	}
}
