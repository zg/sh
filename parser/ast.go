package parser

import "strings"

// Redirect describes one redirection on a command.
type Redirect struct {
	Type TokenType // TokenRedirIn, TokenRedirOut, TokenRedirAppend, TokenRedirErr, TokenRedirErrApp
	File string
}

// Word is a single argument, carrying how it was quoted so expansion can
// decide whether to apply.
type Word struct {
	Value  string
	Quoted QuoteKind
}

// Command is a simple command: a name, arguments, redirections and any
// leading VAR=value assignments.
type Command struct {
	Words     []Word
	Redirects []Redirect
	Assigns   []string // NAME=value pairs preceding the command name
}

// Pipeline is one or more commands joined by pipes.
type Pipeline struct {
	Commands   []*Command
	Background bool
}

// Operator joins pipelines in a list.
type Operator uint8

const (
	OpNone Operator = iota
	OpSemi
	OpAnd // &&
	OpOr  // ||
)

// ListItem is a pipeline plus the operator that connects it to the next.
type ListItem struct {
	Pipeline *Pipeline
	Next     Operator
}

// List is a whole line: pipelines joined by ;, && and ||.
type List struct{ Items []ListItem }

func (c *Command) String() string {
	parts := make([]string, 0, len(c.Assigns)+len(c.Words))
	parts = append(parts, c.Assigns...)
	for _, w := range c.Words {
		parts = append(parts, w.Value)
	}
	s := strings.Join(parts, " ")
	for _, r := range c.Redirects {
		s += " " + r.Type.String() + " " + r.File
	}
	return s
}

func (p *Pipeline) String() string {
	parts := make([]string, len(p.Commands))
	for i, c := range p.Commands {
		parts[i] = c.String()
	}
	s := strings.Join(parts, " | ")
	if p.Background {
		s += " &"
	}
	return s
}

func (l *List) String() string {
	var sb strings.Builder
	for i, item := range l.Items {
		if i > 0 {
			switch l.Items[i-1].Next {
			case OpAnd:
				sb.WriteString(" && ")
			case OpOr:
				sb.WriteString(" || ")
			default:
				sb.WriteString("; ")
			}
		}
		sb.WriteString(item.Pipeline.String())
	}
	return sb.String()
}
