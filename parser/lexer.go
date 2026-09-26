// Package parser turns shell input into a command tree.
package parser

import (
	"fmt"
	"strings"
)

// TokenType classifies a token.
type TokenType int

const (
	TokenWord        TokenType = iota
	TokenPipe                  // |
	TokenAnd                   // &&
	TokenOr                    // ||
	TokenSemi                  // ;
	TokenBackground            // &
	TokenRedirIn               // <
	TokenRedirOut              // >
	TokenRedirAppend           // >>
	TokenRedirErr              // 2>
	TokenRedirErrApp           // 2>>
	TokenEOF
)

func (t TokenType) String() string {
	switch t {
	case TokenWord:
		return "word"
	case TokenPipe:
		return "|"
	case TokenAnd:
		return "&&"
	case TokenOr:
		return "||"
	case TokenSemi:
		return ";"
	case TokenBackground:
		return "&"
	case TokenRedirIn:
		return "<"
	case TokenRedirOut:
		return ">"
	case TokenRedirAppend:
		return ">>"
	case TokenRedirErr:
		return "2>"
	case TokenRedirErrApp:
		return "2>>"
	default:
		return "EOF"
	}
}

// Token is a lexical token. Quoted records how the word was written, which
// decides whether it is later subject to expansion.
type Token struct {
	Type   TokenType
	Value  string
	Quoted QuoteKind
	// NamePrefix is true when the word begins with unquoted characters up
	// to an '=', as in FOO="a b c". The quoting there applies to the value,
	// not to the name, so such a word is still an assignment.
	NamePrefix bool
}

// QuoteKind records how a word was quoted.
type QuoteKind uint8

const (
	Unquoted QuoteKind = iota
	SingleQuoted
	DoubleQuoted
)

// ErrUnterminated reports an unclosed quote, which a shell should treat as
// "keep reading" rather than as a syntax error.
type ErrUnterminated struct{ Quote byte }

func (e *ErrUnterminated) Error() string {
	return fmt.Sprintf("unterminated %s quote", string(e.Quote))
}

// Tokenize splits input into tokens, honouring quoting and escapes.
func Tokenize(input string) ([]Token, error) {
	var tokens []Token
	i := 0

	for i < len(input) {
		c := input[i]

		// Whitespace separates tokens.
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}

		// A comment runs to end of line, but only when it starts a word.
		if c == '#' {
			for i < len(input) && input[i] != '\n' {
				i++
			}
			continue
		}

		// Operators, longest match first so ">>" is not read as two ">".
		if tok, width := matchOperator(input[i:]); width > 0 {
			tokens = append(tokens, tok)
			i += width
			continue
		}

		word, quoted, namePrefix, next, err := readWord(input, i)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, Token{
			Type: TokenWord, Value: word, Quoted: quoted, NamePrefix: namePrefix,
		})
		i = next
	}

	tokens = append(tokens, Token{Type: TokenEOF})
	return tokens, nil
}

func matchOperator(s string) (Token, int) {
	switch {
	case strings.HasPrefix(s, "2>>"):
		return Token{Type: TokenRedirErrApp, Value: "2>>"}, 3
	case strings.HasPrefix(s, "2>"):
		return Token{Type: TokenRedirErr, Value: "2>"}, 2
	case strings.HasPrefix(s, "&&"):
		return Token{Type: TokenAnd, Value: "&&"}, 2
	case strings.HasPrefix(s, "||"):
		return Token{Type: TokenOr, Value: "||"}, 2
	case strings.HasPrefix(s, ">>"):
		return Token{Type: TokenRedirAppend, Value: ">>"}, 2
	case strings.HasPrefix(s, "|"):
		return Token{Type: TokenPipe, Value: "|"}, 1
	case strings.HasPrefix(s, "&"):
		return Token{Type: TokenBackground, Value: "&"}, 1
	case strings.HasPrefix(s, ";"):
		return Token{Type: TokenSemi, Value: ";"}, 1
	case strings.HasPrefix(s, "<"):
		return Token{Type: TokenRedirIn, Value: "<"}, 1
	case strings.HasPrefix(s, ">"):
		return Token{Type: TokenRedirOut, Value: ">"}, 1
	}
	return Token{}, 0
}

// readWord reads one word starting at i, handling the three quoting forms.
//
// A word can mix them — foo"bar"'baz' is one word — so the quote kind
// returned is the strongest quoting seen, which is what decides whether the
// result is expanded later.
func readWord(input string, i int) (string, QuoteKind, bool, int, error) {
	var sb strings.Builder
	quoted := Unquoted
	// sawQuote records whether any quoting has been seen yet, so an '='
	// reached before the first quote marks an unquoted name prefix.
	sawQuote := false
	namePrefix := false

	for i < len(input) {
		c := input[i]

		if isWordBreak(input[i:]) {
			break
		}

		switch c {
		case '\'':
			// Single quotes are literal: nothing inside is special, not
			// even a backslash.
			sawQuote = true
			end := strings.IndexByte(input[i+1:], '\'')
			if end < 0 {
				return "", quoted, namePrefix, i, &ErrUnterminated{Quote: '\''}
			}
			sb.WriteString(input[i+1 : i+1+end])
			if quoted == Unquoted {
				quoted = SingleQuoted
			}
			i += end + 2

		case '"':
			// Double quotes keep spaces but still allow escapes and, later,
			// variable expansion.
			sawQuote = true
			j := i + 1
			closed := false
			for j < len(input) {
				if input[j] == '\\' && j+1 < len(input) {
					sb.WriteByte(input[j+1])
					j += 2
					continue
				}
				if input[j] == '"' {
					closed = true
					break
				}
				sb.WriteByte(input[j])
				j++
			}
			if !closed {
				return "", quoted, namePrefix, i, &ErrUnterminated{Quote: '"'}
			}
			quoted = DoubleQuoted
			i = j + 1

		case '\\':
			if i+1 < len(input) {
				sb.WriteByte(input[i+1])
				i += 2
			} else {
				i++
			}

		default:
			if c == '=' && !sawQuote && sb.Len() > 0 && isName(sb.String()) {
				namePrefix = true
			}
			sb.WriteByte(c)
			i++
		}
	}
	return sb.String(), quoted, namePrefix, i, nil
}

// isName reports whether s is a valid shell variable name.
func isName(s string) bool {
	for i := range len(s) {
		c := s[i]
		isAlpha := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		isDigit := c >= '0' && c <= '9'
		if !isAlpha && !(isDigit && i > 0) {
			return false
		}
	}
	return s != ""
}

// isWordBreak reports whether a word ends here: at whitespace or at the
// start of an operator.
func isWordBreak(s string) bool {
	if s == "" {
		return true
	}
	switch s[0] {
	case ' ', '\t', '\n', '\r':
		return true
	}
	_, width := matchOperator(s)
	return width > 0
}
