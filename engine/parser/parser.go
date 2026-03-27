package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/codong-lang/codong/engine/lexer"
)

// Precedence levels for Pratt parsing.
const (
	_ int = iota
	LOWEST
	OR_PREC      // ||
	AND_PREC     // &&
	EQUALS       // == !=
	LESSGREATER  // < > <= >=
	SUM          // + -
	PRODUCT      // * / %
	PREFIX       // -x !x
	CALL         // fn(x)
	INDEX        // a[0] a.b a?
)

var precedences = map[lexer.TokenType]int{
	lexer.OR:       OR_PREC,
	lexer.AND:      AND_PREC,
	lexer.EQ:       EQUALS,
	lexer.NOT_EQ:   EQUALS,
	lexer.LT:       LESSGREATER,
	lexer.GT:       LESSGREATER,
	lexer.LT_EQ:    LESSGREATER,
	lexer.GT_EQ:    LESSGREATER,
	lexer.PLUS:     SUM,
	lexer.MINUS:    SUM,
	lexer.ASTERISK: PRODUCT,
	lexer.SLASH:    PRODUCT,
	lexer.PERCENT:  PRODUCT,
	lexer.LPAREN:   CALL,
	lexer.LBRACKET: INDEX,
	lexer.DOT:      INDEX,
	lexer.QUESTION: INDEX,
	lexer.CHAN_OP:   SUM, // channel send
}

type (
	prefixParseFn func() Expression
	infixParseFn  func(Expression) Expression
)

// Parser produces an AST from a stream of tokens.
type Parser struct {
	l      *lexer.Lexer
	errors []string

	curToken      lexer.Token
	peekToken     lexer.Token
	lookaheadToken *lexer.Token // used for multi-line chaining lookahead

	prefixParseFns map[lexer.TokenType]prefixParseFn
	infixParseFns  map[lexer.TokenType]infixParseFn
}

// New creates a new Parser.
func New(l *lexer.Lexer) *Parser {
	p := &Parser{l: l, errors: []string{}}

	p.prefixParseFns = make(map[lexer.TokenType]prefixParseFn)
	p.registerPrefix(lexer.IDENT, p.parseIdentifier)
	p.registerPrefix(lexer.NUMBER, p.parseNumberLiteral)
	p.registerPrefix(lexer.STRING, p.parseStringLiteral)
	p.registerPrefix(lexer.TRIPLE_QUOTE, p.parseStringLiteral)
	p.registerPrefix(lexer.TRUE, p.parseBoolLiteral)
	p.registerPrefix(lexer.FALSE, p.parseBoolLiteral)
	p.registerPrefix(lexer.NULL, p.parseNullLiteral)
	p.registerPrefix(lexer.BANG, p.parsePrefixExpression)
	p.registerPrefix(lexer.MINUS, p.parsePrefixExpression)
	p.registerPrefix(lexer.LPAREN, p.parseGroupedExpression)
	p.registerPrefix(lexer.LBRACKET, p.parseListLiteral)
	p.registerPrefix(lexer.LBRACE, p.parseMapLiteral)
	p.registerPrefix(lexer.FN, p.parseFunctionLiteral)
	p.registerPrefix(lexer.CHAN_OP, p.parseChannelReceive)
	p.registerPrefix(lexer.BLANK, p.parseBlankIdentifier)
	p.registerPrefix(lexer.MATCH, p.parseMatchExpression)

	p.infixParseFns = make(map[lexer.TokenType]infixParseFn)
	p.registerInfix(lexer.PLUS, p.parseInfixExpression)
	p.registerInfix(lexer.MINUS, p.parseInfixExpression)
	p.registerInfix(lexer.ASTERISK, p.parseInfixExpression)
	p.registerInfix(lexer.SLASH, p.parseInfixExpression)
	p.registerInfix(lexer.PERCENT, p.parseInfixExpression)
	p.registerInfix(lexer.EQ, p.parseInfixExpression)
	p.registerInfix(lexer.NOT_EQ, p.parseInfixExpression)
	p.registerInfix(lexer.LT, p.parseInfixExpression)
	p.registerInfix(lexer.GT, p.parseInfixExpression)
	p.registerInfix(lexer.LT_EQ, p.parseInfixExpression)
	p.registerInfix(lexer.GT_EQ, p.parseInfixExpression)
	p.registerInfix(lexer.AND, p.parseInfixExpression)
	p.registerInfix(lexer.OR, p.parseInfixExpression)
	p.registerInfix(lexer.LPAREN, p.parseCallExpression)
	p.registerInfix(lexer.LBRACKET, p.parseIndexExpression)
	p.registerInfix(lexer.DOT, p.parseMemberAccess)
	p.registerInfix(lexer.QUESTION, p.parseErrorPropagation)
	p.registerInfix(lexer.CHAN_OP, p.parseChannelSend)

	// Read two tokens to populate curToken and peekToken
	p.nextToken()
	p.nextToken()

	return p
}

func (p *Parser) Errors() []string { return p.errors }

func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	if p.lookaheadToken != nil {
		p.peekToken = *p.lookaheadToken
		p.lookaheadToken = nil
	} else {
		p.peekToken = p.l.NextToken()
	}
}

func (p *Parser) skipNewlines() {
	for p.curToken.Type == lexer.NEWLINE {
		p.nextToken()
	}
}

func (p *Parser) registerPrefix(t lexer.TokenType, fn prefixParseFn) {
	p.prefixParseFns[t] = fn
}

func (p *Parser) parseBlankIdentifier() Expression {
	return &Identifier{Token: p.curToken, Value: "_"}
}

func (p *Parser) registerInfix(t lexer.TokenType, fn infixParseFn) {
	p.infixParseFns[t] = fn
}

func (p *Parser) peekPrecedence() int {
	if p, ok := precedences[p.peekToken.Type]; ok {
		return p
	}
	return LOWEST
}

func (p *Parser) curPrecedence() int {
	if p, ok := precedences[p.curToken.Type]; ok {
		return p
	}
	return LOWEST
}

func (p *Parser) expectPeek(t lexer.TokenType) bool {
	if p.peekToken.Type == t {
		p.nextToken()
		return true
	}
	p.peekError(t)
	return false
}

func (p *Parser) isKeywordToken(t lexer.TokenType) bool {
	switch t {
	case lexer.FN, lexer.IF, lexer.ELSE, lexer.RETURN, lexer.FOR, lexer.IN,
		lexer.WHILE, lexer.MATCH, lexer.CONST, lexer.BREAK, lexer.CONTINUE,
		lexer.IMPORT, lexer.EXPORT, lexer.TRY, lexer.CATCH, lexer.GO,
		lexer.TYPE, lexer.INTERFACE, lexer.TRUE, lexer.FALSE, lexer.NULL:
		return true
	}
	return false
}

func (p *Parser) peekError(t lexer.TokenType) {
	p.errors = append(p.errors, fmt.Sprintf("line %d: expected %s, got %s",
		p.peekToken.Line, t, p.peekToken.Type))
}

// ParseProgram parses the entire program.
func (p *Parser) ParseProgram() *Program {
	program := &Program{}
	for p.curToken.Type != lexer.EOF {
		p.skipNewlines()
		if p.curToken.Type == lexer.EOF {
			break
		}
		stmt := p.parseStatement()
		if stmt != nil {
			program.Statements = append(program.Statements, stmt)
		}
		p.nextToken()
	}
	return program
}

func (p *Parser) parseStatement() Statement {
	switch p.curToken.Type {
	case lexer.CONST:
		return p.parseConstStatement()
	case lexer.RETURN:
		return p.parseReturnStatement()
	case lexer.IF:
		return p.parseIfStatement()
	case lexer.FOR:
		return p.parseForInStatement()
	case lexer.WHILE:
		return p.parseWhileStatement()
	case lexer.MATCH:
		return p.parseMatchStatement()
	case lexer.FN:
		// Check if it's a named function definition (fn name(...))
		if p.peekToken.Type == lexer.IDENT {
			return p.parseFunctionDefinition()
		}
		return p.parseExpressionStatement()
	case lexer.TRY:
		return p.parseTryCatchStatement()
	case lexer.GO:
		return p.parseGoStatement()
	case lexer.SELECT:
		return p.parseSelectStatement()
	case lexer.BREAK:
		return &BreakStatement{Token: p.curToken}
	case lexer.CONTINUE:
		return &ContinueStatement{Token: p.curToken}
	case lexer.IMPORT:
		return p.parseImportStatement()
	case lexer.EXPORT:
		return p.parseExportStatement()
	case lexer.TYPE:
		return p.parseTypeDeclaration()
	case lexer.INTERFACE:
		return p.parseInterfaceDeclaration()
	default:
		return p.parseAssignOrExpressionStatement()
	}
}

func (p *Parser) parseAssignOrExpressionStatement() Statement {
	// Catch common non-Codong keywords
	if p.curToken.Type == lexer.IDENT {
		switch p.curToken.Literal {
		case "var", "let":
			p.errors = append(p.errors, fmt.Sprintf("line %d: '%s' is not a Codong keyword. Assign directly: x = value, or use 'const x = value'", p.curToken.Line, p.curToken.Literal))
			return nil
		}
	}
	// Parse the left-hand side as an expression first
	leftExpr := p.parseExpression(LOWEST)

	// Check if next token is an assignment operator
	switch p.peekToken.Type {
	case lexer.ASSIGN:
		p.nextToken() // move to =
		p.nextToken() // move past =
		value := p.parseExpression(LOWEST)

		switch lhs := leftExpr.(type) {
		case *Identifier:
			return &AssignStatement{Token: p.curToken, Name: lhs, Value: value}
		case *MemberAccessExpression:
			return &PropertyAssignStatement{Token: p.curToken, Object: lhs.Object, Property: lhs.Property, Value: value}
		case *IndexExpression:
			return &IndexAssignStatement{Token: p.curToken, Left: lhs.Left, Index: lhs.Index, Value: value}
		default:
			p.errors = append(p.errors, fmt.Sprintf("line %d: invalid assignment target", p.curToken.Line))
			return nil
		}

	case lexer.PLUS_ASSIGN, lexer.MINUS_ASSIGN, lexer.ASTERISK_ASSIGN, lexer.SLASH_ASSIGN:
		p.nextToken() // move to operator
		op := p.curToken.Literal
		p.nextToken() // move past operator
		value := p.parseExpression(LOWEST)
		return &CompoundAssignStatement{Token: p.curToken, Target: leftExpr, Operator: op, Value: value}
	}

	return &ExpressionStatement{Token: p.curToken, Expression: leftExpr}
}

func (p *Parser) parseConstStatement() *ConstStatement {
	stmt := &ConstStatement{Token: p.curToken}
	p.nextToken() // skip const
	stmt.Name = &Identifier{Token: p.curToken, Value: p.curToken.Literal}
	if !p.expectPeek(lexer.ASSIGN) {
		return nil
	}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseReturnStatement() *ReturnStatement {
	stmt := &ReturnStatement{Token: p.curToken}
	p.nextToken()
	if p.curToken.Type != lexer.NEWLINE && p.curToken.Type != lexer.RBRACE && p.curToken.Type != lexer.EOF {
		stmt.Value = p.parseExpression(LOWEST)
	}
	return stmt
}

func (p *Parser) parseIfStatement() *IfStatement {
	stmt := &IfStatement{Token: p.curToken}
	p.nextToken()
	stmt.Condition = p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	stmt.Consequence = p.parseBlockStatement()
	// check for else — skip newlines between } and else
	for p.peekToken.Type == lexer.NEWLINE {
		p.nextToken()
	}
	if p.peekToken.Type == lexer.ELSE {
		p.nextToken() // move to else
		p.nextToken() // skip else
		if p.curToken.Type == lexer.IF {
			stmt.Alternative = p.parseIfStatement()
		} else if p.curToken.Type == lexer.LBRACE {
			stmt.Alternative = p.parseBlockStatement()
		}
	}
	return stmt
}

func (p *Parser) parseForInStatement() *ForInStatement {
	stmt := &ForInStatement{Token: p.curToken}
	p.nextToken() // skip for
	stmt.Variable = &Identifier{Token: p.curToken, Value: p.curToken.Literal}
	if !p.expectPeek(lexer.IN) {
		return nil
	}
	p.nextToken() // skip in
	stmt.Iterable = p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseWhileStatement() *WhileStatement {
	stmt := &WhileStatement{Token: p.curToken}
	p.nextToken()
	stmt.Condition = p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseMatchStatement() *MatchStatement {
	stmt := &MatchStatement{Token: p.curToken}
	p.nextToken()
	stmt.Subject = p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	p.nextToken() // skip {
	p.skipNewlines()
	for p.curToken.Type != lexer.RBRACE && p.curToken.Type != lexer.EOF {
		mc := MatchCase{}
		if p.curToken.Type == lexer.BLANK {
			mc.IsDefault = true
		} else {
			mc.Pattern = p.parseExpression(LOWEST)
		}
		if !p.expectPeek(lexer.ARROW) {
			return nil
		}
		p.nextToken() // skip =>
		if p.curToken.Type == lexer.LBRACE {
			mc.BodyBlock = p.parseBlockStatement()
		} else if p.curToken.Type == lexer.RETURN {
			retStmt := p.parseReturnStatement()
			mc.BodyBlock = &BlockStatement{Statements: []Statement{retStmt}}
		} else {
			// Parse as a statement (handles assignments, compound assignments,
			// member access assignments like counts.A += 1, and expressions)
			armStmt := p.parseAssignOrExpressionStatement()
			mc.BodyBlock = &BlockStatement{Statements: []Statement{armStmt}}
		}
		stmt.Cases = append(stmt.Cases, mc)
		p.nextToken()
		p.skipNewlines()
	}
	return stmt
}

// parseMatchExpression allows match to be used as an expression (e.g., return match n {...})
func (p *Parser) parseMatchExpression() Expression {
	return p.parseMatchStatement()
}

func (p *Parser) parseFunctionDefinition() *FunctionDefinition {
	stmt := &FunctionDefinition{Token: p.curToken}
	p.nextToken() // skip fn
	stmt.Name = &Identifier{Token: p.curToken, Value: p.curToken.Literal}
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	stmt.Params, stmt.Defaults = p.parseFunctionParameters()
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseFunctionParameters() ([]*TypedIdentifier, map[string]Expression) {
	params := []*TypedIdentifier{}
	defaults := map[string]Expression{}
	p.nextToken() // skip (
	for p.curToken.Type != lexer.RPAREN && p.curToken.Type != lexer.EOF {
		param := &TypedIdentifier{Token: p.curToken, Name: p.curToken.Literal}
		p.nextToken()
		// Check for type annotation: name: type
		if p.curToken.Type == lexer.COLON {
			p.nextToken()
			param.TypeAnnotation = p.curToken.Literal
			p.nextToken()
		}
		// Check for default value: name = expr
		if p.curToken.Type == lexer.ASSIGN {
			p.nextToken()
			defaults[param.Name] = p.parseExpression(LOWEST)
			p.nextToken()
		}
		params = append(params, param)
		if p.curToken.Type == lexer.COMMA {
			p.nextToken()
		}
	}
	return params, defaults
}

func (p *Parser) parseTryCatchStatement() *TryCatchStatement {
	stmt := &TryCatchStatement{Token: p.curToken}
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	stmt.Try = p.parseBlockStatement()
	p.nextToken() // skip }
	p.skipNewlines()
	if p.curToken.Type != lexer.CATCH {
		p.errors = append(p.errors, "expected catch after try block")
		return nil
	}
	p.nextToken() // skip catch
	stmt.CatchVar = &Identifier{Token: p.curToken, Value: p.curToken.Literal}
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	stmt.Catch = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseGoStatement() *GoStatement {
	stmt := &GoStatement{Token: p.curToken}
	p.nextToken() // skip go
	stmt.Call = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseSelectStatement() *SelectStatement {
	stmt := &SelectStatement{Token: p.curToken}
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	p.nextToken() // skip {
	p.skipNewlines()
	for p.curToken.Type != lexer.RBRACE && p.curToken.Type != lexer.EOF {
		sc := SelectCase{Token: p.curToken}
		if p.curToken.Type == lexer.IDENT && p.peekToken.Type == lexer.ASSIGN {
			sc.Assignment = &Identifier{Token: p.curToken, Value: p.curToken.Literal}
			p.nextToken() // skip name
			p.nextToken() // skip =
		}
		// Expect <-ch
		if p.curToken.Type == lexer.CHAN_OP {
			p.nextToken()
			sc.Channel = p.parseExpression(LOWEST)
		}
		if !p.expectPeek(lexer.LBRACE) {
			return nil
		}
		sc.Body = p.parseBlockStatement()
		stmt.Cases = append(stmt.Cases, sc)
		p.nextToken()
		p.skipNewlines()
	}
	return stmt
}

func (p *Parser) parseImportStatement() *ImportStatement {
	stmt := &ImportStatement{Token: p.curToken, Aliases: map[string]string{}}
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	p.nextToken() // skip {
	for p.curToken.Type != lexer.RBRACE && p.curToken.Type != lexer.EOF {
		name := p.curToken.Literal
		p.nextToken()
		// Check for "as alias"
		if p.curToken.Type == lexer.IDENT && p.curToken.Literal == "as" {
			p.nextToken() // skip "as"
			alias := p.curToken.Literal
			stmt.Names = append(stmt.Names, name)
			stmt.Aliases[name] = alias
			p.nextToken()
		} else {
			stmt.Names = append(stmt.Names, name)
		}
		if p.curToken.Type == lexer.COMMA {
			p.nextToken()
		}
	}
	p.nextToken() // skip }
	// expect "from"
	if p.curToken.Literal != "from" {
		p.errors = append(p.errors, "expected 'from' in import statement")
		return nil
	}
	p.nextToken() // skip from
	stmt.Path = p.curToken.Literal
	return stmt
}

func (p *Parser) parseExportStatement() *ExportStatement {
	stmt := &ExportStatement{Token: p.curToken}
	p.nextToken() // skip export
	stmt.Statement = p.parseStatement()
	return stmt
}

func (p *Parser) parseTypeDeclaration() *TypeDeclaration {
	stmt := &TypeDeclaration{Token: p.curToken}
	p.nextToken() // skip type
	stmt.Name = &Identifier{Token: p.curToken, Value: p.curToken.Literal}
	if !p.expectPeek(lexer.ASSIGN) {
		return nil
	}
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	p.nextToken() // skip {
	p.skipNewlines()
	for p.curToken.Type != lexer.RBRACE && p.curToken.Type != lexer.EOF {
		field := TypeField{Name: p.curToken.Literal}
		if !p.expectPeek(lexer.COLON) {
			return nil
		}
		p.nextToken()
		field.Type = p.curToken.Literal
		stmt.Fields = append(stmt.Fields, field)
		p.nextToken()
		if p.curToken.Type == lexer.COMMA {
			p.nextToken()
		}
		p.skipNewlines()
	}
	return stmt
}

func (p *Parser) parseInterfaceDeclaration() *InterfaceDeclaration {
	stmt := &InterfaceDeclaration{Token: p.curToken}
	p.nextToken() // skip interface
	stmt.Name = &Identifier{Token: p.curToken, Value: p.curToken.Literal}
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	p.nextToken() // skip {
	p.skipNewlines()
	for p.curToken.Type != lexer.RBRACE && p.curToken.Type != lexer.EOF {
		if p.curToken.Type == lexer.FN {
			p.nextToken()
			method := InterfaceMethod{Name: p.curToken.Literal}
			if p.expectPeek(lexer.LPAREN) {
				method.Params, _ = p.parseFunctionParameters()
			}
			if p.peekToken.Type == lexer.ARROW {
				p.nextToken() // skip =>
				p.nextToken()
				method.Return = p.curToken.Literal
			}
			stmt.Methods = append(stmt.Methods, method)
		}
		p.nextToken()
		p.skipNewlines()
	}
	return stmt
}

func (p *Parser) parseBlockStatement() *BlockStatement {
	block := &BlockStatement{Token: p.curToken}
	p.nextToken() // skip {
	p.skipNewlines()
	for p.curToken.Type != lexer.RBRACE && p.curToken.Type != lexer.EOF {
		stmt := p.parseStatement()
		if stmt != nil {
			block.Statements = append(block.Statements, stmt)
		}
		p.nextToken()
		p.skipNewlines()
	}
	return block
}

func (p *Parser) parseExpressionStatement() *ExpressionStatement {
	stmt := &ExpressionStatement{Token: p.curToken}
	stmt.Expression = p.parseExpression(LOWEST)
	return stmt
}

// --- Expression Parsing (Pratt) ---

func (p *Parser) parseExpression(precedence int) Expression {
	prefix := p.prefixParseFns[p.curToken.Type]
	if prefix == nil {
		p.errors = append(p.errors, fmt.Sprintf("line %d: no prefix parse function for %s",
			p.curToken.Line, p.curToken.Type))
		return nil
	}
	leftExp := prefix()

	for p.peekToken.Type != lexer.NEWLINE && p.peekToken.Type != lexer.EOF && precedence < p.peekPrecedence() {
		infix := p.infixParseFns[p.peekToken.Type]
		if infix == nil {
			return leftExp
		}
		p.nextToken()
		leftExp = infix(leftExp)
	}

	// Allow method chaining across newlines: if the next non-newline token is `.`
	// at a higher precedence than the current context, continue parsing.
	for p.peekToken.Type == lexer.NEWLINE {
		lookaheadType := p.peekPastNewlines()
		if (lookaheadType == lexer.DOT || lookaheadType == lexer.QUESTION) && precedence < precedences[lookaheadType] {
			// Consume the newlines so curToken becomes DOT/QUESTION
			for p.peekToken.Type == lexer.NEWLINE {
				p.nextToken()
			}
			infix := p.infixParseFns[p.peekToken.Type]
			if infix == nil {
				break
			}
			p.nextToken()
			leftExp = infix(leftExp)
			// After a newline-chained infix (e.g. DOT), process any immediately
			// following non-newline infixes (e.g. call parentheses, index access).
			for p.peekToken.Type != lexer.NEWLINE && p.peekToken.Type != lexer.EOF && precedence < p.peekPrecedence() {
				inf := p.infixParseFns[p.peekToken.Type]
				if inf == nil {
					break
				}
				p.nextToken()
				leftExp = inf(leftExp)
			}
		} else {
			break
		}
	}

	return leftExp
}

// peekPastNewlines returns the type of the first non-newline peek token.
// It uses the token buffer to look ahead without consuming tokens.
func (p *Parser) peekPastNewlines() lexer.TokenType {
	if p.peekToken.Type != lexer.NEWLINE {
		return p.peekToken.Type
	}
	// Need to look further. Use the lookahead buffer.
	if p.lookaheadToken != nil {
		return p.lookaheadToken.Type
	}
	// Scan ahead from lexer
	tok := p.l.NextToken()
	for tok.Type == lexer.NEWLINE {
		tok = p.l.NextToken()
	}
	p.lookaheadToken = &tok
	return tok.Type
}

func (p *Parser) parseIdentifier() Expression {
	return &Identifier{Token: p.curToken, Value: p.curToken.Literal}
}

func (p *Parser) parseNumberLiteral() Expression {
	val, err := strconv.ParseFloat(p.curToken.Literal, 64)
	if err != nil {
		p.errors = append(p.errors, fmt.Sprintf("could not parse %q as number", p.curToken.Literal))
		return nil
	}
	return &NumberLiteral{Token: p.curToken, Value: val}
}

func (p *Parser) parseStringLiteral() Expression {
	raw := p.curToken.Literal
	// Check for interpolation: contains { and }
	if strings.Contains(raw, "{") && strings.Contains(raw, "}") {
		// Skip interpolation if it looks like a JSON literal:
		// JSON pattern: {"key": value} — after the string key there's a colon
		braceIdx := strings.Index(raw, "{")
		if braceIdx >= 0 && braceIdx+1 < len(raw) && raw[braceIdx+1] == '"' {
			// Find the closing quote of the key string
			keyEnd := strings.Index(raw[braceIdx+2:], "\"")
			if keyEnd >= 0 {
				afterKey := braceIdx + 2 + keyEnd + 1
				// JSON: after the key string, next non-space char should be ':'
				isJSON := false
				for j := afterKey; j < len(raw); j++ {
					if raw[j] == ' ' || raw[j] == '\t' { continue }
					if raw[j] == ':' { isJSON = true }
					break
				}
				if isJSON {
					return &StringLiteral{Token: p.curToken, Value: raw}
				}
			}
		}
		return p.parseInterpolatedString(raw)
	}
	return &StringLiteral{Token: p.curToken, Value: raw}
}

func (p *Parser) parseInterpolatedString(raw string) Expression {
	interp := &StringInterpolation{Token: p.curToken}
	i := 0
	for i < len(raw) {
		braceIdx := strings.Index(raw[i:], "{")
		if braceIdx == -1 {
			interp.Parts = append(interp.Parts, &StringLiteral{Token: p.curToken, Value: raw[i:]})
			break
		}
		if braceIdx > 0 {
			interp.Parts = append(interp.Parts, &StringLiteral{Token: p.curToken, Value: raw[i : i+braceIdx]})
		}
		i += braceIdx + 1
		endBrace := strings.Index(raw[i:], "}")
		if endBrace == -1 {
			interp.Parts = append(interp.Parts, &StringLiteral{Token: p.curToken, Value: raw[i:]})
			break
		}
		exprStr := raw[i : i+endBrace]
		if exprStr == "" {
			// Empty braces {} — treat as literal
			interp.Parts = append(interp.Parts, &StringLiteral{Token: p.curToken, Value: "{}"})
		} else {
			// Parse the expression inside {}
			exprLexer := lexer.New(exprStr)
			exprParser := New(exprLexer)
			expr := exprParser.parseExpression(LOWEST)
			if expr != nil {
				interp.Parts = append(interp.Parts, expr)
			}
		}
		i += endBrace + 1
	}
	if len(interp.Parts) == 1 {
		if sl, ok := interp.Parts[0].(*StringLiteral); ok {
			return sl
		}
	}
	return interp
}

func (p *Parser) parseBoolLiteral() Expression {
	return &BoolLiteral{Token: p.curToken, Value: p.curToken.Type == lexer.TRUE}
}

func (p *Parser) parseNullLiteral() Expression {
	return &NullLiteral{Token: p.curToken}
}

func (p *Parser) parsePrefixExpression() Expression {
	expr := &PrefixExpression{Token: p.curToken, Operator: p.curToken.Literal}
	p.nextToken()
	expr.Right = p.parseExpression(PREFIX)
	return expr
}

func (p *Parser) parseGroupedExpression() Expression {
	p.nextToken() // skip (
	exp := p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.RPAREN) {
		return nil
	}
	return exp
}

func (p *Parser) parseListLiteral() Expression {
	lit := &ListLiteral{Token: p.curToken}
	lit.Elements = p.parseExpressionList(lexer.RBRACKET)
	return lit
}

func (p *Parser) parseMapLiteral() Expression {
	ml := &MapLiteral{Token: p.curToken}
	p.nextToken() // skip {
	p.skipNewlines()
	for p.curToken.Type != lexer.RBRACE && p.curToken.Type != lexer.EOF {
		entry := MapEntry{}
		// Allow keywords (type, fn, if, etc.) as map keys by treating them as identifiers
		if p.isKeywordToken(p.curToken.Type) && p.peekToken.Type == lexer.COLON {
			entry.Key = &Identifier{Token: p.curToken, Value: p.curToken.Literal}
		} else {
			entry.Key = p.parseExpression(LOWEST)
		}
		if !p.expectPeek(lexer.COLON) {
			return nil
		}
		p.nextToken()
		entry.Value = p.parseExpression(LOWEST)
		ml.Entries = append(ml.Entries, entry)
		if p.peekToken.Type == lexer.COMMA {
			p.nextToken()
		}
		p.nextToken()
		p.skipNewlines()
	}
	return ml
}

func (p *Parser) parseFunctionLiteral() Expression {
	lit := &FunctionLiteral{Token: p.curToken}
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	lit.Params, lit.Defaults = p.parseFunctionParameters()

	// Check for arrow: fn(x) => expr
	if p.peekToken.Type == lexer.ARROW {
		p.nextToken() // skip =>
		p.nextToken()
		lit.IsArrow = true
		lit.ArrowExpr = p.parseExpression(LOWEST)
		return lit
	}

	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	lit.Body = p.parseBlockStatement()
	return lit
}

func (p *Parser) parseChannelReceive() Expression {
	expr := &ChannelReceiveExpression{Token: p.curToken}
	p.nextToken()
	expr.Channel = p.parseExpression(PREFIX)
	return expr
}

// --- Infix parsers ---

func (p *Parser) parseInfixExpression(left Expression) Expression {
	expr := &InfixExpression{
		Token:    p.curToken,
		Left:     left,
		Operator: p.curToken.Literal,
	}
	prec := p.curPrecedence()
	p.nextToken()
	expr.Right = p.parseExpression(prec)
	return expr
}

func (p *Parser) parseCallExpression(fn Expression) Expression {
	exp := &CallExpression{Token: p.curToken, Function: fn}
	exp.Arguments, exp.Named = p.parseCallArguments()
	return exp
}

func (p *Parser) parseCallArguments() ([]Expression, map[string]Expression) {
	args := []Expression{}
	named := map[string]Expression{}
	p.nextToken() // skip (
	p.skipNewlines()
	for p.curToken.Type != lexer.RPAREN && p.curToken.Type != lexer.EOF {
		// Check for named argument: key: value
		if p.curToken.Type == lexer.IDENT && p.peekToken.Type == lexer.COLON {
			name := p.curToken.Literal
			p.nextToken() // skip name
			p.nextToken() // skip :
			named[name] = p.parseExpression(LOWEST)
		} else {
			args = append(args, p.parseExpression(LOWEST))
		}
		if p.peekToken.Type == lexer.COMMA {
			p.nextToken()
		}
		p.nextToken()
		p.skipNewlines()
	}
	return args, named
}

func (p *Parser) parseIndexExpression(left Expression) Expression {
	exp := &IndexExpression{Token: p.curToken, Left: left}
	p.nextToken()
	exp.Index = p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.RBRACKET) {
		return nil
	}
	return exp
}

func (p *Parser) parseMemberAccess(left Expression) Expression {
	exp := &MemberAccessExpression{Token: p.curToken, Object: left}
	p.nextToken()
	exp.Property = &Identifier{Token: p.curToken, Value: p.curToken.Literal}
	return exp
}

func (p *Parser) parseErrorPropagation(left Expression) Expression {
	return &ErrorPropagationExpression{Token: p.curToken, Expr: left}
}

func (p *Parser) parseChannelSend(left Expression) Expression {
	p.nextToken()
	value := p.parseExpression(LOWEST)
	return &InfixExpression{
		Token:    p.curToken,
		Left:     left,
		Operator: "<-",
		Right:    value,
	}
}

func (p *Parser) parseExpressionList(end lexer.TokenType) []Expression {
	list := []Expression{}
	p.nextToken() // skip opening token
	p.skipNewlines()
	for p.curToken.Type != end && p.curToken.Type != lexer.EOF {
		list = append(list, p.parseExpression(LOWEST))
		if p.peekToken.Type == lexer.COMMA {
			p.nextToken()
		}
		p.nextToken()
		p.skipNewlines()
	}
	return list
}
