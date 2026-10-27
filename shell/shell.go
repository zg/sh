// Package shell executes parsed commands: a user interface for access to an
// operating system's services.
package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/zg/sh/parser"
)

// Shell holds the state a shell needs between commands.
type Shell struct {
	Dir        string // working directory, kept per-shell rather than per-process
	LastStatus int
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer

	mu   sync.RWMutex
	vars map[string]string

	// exit is set by the exit builtin; the REPL stops when it is true.
	exit     bool
	exitCode int

	jobs   []*Job
	jobsMu sync.Mutex
}

// Job is a background pipeline.
type Job struct {
	ID   int
	Cmd  string
	Proc *os.Process
	Done bool
}

// New returns a shell with the process's environment and working directory.
func New() *Shell {
	dir, err := os.Getwd()
	if err != nil {
		dir = "/"
	}
	s := &Shell{
		Dir:    dir,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		vars:   map[string]string{},
	}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			s.vars[k] = v
		}
	}
	return s
}

// Getenv reads a variable.
func (s *Shell) Getenv(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vars[name]
}

// Setenv sets a variable.
func (s *Shell) Setenv(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vars[name] = value
}

// Unsetenv removes a variable.
func (s *Shell) Unsetenv(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.vars, name)
}

// Environ returns the variables as KEY=value, sorted for determinism.
func (s *Shell) Environ() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.vars))
	for k, v := range s.vars {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func (s *Shell) variable(name string) string { return s.Getenv(name) }

// ShouldExit reports whether the exit builtin has run, and with what code.
func (s *Shell) ShouldExit() (bool, int) { return s.exit, s.exitCode }

// Run parses and executes a line, returning the final exit status.
func (s *Shell) Run(ctx context.Context, input string) int {
	list, err := parser.Parse(input)
	if err != nil {
		fmt.Fprintln(s.Stderr, "sh:", err)
		s.LastStatus = 2
		return 2
	}
	return s.RunList(ctx, list)
}

// RunList executes a parsed list, honouring && and || short-circuiting.
func (s *Shell) RunList(ctx context.Context, list *parser.List) int {
	status := 0
	for i := 0; i < len(list.Items); i++ {
		item := list.Items[i]
		status = s.runPipeline(ctx, item.Pipeline)
		s.LastStatus = status

		if s.exit {
			return status
		}

		// && runs the next only on success; || only on failure. Skipping
		// must respect chains, so skip until an item whose predecessor's
		// operator admits the current status.
		switch item.Next {
		case parser.OpAnd:
			if status != 0 {
				i = skipChain(list, i, parser.OpAnd)
			}
		case parser.OpOr:
			if status == 0 {
				i = skipChain(list, i, parser.OpOr)
			}
		}
	}
	return status
}

// skipChain advances past the items that the given operator short-circuits.
func skipChain(list *parser.List, i int, op parser.Operator) int {
	for i < len(list.Items)-1 && list.Items[i].Next == op {
		i++
	}
	return i
}

func (s *Shell) runPipeline(ctx context.Context, p *parser.Pipeline) int {
	if len(p.Commands) == 0 {
		return 0
	}
	if len(p.Commands) == 1 {
		return s.runSingle(ctx, p.Commands[0], p.Background)
	}
	return s.runPiped(ctx, p)
}

// runSingle executes one command, which may be a builtin.
func (s *Shell) runSingle(ctx context.Context, c *parser.Command, background bool) int {
	argv := s.expandCommand(c)

	// A command that is only assignments sets shell variables.
	if len(argv) == 0 {
		for _, a := range c.Assigns {
			if k, v, ok := strings.Cut(a, "="); ok {
				s.Setenv(k, s.expandVariables(v))
			}
		}
		return 0
	}

	stdin, stdout, stderr, cleanup, err := s.applyRedirects(c)
	if err != nil {
		fmt.Fprintln(s.Stderr, "sh:", err)
		return 1
	}
	defer cleanup()

	if fn, ok := builtins[argv[0]]; ok {
		return fn(s, argv, stdin, stdout, stderr)
	}
	return s.runExternal(ctx, c, argv, stdin, stdout, stderr, background)
}

func (s *Shell) expandCommand(c *parser.Command) []string {
	var argv []string
	for _, w := range c.Words {
		argv = append(argv, s.expandWord(w)...)
	}
	return argv
}

// applyRedirects opens the files a command redirects to, returning the
// streams to use and a cleanup function.
func (s *Shell) applyRedirects(c *parser.Command) (io.Reader, io.Writer, io.Writer, func(), error) {
	stdin, stdout, stderr := s.Stdin, s.Stdout, s.Stderr
	var toClose []io.Closer
	cleanup := func() {
		for _, f := range toClose {
			f.Close()
		}
	}

	for _, r := range c.Redirects {
		// The filename is itself subject to expansion.
		names := s.expandWord(parser.Word{Value: r.File, Quoted: parser.Unquoted})
		if len(names) != 1 {
			cleanup()
			return nil, nil, nil, func() {}, fmt.Errorf("ambiguous redirect: %s", r.File)
		}
		path := s.resolve(names[0])

		switch r.Type {
		case parser.TokenRedirIn:
			f, err := os.Open(path)
			if err != nil {
				cleanup()
				return nil, nil, nil, func() {}, err
			}
			toClose = append(toClose, f)
			stdin = f

		case parser.TokenRedirOut, parser.TokenRedirErr:
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
			if err != nil {
				cleanup()
				return nil, nil, nil, func() {}, err
			}
			toClose = append(toClose, f)
			if r.Type == parser.TokenRedirOut {
				stdout = f
			} else {
				stderr = f
			}

		case parser.TokenRedirAppend, parser.TokenRedirErrApp:
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
			if err != nil {
				cleanup()
				return nil, nil, nil, func() {}, err
			}
			toClose = append(toClose, f)
			if r.Type == parser.TokenRedirAppend {
				stdout = f
			} else {
				stderr = f
			}
		}
	}
	return stdin, stdout, stderr, cleanup, nil
}

// resolve makes a path absolute relative to the shell's directory, which is
// not necessarily the process's.
func (s *Shell) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(s.Dir, path)
}

func (s *Shell) runExternal(ctx context.Context, c *parser.Command, argv []string,
	stdin io.Reader, stdout, stderr io.Writer, background bool) int {

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = s.Dir
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// Assignments preceding a command apply to that command only, not to
	// the shell.
	env := s.Environ()
	for _, a := range c.Assigns {
		if k, v, ok := strings.Cut(a, "="); ok {
			env = append(env, k+"="+s.expandVariables(v))
		}
	}
	cmd.Env = env

	if background {
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(s.Stderr, "sh: %s: %v\n", argv[0], err)
			return 127
		}
		job := s.addJob(strings.Join(argv, " "), cmd.Process)
		fmt.Fprintf(s.Stderr, "[%d] %d\n", job.ID, cmd.Process.Pid)
		// Reap asynchronously so a finished background job does not linger
		// as a zombie.
		go func() {
			cmd.Wait()
			s.markDone(job)
		}()
		return 0
	}

	if err := cmd.Run(); err != nil {
		return exitStatus(err, s.Stderr, argv[0])
	}
	return 0
}

// runPiped wires a pipeline together and runs every stage concurrently.
func (s *Shell) runPiped(ctx context.Context, p *parser.Pipeline) int {
	n := len(p.Commands)
	// Each stage's stdout is the next stage's stdin.
	readers := make([]io.Reader, n)
	writers := make([]io.Writer, n)
	var closers []io.Closer

	readers[0] = s.Stdin
	writers[n-1] = s.Stdout
	for i := range n - 1 {
		pr, pw := io.Pipe()
		writers[i] = pw
		readers[i+1] = pr
		closers = append(closers, pw, pr)
	}

	statuses := make([]int, n)
	// Every stage reports errors to the same stream, so serialise writes to it.
	stderr := &syncWriter{w: s.Stderr}
	var wg sync.WaitGroup
	for i, c := range p.Commands {
		wg.Add(1)
		go func(i int, c *parser.Command) {
			defer wg.Done()
			// Closing this stage's write end is what signals EOF to the
			// next stage; without it the pipeline deadlocks.
			if pw, ok := writers[i].(*io.PipeWriter); ok {
				defer pw.Close()
			}
			sub := s.forStage(readers[i], writers[i])
			sub.Stderr = stderr
			statuses[i] = sub.runSingle(ctx, c, false)
		}(i, c)
	}
	wg.Wait()

	for _, c := range closers {
		c.Close()
	}
	// A pipeline's status is its last command's, as in POSIX shells.
	return statuses[n-1]
}

// syncWriter serialises writes so concurrent pipeline stages can share a
// single destination, such as a bytes.Buffer, without a data race.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// forStage returns a shallow shell sharing variables but with its own
// streams, for one stage of a pipeline.
func (s *Shell) forStage(stdin io.Reader, stdout io.Writer) *Shell {
	sub := &Shell{
		Dir:        s.Dir,
		LastStatus: s.LastStatus,
		Stdin:      stdin,
		Stdout:     stdout,
		Stderr:     s.Stderr,
		vars:       s.vars, // shared map: a pipeline stage sees the same variables
	}
	return sub
}

func exitStatus(err error, stderr io.Writer, name string) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if errors.Is(err, exec.ErrNotFound) || os.IsNotExist(err) {
		fmt.Fprintf(stderr, "sh: %s: command not found\n", name)
		return 127
	}
	fmt.Fprintf(stderr, "sh: %s: %v\n", name, err)
	return 126
}

func (s *Shell) addJob(cmd string, proc *os.Process) *Job {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	job := &Job{ID: len(s.jobs) + 1, Cmd: cmd, Proc: proc}
	s.jobs = append(s.jobs, job)
	return job
}

func (s *Shell) markDone(job *Job) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	job.Done = true
}

// Jobs returns a snapshot of background jobs.
func (s *Shell) Jobs() []Job {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	return out
}

// Prompt returns the interactive prompt, showing the working directory with
// the home directory abbreviated to ~.
func (s *Shell) Prompt() string {
	dir := s.Dir
	if home := s.Getenv("HOME"); home != "" && strings.HasPrefix(dir, home) {
		dir = "~" + dir[len(home):]
	}
	status := ""
	if s.LastStatus != 0 {
		status = fmt.Sprintf("[%d] ", s.LastStatus)
	}
	return fmt.Sprintf("%s%s $ ", status, dir)
}
