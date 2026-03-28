package lexer

// TokenType represents the type of a lexer token.
type TokenType string

// Token type constants.
const (
	// Special tokens
	ILLEGAL TokenType = "ILLEGAL"
	EOF     TokenType = "EOF"
	NEWLINE TokenType = "NEWLINE"

	// Identifiers and literals
	IDENT        TokenType = "IDENT"
	NUMBER       TokenType = "NUMBER"
	STRING       TokenType = "STRING"
	STRING_INTERP TokenType = "STRING_INTERP"
	TRIPLE_QUOTE TokenType = "TRIPLE_QUOTE"

	// Operators
	ASSIGN   TokenType = "="
	PLUS     TokenType = "+"
	MINUS    TokenType = "-"
	ASTERISK TokenType = "*"
	SLASH    TokenType = "/"
	PERCENT  TokenType = "%"
	BANG     TokenType = "!"

	EQ     TokenType = "=="
	NOT_EQ TokenType = "!="
	LT     TokenType = "<"
	GT     TokenType = ">"
	LT_EQ  TokenType = "<="
	GT_EQ  TokenType = ">="

	AND TokenType = "&&"
	OR  TokenType = "||"

	ARROW    TokenType = "=>"
	CHAN_OP  TokenType = "<-"
	QUESTION TokenType = "?"
	DOT      TokenType = "."
	COLON    TokenType = ":"

	POWER           TokenType = "**"

	PLUS_ASSIGN     TokenType = "+="
	MINUS_ASSIGN    TokenType = "-="
	ASTERISK_ASSIGN TokenType = "*="
	SLASH_ASSIGN    TokenType = "/="

	// Delimiters
	LPAREN   TokenType = "("
	RPAREN   TokenType = ")"
	LBRACE   TokenType = "{"
	RBRACE   TokenType = "}"
	LBRACKET TokenType = "["
	RBRACKET TokenType = "]"
	COMMA    TokenType = ","

	// Comments
	COMMENT       TokenType = "COMMENT"
	BLOCK_COMMENT TokenType = "BLOCK_COMMENT"

	// Keywords
	FN        TokenType = "FN"
	RETURN    TokenType = "RETURN"
	IF        TokenType = "IF"
	ELSE      TokenType = "ELSE"
	FOR       TokenType = "FOR"
	WHILE     TokenType = "WHILE"
	MATCH     TokenType = "MATCH"
	BREAK     TokenType = "BREAK"
	CONTINUE  TokenType = "CONTINUE"
	CONST     TokenType = "CONST"
	IMPORT    TokenType = "IMPORT"
	EXPORT    TokenType = "EXPORT"
	TRY       TokenType = "TRY"
	CATCH     TokenType = "CATCH"
	GO        TokenType = "GO"
	SELECT    TokenType = "SELECT"
	INTERFACE TokenType = "INTERFACE"
	TYPE      TokenType = "TYPE"
	NULL      TokenType = "NULL"
	TRUE      TokenType = "TRUE"
	FALSE     TokenType = "FALSE"
	IN        TokenType = "IN"
	BLANK     TokenType = "_"
)

// keywords maps keyword literals to their token types.
var keywords = map[string]TokenType{
	"fn":        FN,
	"return":    RETURN,
	"if":        IF,
	"else":      ELSE,
	"for":       FOR,
	"while":     WHILE,
	"match":     MATCH,
	"break":     BREAK,
	"continue":  CONTINUE,
	"const":     CONST,
	"import":    IMPORT,
	"export":    EXPORT,
	"try":       TRY,
	"catch":     CATCH,
	"go":        GO,
	"select":    SELECT,
	"interface": INTERFACE,
	"type":      TYPE,
	"null":      NULL,
	"true":      TRUE,
	"false":     FALSE,
	"in":        IN,
	"_":         BLANK,
}

// Token represents a single lexical token produced by the lexer.
type Token struct {
	Type    TokenType
	Literal string
	Line    int
	Column  int
}

// LookupIdent checks whether the given identifier is a keyword. If it is,
// the corresponding keyword TokenType is returned. Otherwise, IDENT is returned.
func LookupIdent(ident string) TokenType {
	if tok, ok := keywords[ident]; ok {
		return tok
	}
	return IDENT
}
