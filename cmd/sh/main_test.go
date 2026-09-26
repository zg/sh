package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/zg/sh/shell"
)

func testShell(t *testing.T) (*shell.Shell, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	sh := shell.New()
	sh.Dir = t.TempDir()
	sh.Stdout = &out
	sh.Stderr = &out
	return sh, &out
}

func TestRunScript(t *testing.T) {
	sh, out := testShell(t)
	script := strings.NewReader("echo one\necho two\necho three\n")
	if code := RunScript(context.Background(), sh, script); code != 0 {
		t.Errorf("exit code = %d", code)
	}
	if got := out.String(); got != "one\ntwo\nthree\n" {
		t.Errorf("got %q", got)
	}
}

// exit must stop the script immediately, not run the remaining lines.
func TestScriptStopsAtExit(t *testing.T) {
	sh, out := testShell(t)
	script := strings.NewReader("echo before\nexit 7\necho after\n")
	if code := RunScript(context.Background(), sh, script); code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	if strings.Contains(out.String(), "after") {
		t.Errorf("lines after exit were executed: %q", out.String())
	}
}

func TestREPLRunsCommands(t *testing.T) {
	sh, out := testShell(t)
	in := strings.NewReader("echo hello\nexit 0\n")
	if code := REPL(context.Background(), sh, in, out); code != 0 {
		t.Errorf("exit code = %d", code)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("got %q", out.String())
	}
}

// An unterminated quote must continue onto the next line rather than
// reporting a syntax error.
func TestREPLContinuesUnterminatedQuote(t *testing.T) {
	sh, out := testShell(t)
	in := strings.NewReader("echo \"first\nsecond\"\nexit 0\n")
	REPL(context.Background(), sh, in, out)

	got := out.String()
	if !strings.Contains(got, "first\nsecond") {
		t.Errorf("multi-line quote was not joined: %q", got)
	}
	if strings.Contains(got, "unterminated") {
		t.Errorf("reported a syntax error instead of continuing: %q", got)
	}
}

func TestREPLEndsAtEOF(t *testing.T) {
	sh, out := testShell(t)
	if code := REPL(context.Background(), sh, strings.NewReader("echo hi\n"), out); code != 0 {
		t.Errorf("exit code = %d, want 0 at EOF", code)
	}
}

func TestPromptShowsStatusAndTilde(t *testing.T) {
	sh, _ := testShell(t)
	sh.Setenv("HOME", sh.Dir)

	if got := sh.Prompt(); !strings.HasPrefix(got, "~") {
		t.Errorf("prompt = %q, want the home directory abbreviated", got)
	}
	sh.LastStatus = 3
	if got := sh.Prompt(); !strings.Contains(got, "[3]") {
		t.Errorf("prompt = %q, want the failing status shown", got)
	}
}
