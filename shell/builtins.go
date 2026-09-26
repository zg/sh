package shell

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// builtinFunc is a command implemented inside the shell rather than by
// exec'ing a program.
type builtinFunc func(s *Shell, argv []string, stdin io.Reader, stdout, stderr io.Writer) int

// builtins must run in the shell's own process, because they change state
// that would be lost in a child: the working directory, variables, and
// whether to exit.
var builtins map[string]builtinFunc

func init() {
	builtins = map[string]builtinFunc{
		"cd":     builtinCd,
		"pwd":    builtinPwd,
		"exit":   builtinExit,
		"export": builtinExport,
		"unset":  builtinUnset,
		"echo":   builtinEcho,
		"env":    builtinEnv,
		"true":   func(*Shell, []string, io.Reader, io.Writer, io.Writer) int { return 0 },
		"false":  func(*Shell, []string, io.Reader, io.Writer, io.Writer) int { return 1 },
		"jobs":   builtinJobs,
		"type":   builtinType,
		"read":   builtinRead,
	}
}

// IsBuiltin reports whether name is handled internally.
func IsBuiltin(name string) bool {
	_, ok := builtins[name]
	return ok
}

// BuiltinNames returns the builtin names, sorted.
func BuiltinNames() []string {
	out := make([]string, 0, len(builtins))
	for k := range builtins {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func builtinCd(s *Shell, argv []string, _ io.Reader, _ io.Writer, stderr io.Writer) int {
	var target string
	switch {
	case len(argv) < 2:
		target = s.Getenv("HOME")
		if target == "" {
			fmt.Fprintln(stderr, "cd: HOME not set")
			return 1
		}
	case argv[1] == "-":
		// cd - returns to the previous directory.
		target = s.Getenv("OLDPWD")
		if target == "" {
			fmt.Fprintln(stderr, "cd: OLDPWD not set")
			return 1
		}
	default:
		target = argv[1]
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(s.Dir, target)
	}
	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(stderr, "cd: %s: %v\n", argv[len(argv)-1], err)
		return 1
	}
	if !info.IsDir() {
		fmt.Fprintf(stderr, "cd: %s: not a directory\n", argv[1])
		return 1
	}

	// Resolving symlinks keeps PWD consistent with what other tools report.
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	s.Setenv("OLDPWD", s.Dir)
	s.Dir = target
	s.Setenv("PWD", target)
	return 0
}

func builtinPwd(s *Shell, _ []string, _ io.Reader, stdout io.Writer, _ io.Writer) int {
	fmt.Fprintln(stdout, s.Dir)
	return 0
}

func builtinExit(s *Shell, argv []string, _ io.Reader, _ io.Writer, stderr io.Writer) int {
	code := s.LastStatus
	if len(argv) > 1 {
		n, err := strconv.Atoi(argv[1])
		if err != nil {
			fmt.Fprintf(stderr, "exit: %s: numeric argument required\n", argv[1])
			s.exit = true
			s.exitCode = 2
			return 2
		}
		code = n
	}
	s.exit = true
	s.exitCode = code
	return code
}

func builtinExport(s *Shell, argv []string, _ io.Reader, stdout io.Writer, _ io.Writer) int {
	if len(argv) == 1 {
		for _, kv := range s.Environ() {
			fmt.Fprintln(stdout, "export "+kv)
		}
		return 0
	}
	for _, arg := range argv[1:] {
		if k, v, ok := strings.Cut(arg, "="); ok {
			s.Setenv(k, v)
		}
		// `export NAME` with no value marks an existing variable; since
		// every variable here is already exported, there is nothing to do.
	}
	return 0
}

func builtinUnset(s *Shell, argv []string, _ io.Reader, _ io.Writer, _ io.Writer) int {
	for _, name := range argv[1:] {
		s.Unsetenv(name)
	}
	return 0
}

func builtinEcho(_ *Shell, argv []string, _ io.Reader, stdout io.Writer, _ io.Writer) int {
	args := argv[1:]
	newline := true
	if len(args) > 0 && args[0] == "-n" {
		newline = false
		args = args[1:]
	}
	fmt.Fprint(stdout, strings.Join(args, " "))
	if newline {
		fmt.Fprintln(stdout)
	}
	return 0
}

func builtinEnv(s *Shell, _ []string, _ io.Reader, stdout io.Writer, _ io.Writer) int {
	for _, kv := range s.Environ() {
		fmt.Fprintln(stdout, kv)
	}
	return 0
}

func builtinJobs(s *Shell, _ []string, _ io.Reader, stdout io.Writer, _ io.Writer) int {
	for _, j := range s.Jobs() {
		state := "running"
		if j.Done {
			state = "done"
		}
		fmt.Fprintf(stdout, "[%d] %-8s %s\n", j.ID, state, j.Cmd)
	}
	return 0
}

func builtinType(s *Shell, argv []string, _ io.Reader, stdout io.Writer, stderr io.Writer) int {
	status := 0
	for _, name := range argv[1:] {
		if IsBuiltin(name) {
			fmt.Fprintf(stdout, "%s is a shell builtin\n", name)
			continue
		}
		if path, err := s.lookPath(name); err == nil {
			fmt.Fprintf(stdout, "%s is %s\n", name, path)
			continue
		}
		fmt.Fprintf(stderr, "type: %s: not found\n", name)
		status = 1
	}
	return status
}

// builtinRead reads one line into a variable, which is what makes `read`
// useful in a script: the assignment must survive into the shell.
func builtinRead(s *Shell, argv []string, stdin io.Reader, _ io.Writer, _ io.Writer) int {
	name := "REPLY"
	if len(argv) > 1 {
		name = argv[1]
	}
	var sb strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := stdin.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			sb.WriteByte(buf[0])
		}
		if err != nil {
			if sb.Len() == 0 {
				return 1 // EOF with nothing read
			}
			break
		}
	}
	s.Setenv(name, sb.String())
	return 0
}

// lookPath finds an executable using the shell's PATH rather than the
// process's, so `PATH=... type foo` behaves as expected.
func (s *Shell) lookPath(name string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		p := s.resolve(name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
		return "", os.ErrNotExist
	}
	for _, dir := range filepath.SplitList(s.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}
