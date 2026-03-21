package parser

import (
	"fmt"
	"strings"

	"github.com/codong-lang/codong/engine/lexer"
)

// Node is the base interface for all AST nodes.
type Node interface {
	TokenLiteral() string
	String() string
}

// Statement is a node that does not produce a value.
type Statement interface {
	Node
	statementNode()
}

// Expression is a node that produces a value.
type Expression interface {
	Node
	expressionNode()
}

// Program is the root node of every AST.
type Program struct {
	Statements []Statement
}

func (p *Program) TokenLiteral() string {
	if len(p.Statements) > 0 {
		return p.Statements[0].TokenLiteral()
	}
	return ""
}

func (p *Program) String() string {
	var out strings.Builder
	for _, s := range p.Statements {
		out.WriteString(s.String())
	}
	return out.String()
}

// --- Statements ---

type ExpressionStatement struct {
	Token      lexer.Token
	Expression Expression
}

func (es *ExpressionStatement) statementNode()       {}
func (es *ExpressionStatement) TokenLiteral() string  { return es.Token.Literal }
func (es *ExpressionStatement) String() string        { return es.Expression.String() }

type AssignStatement struct {
	Token lexer.Token
	Name  *Identifier
	Value Expression
}

func (as *AssignStatement) statementNode()       {}
func (as *AssignStatement) TokenLiteral() string { return as.Token.Literal }
func (as *AssignStatement) String() string {
	return fmt.Sprintf("%s = %s", as.Name.String(), as.Value.String())
}

type ConstStatement struct {
	Token lexer.Token
	Name  *Identifier
	Value Expression
}

func (cs *ConstStatement) statementNode()       {}
func (cs *ConstStatement) TokenLiteral() string { return cs.Token.Literal }
func (cs *ConstStatement) String() string {
	return fmt.Sprintf("const %s = %s", cs.Name.String(), cs.Value.String())
}

type CompoundAssignStatement struct {
	Token    lexer.Token
	Target   Expression // can be Identifier, MemberAccessExpression, or IndexExpression
	Operator string     // "+=", "-=", "*=", "/="
	Value    Expression
}

func (cas *CompoundAssignStatement) statementNode()       {}
func (cas *CompoundAssignStatement) TokenLiteral() string { return cas.Token.Literal }
func (cas *CompoundAssignStatement) String() string {
	return fmt.Sprintf("%s %s %s", cas.Target.String(), cas.Operator, cas.Value.String())
}

type PropertyAssignStatement struct {
	Token    lexer.Token
	Object   Expression
	Property *Identifier
	Value    Expression
}

func (pas *PropertyAssignStatement) statementNode()       {}
func (pas *PropertyAssignStatement) TokenLiteral() string { return pas.Token.Literal }
func (pas *PropertyAssignStatement) String() string {
	return fmt.Sprintf("%s.%s = %s", pas.Object.String(), pas.Property.String(), pas.Value.String())
}

type IndexAssignStatement struct {
	Token lexer.Token
	Left  Expression
	Index Expression
	Value Expression
}

func (ias *IndexAssignStatement) statementNode()       {}
func (ias *IndexAssignStatement) TokenLiteral() string { return ias.Token.Literal }
func (ias *IndexAssignStatement) String() string {
	return fmt.Sprintf("%s[%s] = %s", ias.Left.String(), ias.Index.String(), ias.Value.String())
}

type ReturnStatement struct {
	Token lexer.Token
	Value Expression
}

func (rs *ReturnStatement) statementNode()       {}
func (rs *ReturnStatement) TokenLiteral() string { return rs.Token.Literal }
func (rs *ReturnStatement) String() string {
	if rs.Value != nil {
		return "return " + rs.Value.String()
	}
	return "return"
}

type BlockStatement struct {
	Token      lexer.Token
	Statements []Statement
}

func (bs *BlockStatement) statementNode()       {}
func (bs *BlockStatement) TokenLiteral() string { return bs.Token.Literal }
func (bs *BlockStatement) String() string {
	var out strings.Builder
	for _, s := range bs.Statements {
		out.WriteString(s.String())
	}
	return out.String()
}

type IfStatement struct {
	Token       lexer.Token
	Condition   Expression
	Consequence *BlockStatement
	Alternative Statement // can be another IfStatement (else if) or BlockStatement (else)
}

func (is *IfStatement) statementNode()       {}
func (is *IfStatement) TokenLiteral() string { return is.Token.Literal }
func (is *IfStatement) String() string {
	s := fmt.Sprintf("if %s { %s }", is.Condition.String(), is.Consequence.String())
	if is.Alternative != nil {
		s += " else { " + is.Alternative.String() + " }"
	}
	return s
}

type ForInStatement struct {
	Token    lexer.Token
	Variable *Identifier
	Iterable Expression
	Body     *BlockStatement
}

func (fis *ForInStatement) statementNode()       {}
func (fis *ForInStatement) TokenLiteral() string { return fis.Token.Literal }
func (fis *ForInStatement) String() string {
	return fmt.Sprintf("for %s in %s { %s }", fis.Variable.String(), fis.Iterable.String(), fis.Body.String())
}

type WhileStatement struct {
	Token     lexer.Token
	Condition Expression
	Body      *BlockStatement
}

func (ws *WhileStatement) statementNode()       {}
func (ws *WhileStatement) TokenLiteral() string { return ws.Token.Literal }
func (ws *WhileStatement) String() string {
	return fmt.Sprintf("while %s { %s }", ws.Condition.String(), ws.Body.String())
}

type MatchCase struct {
	Pattern   Expression // literal or nil for default
	Body      Expression
	IsDefault bool
}

type MatchStatement struct {
	Token   lexer.Token
	Subject Expression
	Cases   []MatchCase
}

func (ms *MatchStatement) statementNode()       {}
func (ms *MatchStatement) TokenLiteral() string { return ms.Token.Literal }
func (ms *MatchStatement) String() string       { return "match { ... }" }

type FunctionDefinition struct {
	Token   lexer.Token
	Name    *Identifier
	Params  []*TypedIdentifier
	Body    *BlockStatement
	IsArrow bool
	Defaults map[string]Expression // param name -> default value
}

func (fd *FunctionDefinition) statementNode()       {}
func (fd *FunctionDefinition) TokenLiteral() string { return fd.Token.Literal }
func (fd *FunctionDefinition) String() string {
	return fmt.Sprintf("fn %s(...) { ... }", fd.Name.String())
}

type TryCatchStatement struct {
	Token    lexer.Token
	Try      *BlockStatement
	CatchVar *Identifier
	Catch    *BlockStatement
}

func (tcs *TryCatchStatement) statementNode()       {}
func (tcs *TryCatchStatement) TokenLiteral() string { return tcs.Token.Literal }
func (tcs *TryCatchStatement) String() string       { return "try { ... } catch { ... }" }

type GoStatement struct {
	Token lexer.Token
	Call  Expression
}

func (gs *GoStatement) statementNode()       {}
func (gs *GoStatement) TokenLiteral() string { return gs.Token.Literal }
func (gs *GoStatement) String() string       { return "go " + gs.Call.String() }

type SelectCase struct {
	Token      lexer.Token
	Assignment *Identifier  // optional: msg = <-ch
	Channel    Expression   // the channel expression
	Body       *BlockStatement
}

type SelectStatement struct {
	Token lexer.Token
	Cases []SelectCase
}

func (ss *SelectStatement) statementNode()       {}
func (ss *SelectStatement) TokenLiteral() string { return ss.Token.Literal }
func (ss *SelectStatement) String() string       { return "select { ... }" }

type BreakStatement struct{ Token lexer.Token }

func (bs *BreakStatement) statementNode()       {}
func (bs *BreakStatement) TokenLiteral() string { return bs.Token.Literal }
func (bs *BreakStatement) String() string       { return "break" }

type ContinueStatement struct{ Token lexer.Token }

func (cs *ContinueStatement) statementNode()       {}
func (cs *ContinueStatement) TokenLiteral() string { return cs.Token.Literal }
func (cs *ContinueStatement) String() string       { return "continue" }

type ImportStatement struct {
	Token lexer.Token
	Names []string
	Path  string
}

func (is *ImportStatement) statementNode()       {}
func (is *ImportStatement) TokenLiteral() string { return is.Token.Literal }
func (is *ImportStatement) String() string {
	return fmt.Sprintf("import { %s } from \"%s\"", strings.Join(is.Names, ", "), is.Path)
}

type ExportStatement struct {
	Token     lexer.Token
	Statement Statement // the fn/const/type being exported
}

func (es *ExportStatement) statementNode()       {}
func (es *ExportStatement) TokenLiteral() string { return es.Token.Literal }
func (es *ExportStatement) String() string       { return "export " + es.Statement.String() }

type TypeField struct {
	Name string
	Type string
}

type TypeDeclaration struct {
	Token  lexer.Token
	Name   *Identifier
	Fields []TypeField
}

func (td *TypeDeclaration) statementNode()       {}
func (td *TypeDeclaration) TokenLiteral() string { return td.Token.Literal }
func (td *TypeDeclaration) String() string       { return fmt.Sprintf("type %s = { ... }", td.Name.String()) }

type InterfaceMethod struct {
	Name   string
	Params []*TypedIdentifier
	Return string
}

type InterfaceDeclaration struct {
	Token   lexer.Token
	Name    *Identifier
	Methods []InterfaceMethod
}

func (id *InterfaceDeclaration) statementNode()       {}
func (id *InterfaceDeclaration) TokenLiteral() string { return id.Token.Literal }
func (id *InterfaceDeclaration) String() string       { return fmt.Sprintf("interface %s { ... }", id.Name.String()) }

// --- Expressions ---

type Identifier struct {
	Token lexer.Token
	Value string
}

func (i *Identifier) expressionNode()      {}
func (i *Identifier) TokenLiteral() string { return i.Token.Literal }
func (i *Identifier) String() string       { return i.Value }

type NumberLiteral struct {
	Token lexer.Token
	Value float64
}

func (nl *NumberLiteral) expressionNode()      {}
func (nl *NumberLiteral) TokenLiteral() string { return nl.Token.Literal }
func (nl *NumberLiteral) String() string       { return nl.Token.Literal }

type StringLiteral struct {
	Token lexer.Token
	Value string
}

func (sl *StringLiteral) expressionNode()      {}
func (sl *StringLiteral) TokenLiteral() string { return sl.Token.Literal }
func (sl *StringLiteral) String() string       { return fmt.Sprintf("\"%s\"", sl.Value) }

type StringInterpolation struct {
	Token lexer.Token
	Parts []Expression // alternating StringLiteral and expressions
}

func (si *StringInterpolation) expressionNode()      {}
func (si *StringInterpolation) TokenLiteral() string { return si.Token.Literal }
func (si *StringInterpolation) String() string       { return "\"...interpolated...\"" }

type BoolLiteral struct {
	Token lexer.Token
	Value bool
}

func (bl *BoolLiteral) expressionNode()      {}
func (bl *BoolLiteral) TokenLiteral() string { return bl.Token.Literal }
func (bl *BoolLiteral) String() string {
	if bl.Value {
		return "true"
	}
	return "false"
}

type NullLiteral struct {
	Token lexer.Token
}

func (nl *NullLiteral) expressionNode()      {}
func (nl *NullLiteral) TokenLiteral() string { return nl.Token.Literal }
func (nl *NullLiteral) String() string       { return "null" }

type ListLiteral struct {
	Token    lexer.Token
	Elements []Expression
}

func (ll *ListLiteral) expressionNode()      {}
func (ll *ListLiteral) TokenLiteral() string { return ll.Token.Literal }
func (ll *ListLiteral) String() string       { return "[...]" }

type MapEntry struct {
	Key   Expression
	Value Expression
}

type MapLiteral struct {
	Token   lexer.Token
	Entries []MapEntry
}

func (ml *MapLiteral) expressionNode()      {}
func (ml *MapLiteral) TokenLiteral() string { return ml.Token.Literal }
func (ml *MapLiteral) String() string       { return "{...}" }

type FunctionLiteral struct {
	Token    lexer.Token
	Params   []*TypedIdentifier
	Body     *BlockStatement
	ArrowExpr Expression // for fn(x) => expr
	IsArrow  bool
	Defaults map[string]Expression
}

func (fl *FunctionLiteral) expressionNode()      {}
func (fl *FunctionLiteral) TokenLiteral() string { return fl.Token.Literal }
func (fl *FunctionLiteral) String() string       { return "fn(...) { ... }" }

type CallExpression struct {
	Token     lexer.Token
	Function  Expression
	Arguments []Expression
	Named     map[string]Expression
}

func (ce *CallExpression) expressionNode()      {}
func (ce *CallExpression) TokenLiteral() string { return ce.Token.Literal }
func (ce *CallExpression) String() string {
	return ce.Function.String() + "(...)"
}

type MemberAccessExpression struct {
	Token    lexer.Token
	Object   Expression
	Property *Identifier
}

func (mae *MemberAccessExpression) expressionNode()      {}
func (mae *MemberAccessExpression) TokenLiteral() string { return mae.Token.Literal }
func (mae *MemberAccessExpression) String() string {
	return mae.Object.String() + "." + mae.Property.String()
}

type IndexExpression struct {
	Token lexer.Token
	Left  Expression
	Index Expression
}

func (ie *IndexExpression) expressionNode()      {}
func (ie *IndexExpression) TokenLiteral() string { return ie.Token.Literal }
func (ie *IndexExpression) String() string {
	return fmt.Sprintf("%s[%s]", ie.Left.String(), ie.Index.String())
}

type InfixExpression struct {
	Token    lexer.Token
	Left     Expression
	Operator string
	Right    Expression
}

func (ie *InfixExpression) expressionNode()      {}
func (ie *InfixExpression) TokenLiteral() string { return ie.Token.Literal }
func (ie *InfixExpression) String() string {
	return fmt.Sprintf("(%s %s %s)", ie.Left.String(), ie.Operator, ie.Right.String())
}

type PrefixExpression struct {
	Token    lexer.Token
	Operator string
	Right    Expression
}

func (pe *PrefixExpression) expressionNode()      {}
func (pe *PrefixExpression) TokenLiteral() string { return pe.Token.Literal }
func (pe *PrefixExpression) String() string {
	return fmt.Sprintf("(%s%s)", pe.Operator, pe.Right.String())
}

type ChannelReceiveExpression struct {
	Token   lexer.Token
	Channel Expression
}

func (cre *ChannelReceiveExpression) expressionNode()      {}
func (cre *ChannelReceiveExpression) TokenLiteral() string { return cre.Token.Literal }
func (cre *ChannelReceiveExpression) String() string       { return "<-" + cre.Channel.String() }

type ErrorPropagationExpression struct {
	Token lexer.Token
	Expr  Expression
}

func (epe *ErrorPropagationExpression) expressionNode()      {}
func (epe *ErrorPropagationExpression) TokenLiteral() string { return epe.Token.Literal }
func (epe *ErrorPropagationExpression) String() string       { return epe.Expr.String() + "?" }

type TypedIdentifier struct {
	Token          lexer.Token
	Name           string
	TypeAnnotation string
}

func (ti *TypedIdentifier) expressionNode()      {}
func (ti *TypedIdentifier) TokenLiteral() string { return ti.Token.Literal }
func (ti *TypedIdentifier) String() string {
	if ti.TypeAnnotation != "" {
		return fmt.Sprintf("%s: %s", ti.Name, ti.TypeAnnotation)
	}
	return ti.Name
}
