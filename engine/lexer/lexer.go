package lexer

// Lexer performs lexical analysis on Codong source code, producing a stream
// of tokens for the parser.
type Lexer struct {
	input        string
	position     int  // current position in input (points to current char)
	readPosition int  // current reading position (after current char)
	ch           byte // current character under examination
	line         int  // current line number (1-based)
	column       int  // current column number (1-based)
}

// New creates a new Lexer for the given input string.
func New(input string) *Lexer {
	l := &Lexer{
		input:  input,
		line:   1,
		column: 0,
	}
	l.readChar()
	return l
}

// readChar advances the lexer by one character.
func (l *Lexer) readChar() {
	if l.readPosition >= len(l.input) {
		l.ch = 0
	} else {
		l.ch = l.input[l.readPosition]
	}
	l.position = l.readPosition
	l.readPosition++
	l.column++
}

// peekChar returns the next character without advancing the lexer.
func (l *Lexer) peekChar() byte {
	if l.readPosition >= len(l.input) {
		return 0
	}
	return l.input[l.readPosition]
}

// peekCharAt returns the character at position offset from readPosition.
func (l *Lexer) peekCharAt(offset int) byte {
	pos := l.readPosition + offset
	if pos >= len(l.input) {
		return 0
	}
	return l.input[pos]
}

// skipWhitespace consumes spaces and tabs (but not newlines, which are
// significant as statement terminators).
func (l *Lexer) skipWhitespace() {
	for l.ch == ' ' || l.ch == '\t' || l.ch == '\r' {
		l.readChar()
	}
}

// NextToken reads the next token from the input and returns it.
func (l *Lexer) NextToken() Token {
	l.skipWhitespace()

	tok := Token{Line: l.line, Column: l.column}

	switch l.ch {
	case '\n':
		tok.Type = NEWLINE
		tok.Literal = "\n"
		l.line++
		l.column = 0
		l.readChar()
	case ';':
		tok.Type = NEWLINE
		tok.Literal = ";"
		l.readChar()
		return tok

	// Operators and delimiters
	case '=':
		if l.peekChar() == '=' {
			tok.Type = EQ
			tok.Literal = "=="
			l.readChar()
			l.readChar()
		} else if l.peekChar() == '>' {
			tok.Type = ARROW
			tok.Literal = "=>"
			l.readChar()
			l.readChar()
		} else {
			tok.Type = ASSIGN
			tok.Literal = "="
			l.readChar()
		}

	case '+':
		if l.peekChar() == '=' {
			tok.Type = PLUS_ASSIGN
			tok.Literal = "+="
			l.readChar()
			l.readChar()
		} else {
			tok.Type = PLUS
			tok.Literal = "+"
			l.readChar()
		}

	case '-':
		if l.peekChar() == '=' {
			tok.Type = MINUS_ASSIGN
			tok.Literal = "-="
			l.readChar()
			l.readChar()
		} else {
			tok.Type = MINUS
			tok.Literal = "-"
			l.readChar()
		}

	case '*':
		if l.peekChar() == '=' {
			tok.Type = ASTERISK_ASSIGN
			tok.Literal = "*="
			l.readChar()
			l.readChar()
		} else {
			tok.Type = ASTERISK
			tok.Literal = "*"
			l.readChar()
		}

	case '/':
		if l.peekChar() == '/' {
			l.skipLineComment()
			return l.NextToken()
		} else if l.peekChar() == '*' {
			l.skipBlockComment()
			return l.NextToken()
		} else if l.peekChar() == '=' {
			tok.Type = SLASH_ASSIGN
			tok.Literal = "/="
			l.readChar()
			l.readChar()
		} else {
			tok.Type = SLASH
			tok.Literal = "/"
			l.readChar()
		}

	case '%':
		tok.Type = PERCENT
		tok.Literal = "%"
		l.readChar()

	case '!':
		if l.peekChar() == '=' {
			tok.Type = NOT_EQ
			tok.Literal = "!="
			l.readChar()
			l.readChar()
		} else {
			tok.Type = BANG
			tok.Literal = "!"
			l.readChar()
		}

	case '<':
		if l.peekChar() == '=' {
			tok.Type = LT_EQ
			tok.Literal = "<="
			l.readChar()
			l.readChar()
		} else if l.peekChar() == '-' {
			tok.Type = CHAN_OP
			tok.Literal = "<-"
			l.readChar()
			l.readChar()
		} else {
			tok.Type = LT
			tok.Literal = "<"
			l.readChar()
		}

	case '>':
		if l.peekChar() == '=' {
			tok.Type = GT_EQ
			tok.Literal = ">="
			l.readChar()
			l.readChar()
		} else {
			tok.Type = GT
			tok.Literal = ">"
			l.readChar()
		}

	case '&':
		if l.peekChar() == '&' {
			tok.Type = AND
			tok.Literal = "&&"
			l.readChar()
			l.readChar()
		} else {
			tok.Type = ILLEGAL
			tok.Literal = string(l.ch)
			l.readChar()
		}

	case '|':
		if l.peekChar() == '|' {
			tok.Type = OR
			tok.Literal = "||"
			l.readChar()
			l.readChar()
		} else {
			tok.Type = ILLEGAL
			tok.Literal = string(l.ch)
			l.readChar()
		}

	case '?':
		tok.Type = QUESTION
		tok.Literal = "?"
		l.readChar()

	case '.':
		tok.Type = DOT
		tok.Literal = "."
		l.readChar()

	case ':':
		tok.Type = COLON
		tok.Literal = ":"
		l.readChar()

	case ',':
		tok.Type = COMMA
		tok.Literal = ","
		l.readChar()

	case '(':
		tok.Type = LPAREN
		tok.Literal = "("
		l.readChar()

	case ')':
		tok.Type = RPAREN
		tok.Literal = ")"
		l.readChar()

	case '{':
		tok.Type = LBRACE
		tok.Literal = "{"
		l.readChar()

	case '}':
		tok.Type = RBRACE
		tok.Literal = "}"
		l.readChar()

	case '[':
		tok.Type = LBRACKET
		tok.Literal = "["
		l.readChar()

	case ']':
		tok.Type = RBRACKET
		tok.Literal = "]"
		l.readChar()

	case '"':
		// Check for triple-quoted multi-line string.
		if l.peekChar() == '"' && l.peekCharAt(1) == '"' {
			tok.Type = TRIPLE_QUOTE
			tok.Literal = l.readTripleQuoteString()
		} else {
			tok.Type = STRING
			tok.Literal = l.readString()
		}

	case 0:
		tok.Type = EOF
		tok.Literal = ""

	default:
		if isLetter(l.ch) {
			tok.Literal = l.readIdentifier()
			tok.Type = LookupIdent(tok.Literal)
			return tok
		} else if isDigit(l.ch) {
			tok.Literal = l.readNumber()
			tok.Type = NUMBER
			return tok
		} else {
			tok.Type = ILLEGAL
			tok.Literal = string(l.ch)
			l.readChar()
		}
	}

	return tok
}

// readIdentifier reads an identifier or keyword: [a-zA-Z_][a-zA-Z0-9_]*
func (l *Lexer) readIdentifier() string {
	start := l.position
	for isLetter(l.ch) || isDigit(l.ch) {
		l.readChar()
	}
	return l.input[start:l.position]
}

// readNumber reads an integer or floating-point number literal.
// Supports forms like 42 and 3.14.
func (l *Lexer) readNumber() string {
	start := l.position
	for isDigit(l.ch) {
		l.readChar()
	}
	// Check for a decimal point followed by more digits.
	if l.ch == '.' && isDigit(l.peekChar()) {
		l.readChar() // consume '.'
		for isDigit(l.ch) {
			l.readChar()
		}
	}
	return l.input[start:l.position]
}

// readString reads a double-quoted string literal, processing escape
// sequences. String interpolation expressions like {name} are included
// verbatim in the literal; the parser handles splitting them out.
func (l *Lexer) readString() string {
	l.readChar() // consume opening '"'
	var result []byte
	for {
		if l.ch == '"' || l.ch == 0 {
			break
		}
		if l.ch == '\\' {
			result = append(result, l.readEscape()...)
		} else {
			if l.ch == '\n' {
				l.line++
				l.column = 0
			}
			result = append(result, l.ch)
			l.readChar()
		}
	}
	l.readChar() // consume closing '"'
	return string(result)
}

// readEscape reads and returns the bytes for an escape sequence.
// The lexer is positioned on the backslash when called.
func (l *Lexer) readEscape() []byte {
	l.readChar() // consume '\\'
	var b byte
	switch l.ch {
	case 'n':
		b = '\n'
	case 't':
		b = '\t'
	case 'r':
		b = '\r'
	case '\\':
		b = '\\'
	case '"':
		b = '"'
	case '0':
		b = 0
	default:
		// Unknown escape: keep both characters.
		result := []byte{'\\', l.ch}
		l.readChar()
		return result
	}
	l.readChar()
	return []byte{b}
}

// readTripleQuoteString reads a triple-double-quoted multi-line string
// literal ("""..."""). The content between the delimiters is returned as-is,
// except that escape sequences are processed.
func (l *Lexer) readTripleQuoteString() string {
	// Consume the opening """
	l.readChar() // first "
	l.readChar() // second "
	l.readChar() // third "

	var result []byte
	for {
		if l.ch == 0 {
			break
		}
		// Check for closing """
		if l.ch == '"' && l.peekChar() == '"' && l.peekCharAt(1) == '"' {
			l.readChar() // first "
			l.readChar() // second "
			l.readChar() // third "
			break
		}
		if l.ch == '\\' {
			result = append(result, l.readEscape()...)
		} else {
			if l.ch == '\n' {
				l.line++
				l.column = 0
			}
			result = append(result, l.ch)
			l.readChar()
		}
	}
	return string(result)
}

// skipLineComment skips a single-line comment starting with //.
func (l *Lexer) skipLineComment() {
	for l.ch != '\n' && l.ch != 0 {
		l.readChar()
	}
}

// skipBlockComment skips a block comment delimited by /* and */.
func (l *Lexer) skipBlockComment() {
	l.readChar() // consume '/'
	l.readChar() // consume '*'
	for {
		if l.ch == 0 {
			break
		}
		if l.ch == '\n' {
			l.line++
			l.column = 0
		}
		if l.ch == '*' && l.peekChar() == '/' {
			l.readChar() // consume '*'
			l.readChar() // consume '/'
			break
		}
		l.readChar()
	}
}

// isLetter reports whether ch is a letter or underscore.
func isLetter(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || ch == '$'
}

// isDigit reports whether ch is a decimal digit.
func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}
