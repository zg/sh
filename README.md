# sh

[![ci](https://github.com/zg/sh/actions/workflows/ci.yml/badge.svg)](https://github.com/zg/sh/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/zg/sh.svg)](https://pkg.go.dev/github.com/zg/sh) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A shell: a user interface for access to an operating system's services.

Supports pipelines, redirection, `&&`/`||`/`;` lists, background jobs,
variables and expansion, globbing, quoting, and the builtins that have to
run in the shell's own process.

## Install

```sh
go install github.com/zg/sh/cmd/sh@latest
```

## Use

```sh
sh                    # interactive
sh script.sh          # run a script
sh -c 'echo hello'    # run one command
```

## Supported

| feature | example |
|---|---|
| pipelines | `ls \| grep foo \| wc -l` |
| redirection | `cmd < in > out 2>> err`, `>>` to append |
| lists | `a && b`, `a \|\| b`, `a; b` |
| background | `sleep 10 &`, then `jobs` |
| variables | `FOO=bar`, `$FOO`, `${FOO}`, `$?`, `$$` |
| scoped assignment | `FOO=bar cmd` sets it for `cmd` only |
| quoting | `'literal'`, `"$expanded"`, `a\ b` |
| globbing | `*.txt`, `?`, `[abc]` |
| tilde | `~`, `~/path` |
| comments | `echo hi # ignored` |

Builtins: `cd` (including `cd -`), `pwd`, `exit`, `export`, `unset`, `echo`,
`env`, `jobs`, `type`, `read`, `true`, `false`.

## Design notes

**Builtins run in-process by necessity**, not as an optimisation. `cd`,
`export` and `exit` change state that would be lost if they ran in a child —
a `cd` in a subprocess changes nothing the shell can observe.

**Working directory is per-shell, not per-process.** `Shell.Dir` is carried
explicitly and every relative path is resolved against it. That keeps a
shell instance self-contained and testable: tests run a shell rooted at a
temp directory without touching the process's own cwd.

**Quoting decides expansion, and only the value of an assignment is
quoted.** `FOO="a b c"` is an assignment even though the word contains a
quote — the quoting applies to the value, not the name. The first
implementation required the whole word to be unquoted and so treated this as
a command called `FOO=a b c`; the lexer now records whether an unquoted name
prefix preceded the first quote.

**Expansion order mirrors a real shell**: tilde, then variables, then — for
unquoted words only — field splitting and globbing. A quoted word yields
exactly one field regardless of its contents, which is the entire point of
quoting. An unquoted variable that expands to nothing contributes no field
at all rather than an empty one.

**Pipelines close their write ends.** Each stage runs in a goroutine and
closes its own pipe writer on exit; without that the next stage never sees
EOF and the pipeline deadlocks. A pipeline's exit status is its last
command's, as POSIX specifies.

**An unterminated quote means "keep reading".** The lexer reports it as a
distinct error type rather than a generic syntax error, so the REPL can
continue onto the next line instead of rejecting the input.

**`&&` and `||` skip whole chains.** `false && a && b` must not run `b`
either, so short-circuiting advances past every item joined by the same
operator rather than skipping only the next one.

## Tests

```sh
go test -race ./...
```

Tests that need external programs (`cat`, `tr`) are skipped on Windows.
