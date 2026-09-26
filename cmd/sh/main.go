// Command sh is a shell: a user interface for access to an operating
// system's services.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/zg/sh/parser"
	"github.com/zg/sh/shell"
)

func main() {
	c := flag.String("c", "", "run the given command and exit")
	flag.Parse()

	// Ctrl-C must interrupt the running command, not kill the shell.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	sh := shell.New()

	if *c != "" {
		status := sh.Run(ctx, *c)
		os.Exit(status)
	}

	if args := flag.Args(); len(args) > 0 {
		f, err := os.Open(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "sh:", err)
			os.Exit(1)
		}
		defer f.Close()
		os.Exit(RunScript(ctx, sh, f))
	}

	os.Exit(REPL(ctx, sh, os.Stdin, os.Stdout))
}

// RunScript executes every line of r, stopping at the first exit.
func RunScript(ctx context.Context, sh *shell.Shell, r io.Reader) int {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	status := 0
	for scanner.Scan() {
		status = sh.Run(ctx, scanner.Text())
		if exit, code := sh.ShouldExit(); exit {
			return code
		}
	}
	return status
}

// REPL runs the interactive loop.
func REPL(ctx context.Context, sh *shell.Shell, in io.Reader, out io.Writer) int {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// pending accumulates a command split across lines by an unterminated
	// quote, which is what makes multi-line input work.
	var pending strings.Builder

	for {
		if pending.Len() == 0 {
			fmt.Fprint(out, sh.Prompt())
		} else {
			fmt.Fprint(out, "> ")
		}

		if !scanner.Scan() {
			fmt.Fprintln(out)
			return 0
		}
		line := scanner.Text()

		if pending.Len() > 0 {
			pending.WriteByte('\n')
		}
		pending.WriteString(line)
		input := pending.String()

		// An unterminated quote means the command is incomplete; keep
		// reading rather than reporting a syntax error.
		if _, err := parser.Tokenize(input); err != nil {
			var ue *parser.ErrUnterminated
			if asUnterminated(err, &ue) {
				continue
			}
		}
		pending.Reset()

		sh.Run(ctx, input)
		if exit, code := sh.ShouldExit(); exit {
			return code
		}
	}
}

func asUnterminated(err error, target **parser.ErrUnterminated) bool {
	if e, ok := err.(*parser.ErrUnterminated); ok {
		*target = e
		return true
	}
	return false
}
