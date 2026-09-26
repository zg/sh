package parser

import "fmt"

// Parse builds a command list from input.
func Parse(input string) (*List, error) {
	tokens, err := Tokenize(input)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	return p.parseList()
}

type parser struct {
	tokens []Token
	pos    int
}

func (p *parser) cur() Token  { return p.tokens[p.pos] }
func (p *parser) next() Token { t := p.tokens[p.pos]; p.pos++; return t }

func (p *parser) parseList() (*List, error) {
	list := &List{}
	for p.cur().Type != TokenEOF {
		pipeline, err := p.parsePipeline()
		if err != nil {
			return nil, err
		}
		// An empty pipeline means a leading or doubled separator; skip it
		// rather than producing an empty command to execute.
		if len(pipeline.Commands) == 0 {
			if p.cur().Type == TokenEOF {
				break
			}
			p.next()
			continue
		}

		item := ListItem{Pipeline: pipeline, Next: OpNone}
		switch p.cur().Type {
		case TokenAnd:
			item.Next = OpAnd
			p.next()
		case TokenOr:
			item.Next = OpOr
			p.next()
		case TokenSemi:
			item.Next = OpSemi
			p.next()
		}
		list.Items = append(list.Items, item)
	}
	return list, nil
}

func (p *parser) parsePipeline() (*Pipeline, error) {
	pipeline := &Pipeline{}
	for {
		cmd, err := p.parseCommand()
		if err != nil {
			return nil, err
		}
		if cmd != nil {
			pipeline.Commands = append(pipeline.Commands, cmd)
		}

		if p.cur().Type == TokenPipe {
			p.next()
			// A pipe must be followed by a command.
			if p.cur().Type == TokenEOF {
				return nil, fmt.Errorf("syntax error: unexpected end of input after '|'")
			}
			continue
		}
		break
	}

	if p.cur().Type == TokenBackground {
		pipeline.Background = true
		p.next()
	}
	return pipeline, nil
}

func (p *parser) parseCommand() (*Command, error) {
	cmd := &Command{}
	for {
		switch t := p.cur(); t.Type {
		case TokenWord:
			p.next()
			// A leading NAME=value, before any command word, is an
			// assignment rather than an argument. The value may be quoted
			// (FOO="a b c") — only the *name* must be unquoted, which
			// NamePrefix records.
			if len(cmd.Words) == 0 && (t.Quoted == Unquoted || t.NamePrefix) && isAssignment(t.Value) {
				cmd.Assigns = append(cmd.Assigns, t.Value)
				continue
			}
			cmd.Words = append(cmd.Words, Word{Value: t.Value, Quoted: t.Quoted})

		case TokenRedirIn, TokenRedirOut, TokenRedirAppend, TokenRedirErr, TokenRedirErrApp:
			p.next()
			if p.cur().Type != TokenWord {
				return nil, fmt.Errorf("syntax error: expected a filename after %s", t.Type)
			}
			file := p.next()
			cmd.Redirects = append(cmd.Redirects, Redirect{Type: t.Type, File: file.Value})

		default:
			if len(cmd.Words) == 0 && len(cmd.Assigns) == 0 && len(cmd.Redirects) == 0 {
				return nil, nil
			}
			return cmd, nil
		}
	}
}

// isAssignment reports whether s looks like NAME=value with a valid name.
func isAssignment(s string) bool {
	for i := range len(s) {
		c := s[i]
		if c == '=' {
			return i > 0 // a leading '=' is not an assignment
		}
		isAlpha := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		isDigit := c >= '0' && c <= '9'
		if !isAlpha && !(isDigit && i > 0) {
			return false
		}
	}
	return false
}
