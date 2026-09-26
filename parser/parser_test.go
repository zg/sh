package parser

import "testing"

func TestTokenizeBasic(t *testing.T) {
	tokens, err := Tokenize("ls -la /tmp")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ls", "-la", "/tmp"}
	if len(tokens) != len(want)+1 { // +1 for EOF
		t.Fatalf("got %d tokens, want %d", len(tokens), len(want)+1)
	}
	for i, w := range want {
		if tokens[i].Type != TokenWord || tokens[i].Value != w {
			t.Errorf("token %d = %v %q, want word %q", i, tokens[i].Type, tokens[i].Value, w)
		}
	}
}

func TestTokenizeOperators(t *testing.T) {
	tests := []struct {
		in   string
		want []TokenType
	}{
		{"a | b", []TokenType{TokenWord, TokenPipe, TokenWord, TokenEOF}},
		{"a && b", []TokenType{TokenWord, TokenAnd, TokenWord, TokenEOF}},
		{"a || b", []TokenType{TokenWord, TokenOr, TokenWord, TokenEOF}},
		{"a ; b", []TokenType{TokenWord, TokenSemi, TokenWord, TokenEOF}},
		{"a &", []TokenType{TokenWord, TokenBackground, TokenEOF}},
		{"a > f", []TokenType{TokenWord, TokenRedirOut, TokenWord, TokenEOF}},
		{"a >> f", []TokenType{TokenWord, TokenRedirAppend, TokenWord, TokenEOF}},
		{"a < f", []TokenType{TokenWord, TokenRedirIn, TokenWord, TokenEOF}},
		{"a 2> f", []TokenType{TokenWord, TokenRedirErr, TokenWord, TokenEOF}},
		{"a 2>> f", []TokenType{TokenWord, TokenRedirErrApp, TokenWord, TokenEOF}},
		// Operators need no surrounding whitespace.
		{"a|b", []TokenType{TokenWord, TokenPipe, TokenWord, TokenEOF}},
		{"a>f", []TokenType{TokenWord, TokenRedirOut, TokenWord, TokenEOF}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			tokens, err := Tokenize(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(tokens) != len(tt.want) {
				t.Fatalf("got %d tokens, want %d: %v", len(tokens), len(tt.want), tokens)
			}
			for i, w := range tt.want {
				if tokens[i].Type != w {
					t.Errorf("token %d = %v, want %v", i, tokens[i].Type, w)
				}
			}
		})
	}
}

// Longest-match matters: ">>" must not read as two ">".
func TestOperatorLongestMatch(t *testing.T) {
	tokens, _ := Tokenize("a >> b")
	if tokens[1].Type != TokenRedirAppend {
		t.Errorf("got %v, want >>", tokens[1].Type)
	}
	tokens, _ = Tokenize("a && b")
	if tokens[1].Type != TokenAnd {
		t.Errorf("got %v, want &&", tokens[1].Type)
	}
}

func TestQuoting(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		quoted QuoteKind
	}{
		{`'single quoted'`, "single quoted", SingleQuoted},
		{`"double quoted"`, "double quoted", DoubleQuoted},
		{`no_quotes`, "no_quotes", Unquoted},
		{`"with \"escape\""`, `with "escape"`, DoubleQuoted},
		{`a\ b`, "a b", Unquoted},
		{`'$notexpanded'`, "$notexpanded", SingleQuoted},
		// Quoting forms concatenate into one word.
		{`foo"bar"'baz'`, "foobarbaz", DoubleQuoted},
		{`''`, "", SingleQuoted},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			tokens, err := Tokenize(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if tokens[0].Value != tt.want {
				t.Errorf("value = %q, want %q", tokens[0].Value, tt.want)
			}
			if tokens[0].Quoted != tt.quoted {
				t.Errorf("quoted = %v, want %v", tokens[0].Quoted, tt.quoted)
			}
		})
	}
}

// Quoted whitespace must not split the word — the entire point of quoting.
func TestQuotedWhitespaceStaysOneWord(t *testing.T) {
	for _, in := range []string{`echo "a b c"`, `echo 'a b c'`, `echo a\ b\ c`} {
		tokens, err := Tokenize(in)
		if err != nil {
			t.Fatal(err)
		}
		if len(tokens) != 3 { // echo, the word, EOF
			t.Errorf("%s produced %d tokens, want 3: %v", in, len(tokens), tokens)
		}
	}
}

// An unterminated quote is a distinct condition: a real shell keeps reading
// rather than reporting a syntax error.
func TestUnterminatedQuote(t *testing.T) {
	for _, in := range []string{`echo "unterminated`, `echo 'unterminated`} {
		_, err := Tokenize(in)
		if err == nil {
			t.Fatalf("%s should report an unterminated quote", in)
		}
		var ue *ErrUnterminated
		if !asUnterminated(err, &ue) {
			t.Errorf("got %T, want *ErrUnterminated", err)
		}
	}
}

func asUnterminated(err error, target **ErrUnterminated) bool {
	if e, ok := err.(*ErrUnterminated); ok {
		*target = e
		return true
	}
	return false
}

func TestComments(t *testing.T) {
	tokens, err := Tokenize("echo hi # this is a comment")
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 3 {
		t.Errorf("got %d tokens, want 3 (echo, hi, EOF): %v", len(tokens), tokens)
	}
}

func TestParsePipeline(t *testing.T) {
	list, err := Parse("ls -la | grep foo | wc -l")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(list.Items))
	}
	if n := len(list.Items[0].Pipeline.Commands); n != 3 {
		t.Fatalf("got %d pipeline stages, want 3", n)
	}
}

func TestParseRedirects(t *testing.T) {
	list, err := Parse("cmd < in.txt > out.txt 2>> err.txt")
	if err != nil {
		t.Fatal(err)
	}
	cmd := list.Items[0].Pipeline.Commands[0]
	if len(cmd.Redirects) != 3 {
		t.Fatalf("got %d redirects, want 3", len(cmd.Redirects))
	}
	want := []struct {
		typ  TokenType
		file string
	}{
		{TokenRedirIn, "in.txt"},
		{TokenRedirOut, "out.txt"},
		{TokenRedirErrApp, "err.txt"},
	}
	for i, w := range want {
		if cmd.Redirects[i].Type != w.typ || cmd.Redirects[i].File != w.file {
			t.Errorf("redirect %d = %v %q, want %v %q",
				i, cmd.Redirects[i].Type, cmd.Redirects[i].File, w.typ, w.file)
		}
	}
}

func TestParseOperators(t *testing.T) {
	list, err := Parse("a && b || c ; d")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 4 {
		t.Fatalf("got %d items, want 4", len(list.Items))
	}
	wants := []Operator{OpAnd, OpOr, OpSemi, OpNone}
	for i, w := range wants {
		if list.Items[i].Next != w {
			t.Errorf("item %d operator = %v, want %v", i, list.Items[i].Next, w)
		}
	}
}

func TestParseBackground(t *testing.T) {
	list, err := Parse("sleep 10 &")
	if err != nil {
		t.Fatal(err)
	}
	if !list.Items[0].Pipeline.Background {
		t.Error("pipeline should be marked background")
	}
}

func TestParseAssignments(t *testing.T) {
	list, err := Parse("FOO=bar BAZ=qux cmd arg")
	if err != nil {
		t.Fatal(err)
	}
	cmd := list.Items[0].Pipeline.Commands[0]
	if len(cmd.Assigns) != 2 {
		t.Fatalf("got %d assignments, want 2: %v", len(cmd.Assigns), cmd.Assigns)
	}
	if len(cmd.Words) != 2 || cmd.Words[0].Value != "cmd" {
		t.Errorf("words = %v, want [cmd arg]", cmd.Words)
	}
}

// An = after the command name is an ordinary argument, not an assignment.
func TestAssignmentOnlyBeforeCommandName(t *testing.T) {
	list, _ := Parse("cmd FOO=bar")
	cmd := list.Items[0].Pipeline.Commands[0]
	if len(cmd.Assigns) != 0 {
		t.Errorf("FOO=bar after the command name should be an argument, got assigns %v", cmd.Assigns)
	}
	if len(cmd.Words) != 2 {
		t.Errorf("words = %v, want two", cmd.Words)
	}
}

func TestIsAssignment(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"FOO=bar", true},
		{"_x=1", true},
		{"a1=2", true},
		{"=bar", false},   // no name
		{"1FOO=x", false}, // cannot start with a digit
		{"FOO", false},
		{"FOO-BAR=x", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isAssignment(tt.in); got != tt.want {
			t.Errorf("isAssignment(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"cmd >", "cmd <", "cmd |", "cmd 2>"} {
		t.Run(in, func(t *testing.T) {
			if _, err := Parse(in); err == nil {
				t.Error("expected a syntax error")
			}
		})
	}
}

// Empty and separator-only input must parse to nothing rather than to an
// empty command that would then be executed.
func TestParseEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n", ";", ";;", "# just a comment"} {
		list, err := Parse(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if len(list.Items) != 0 {
			t.Errorf("%q produced %d items, want 0", in, len(list.Items))
		}
	}
}

func TestListString(t *testing.T) {
	list, err := Parse("ls -l | grep foo && echo done")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := list.String(), "ls -l | grep foo && echo done"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// FOO="a b c" is an assignment: the quoting applies to the value, not the
// name. Requiring the whole word to be unquoted gets this wrong.
func TestQuotedAssignmentValue(t *testing.T) {
	tests := []struct {
		in         string
		wantAssign bool
	}{
		{`FOO=bar`, true},
		{`FOO="a b c"`, true},
		{`FOO='a b c'`, true},
		{`"FOO=bar"`, false}, // the whole thing quoted is a command word
		{`'FOO=bar'`, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			list, err := Parse(tt.in + " cmd")
			if err != nil {
				t.Fatal(err)
			}
			cmd := list.Items[0].Pipeline.Commands[0]
			got := len(cmd.Assigns) == 1
			if got != tt.wantAssign {
				t.Errorf("assignment = %v, want %v (assigns=%v words=%v)",
					got, tt.wantAssign, cmd.Assigns, cmd.Words)
			}
		})
	}
}
