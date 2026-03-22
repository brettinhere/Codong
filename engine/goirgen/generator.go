package goirgen

import (
	"fmt"
	"strings"

	"github.com/codong-lang/codong/engine/parser"
)

// Generator converts a Codong AST to Go source code.
type Generator struct {
	indent   int
	output   strings.Builder
	declared map[string]bool // tracks declared variables
}

// Generate produces a complete Go program from a Codong AST.
func Generate(program *parser.Program) string {
	g := &Generator{declared: map[string]bool{}}
	g.output.WriteString(RuntimeSource)
	g.output.WriteString("\n\nfunc main() {\n")
	g.indent = 1
	for _, stmt := range program.Statements {
		g.genStatement(stmt)
	}
	g.output.WriteString("}\n")
	return g.output.String()
}

func (g *Generator) write(s string) {
	g.output.WriteString(strings.Repeat("\t", g.indent))
	g.output.WriteString(s)
	g.output.WriteString("\n")
}

func (g *Generator) writef(format string, args ...interface{}) {
	g.write(fmt.Sprintf(format, args...))
}

func (g *Generator) genStatement(stmt parser.Statement) {
	switch s := stmt.(type) {
	case *parser.ExpressionStatement:
		g.write(g.genExpr(s.Expression))
	case *parser.AssignStatement:
		name := s.Name.Value
		if g.declared[name] {
			g.writef("%s = %s", name, g.genExpr(s.Value))
		} else {
			g.declared[name] = true
			g.writef("var %s Value = %s", name, g.genExpr(s.Value))
		}
	case *parser.ConstStatement:
		g.declared[s.Name.Value] = true
		g.writef("var %s Value = %s", s.Name.Value, g.genExpr(s.Value))
	case *parser.CompoundAssignStatement:
		target := g.genExpr(s.Target)
		g.writef("%s %s %s", target, s.Operator, g.genExpr(s.Value))
	case *parser.PropertyAssignStatement:
		g.writef("cSet(%s, %q, %s)", g.genExpr(s.Object), s.Property.Value, g.genExpr(s.Value))
	case *parser.IndexAssignStatement:
		g.writef("cSetIndex(%s, %s, %s)", g.genExpr(s.Left), g.genExpr(s.Index), g.genExpr(s.Value))
	case *parser.ReturnStatement:
		if s.Value != nil {
			g.writef("return %s", g.genExpr(s.Value))
		} else {
			g.write("return nil")
		}
	case *parser.FunctionDefinition:
		g.genFuncDef(s)
	case *parser.IfStatement:
		g.genIf(s)
	case *parser.ForInStatement:
		g.genForIn(s)
	case *parser.WhileStatement:
		g.genWhile(s)
	case *parser.MatchStatement:
		g.genMatch(s)
	case *parser.TryCatchStatement:
		g.genTryCatch(s)
	case *parser.BreakStatement:
		g.write("break")
	case *parser.ContinueStatement:
		g.write("continue")
	case *parser.BlockStatement:
		for _, inner := range s.Statements {
			g.genStatement(inner)
		}
	case *parser.GoStatement:
		g.writef("go func() { %s }()", g.genExpr(s.Call))
	case *parser.ImportStatement, *parser.ExportStatement:
		// no-op in compiled mode
	case *parser.TypeDeclaration, *parser.InterfaceDeclaration:
		// no-op
	}
}

func (g *Generator) genFuncDef(s *parser.FunctionDefinition) {
	g.writef("var %s func(args ...Value) Value", s.Name.Value)
	g.writef("%s = func(args ...Value) Value {", s.Name.Value)
	g.indent++
	// Bind parameters
	for i, p := range s.Params {
		name := p.Name
		if s.Defaults != nil {
			if defExpr, ok := s.Defaults[name]; ok {
				g.writef("var %s Value; if len(args) > %d { %s = args[%d] } else { %s = %s }",
					name, i, name, i, name, g.genExpr(defExpr))
				continue
			}
		}
		g.writef("var %s Value; if len(args) > %d { %s = args[%d] }", name, i, name, i)
	}
	g.genBlock(s.Body)
	g.write("return nil")
	g.indent--
	g.write("}")
}

func (g *Generator) genBlock(block *parser.BlockStatement) {
	if block == nil {
		return
	}
	for _, stmt := range block.Statements {
		g.genStatement(stmt)
	}
}

func (g *Generator) genIf(s *parser.IfStatement) {
	cond := g.genExpr(s.Condition)
	g.writef("if isTruthy(%s) {", cond)
	g.indent++
	g.genBlock(s.Consequence)
	g.indent--
	if s.Alternative != nil {
		switch alt := s.Alternative.(type) {
		case *parser.BlockStatement:
			g.write("} else {")
			g.indent++
			g.genBlock(alt)
			g.indent--
		case *parser.IfStatement:
			g.output.WriteString(strings.Repeat("\t", g.indent))
			g.output.WriteString("} else ")
			g.genIf(alt)
			return
		}
	}
	g.write("}")
}

func (g *Generator) genForIn(s *parser.ForInStatement) {
	iter := g.genExpr(s.Iterable)
	varName := s.Variable.Value
	if !g.declared[varName] {
		g.writef("var %s Value", varName)
		g.declared[varName] = true
	}
	g.writef("for _, _item := range %s.(*CodongList).Elements {", iter)
	g.indent++
	g.writef("%s = _item", varName)
	g.genBlock(s.Body)
	g.indent--
	g.write("}")
}

func (g *Generator) genWhile(s *parser.WhileStatement) {
	cond := g.genExpr(s.Condition)
	g.writef("for isTruthy(%s) {", cond)
	g.indent++
	g.genBlock(s.Body)
	g.indent--
	g.write("}")
}

func (g *Generator) genMatch(s *parser.MatchStatement) {
	subj := g.genExpr(s.Subject)
	g.writef("_match_subj := %s", subj)
	for i, mc := range s.Cases {
		keyword := "if"
		if i > 0 {
			keyword = "} else if"
		}
		if mc.IsDefault {
			if i == 0 {
				g.write("{")
			} else {
				g.write("} else {")
			}
		} else {
			pattern := g.genExpr(mc.Pattern)
			g.writef("%s cEq(_match_subj, %s) {", keyword, pattern)
		}
		g.indent++
		if mc.BodyBlock != nil {
			g.genBlock(mc.BodyBlock)
		} else if mc.Body != nil {
			g.write(g.genExpr(mc.Body))
		}
		g.indent--
	}
	if len(s.Cases) > 0 {
		g.write("}")
	}
}

func (g *Generator) genTryCatch(s *parser.TryCatchStatement) {
	catchVar := s.CatchVar.Value
	g.write("func() {")
	g.indent++
	g.write("defer func() {")
	g.indent++
	g.writef("if _r := recover(); _r != nil {")
	g.indent++
	g.writef("if _rs, ok := _r.(*cReturnSignal); ok {")
	g.indent++
	g.writef("if _ce, ok := _rs.Value.(*CodongError); ok {")
	g.indent++
	g.writef("%s = _ce", catchVar)
	// Generate catch body
	g.genBlock(s.Catch)
	g.indent--
	g.write("}")
	g.indent--
	g.write("}")
	g.indent--
	g.write("}")
	g.indent--
	g.write("}()")
	// Generate try body
	g.genBlock(s.Try)
	g.indent--
	g.write("}()")
}

func (g *Generator) genExpr(expr parser.Expression) string {
	if expr == nil {
		return "nil"
	}
	switch e := expr.(type) {
	case *parser.NumberLiteral:
		return fmt.Sprintf("float64(%v)", e.Value)
	case *parser.StringLiteral:
		return fmt.Sprintf("%q", e.Value)
	case *parser.BoolLiteral:
		if e.Value {
			return "true"
		}
		return "false"
	case *parser.NullLiteral:
		return "nil"
	case *parser.Identifier:
		return g.genIdentifier(e.Value)
	case *parser.PrefixExpression:
		right := g.genExpr(e.Right)
		switch e.Operator {
		case "!":
			return fmt.Sprintf("!isTruthy(%s)", right)
		case "-":
			return fmt.Sprintf("(-toFloat(%s))", right)
		}
	case *parser.InfixExpression:
		left := g.genExpr(e.Left)
		right := g.genExpr(e.Right)
		return g.genInfix(e.Operator, left, right)
	case *parser.CallExpression:
		return g.genCall(e)
	case *parser.MemberAccessExpression:
		return g.genMemberAccess(e)
	case *parser.IndexExpression:
		return fmt.Sprintf("cIndex(%s, %s)", g.genExpr(e.Left), g.genExpr(e.Index))
	case *parser.ListLiteral:
		elems := make([]string, len(e.Elements))
		for i, el := range e.Elements {
			elems[i] = g.genExpr(el)
		}
		return fmt.Sprintf("cList(%s)", strings.Join(elems, ", "))
	case *parser.MapLiteral:
		kvs := make([]string, 0, len(e.Entries)*2)
		for _, entry := range e.Entries {
			kvs = append(kvs, fmt.Sprintf("%q", entry.Key), g.genExpr(entry.Value))
		}
		return fmt.Sprintf("cMap(%s)", strings.Join(kvs, ", "))
	case *parser.FunctionLiteral:
		return g.genFuncLiteral(e)
	case *parser.StringInterpolation:
		return g.genStringInterp(e)
	case *parser.ErrorPropagationExpression:
		return fmt.Sprintf("cPropagate(%s)", g.genExpr(e.Expr))
	}
	return "nil"
}

func (g *Generator) genIdentifier(name string) string {
	switch name {
	case "print":
		return "cPrint"
	case "type_of":
		return "typeOf"
	case "to_string":
		return "toString"
	case "to_number":
		return "toFloat"
	case "to_bool":
		return "toBool"
	case "range":
		return "cRange"
	case "true":
		return "true"
	case "false":
		return "false"
	case "null":
		return "nil"
	}
	return name
}

func (g *Generator) genInfix(op, left, right string) string {
	switch op {
	case "+":
		return fmt.Sprintf("cAdd(%s, %s)", left, right)
	case "-":
		return fmt.Sprintf("cSub(%s, %s)", left, right)
	case "*":
		return fmt.Sprintf("cMul(%s, %s)", left, right)
	case "/":
		return fmt.Sprintf("cDiv(%s, %s)", left, right)
	case "%":
		return fmt.Sprintf("cMod(%s, %s)", left, right)
	case "==":
		return fmt.Sprintf("cEq(%s, %s)", left, right)
	case "!=":
		return fmt.Sprintf("!cEq(%s, %s)", left, right)
	case "<":
		return fmt.Sprintf("cLt(%s, %s)", left, right)
	case ">":
		return fmt.Sprintf("cGt(%s, %s)", left, right)
	case "<=":
		return fmt.Sprintf("cLte(%s, %s)", left, right)
	case ">=":
		return fmt.Sprintf("cGte(%s, %s)", left, right)
	case "&&":
		return fmt.Sprintf("(isTruthy(%s) && isTruthy(%s))", left, right)
	case "||":
		return fmt.Sprintf("(isTruthy(%s) || isTruthy(%s))", left, right)
	}
	return "nil"
}

func (g *Generator) genCall(e *parser.CallExpression) string {
	// Check if it's a method call on a module: web.serve(), db.connect(), etc.
	if member, ok := e.Function.(*parser.MemberAccessExpression); ok {
		return g.genMethodCall(member, e.Arguments, e.Named)
	}

	fn := g.genExpr(e.Function)
	args := make([]string, len(e.Arguments))
	for i, a := range e.Arguments {
		args[i] = g.genExpr(a)
	}

	// Special cases
	switch fn {
	case "cPrint":
		if len(args) > 0 {
			return fmt.Sprintf("cPrint(%s)", args[0])
		}
		return "cPrint(nil)"
	case "cRange":
		if len(args) >= 2 {
			return fmt.Sprintf("cRange(toFloat(%s), toFloat(%s))", args[0], args[1])
		}
	}

	// Generic function call
	return fmt.Sprintf("%s(%s)", fn, strings.Join(args, ", "))
}

func (g *Generator) genMethodCall(member *parser.MemberAccessExpression, arguments []parser.Expression, named map[string]parser.Expression) string {
	obj := g.genExpr(member.Object)
	method := member.Property.Value
	args := make([]string, len(arguments))
	for i, a := range arguments {
		args[i] = g.genExpr(a)
	}

	// Module-level calls
	if ident, ok := member.Object.(*parser.Identifier); ok {
		switch ident.Value {
		case "web":
			return g.genWebCall(method, args, named)
		case "db":
			return g.genDbCall(method, args, named)
		case "http":
			return g.genHttpCall(method, args)
		case "error":
			return g.genErrorCall(method, args, named)
		}
	}

	// Object method call: obj.method(args)
	return fmt.Sprintf("cCall(%s, %q, %s)", obj, method, strings.Join(args, ", "))
}

func (g *Generator) genWebCall(method string, args []string, named map[string]parser.Expression) string {
	switch method {
	case "get":
		return fmt.Sprintf("cWebGet(%s, %s)", args[0], args[1])
	case "post":
		return fmt.Sprintf("cWebPost(%s, %s)", args[0], args[1])
	case "serve":
		port := "8080"
		if len(args) > 0 {
			port = args[0]
		}
		if named != nil {
			if p, ok := named["port"]; ok {
				port = fmt.Sprintf("int(toFloat(%s))", g.genExpr(p))
			}
		}
		return fmt.Sprintf("cWebServe(%s)", port)
	case "json":
		return fmt.Sprintf("cWebJson(%s)", args[0])
	case "text":
		return fmt.Sprintf("cWebText(%s)", args[0])
	case "html":
		return fmt.Sprintf("cWebHtml(%s)", args[0])
	}
	return "nil"
}

func (g *Generator) genDbCall(method string, args []string, named map[string]parser.Expression) string {
	switch method {
	case "connect":
		return fmt.Sprintf("cDbConnect(%s)", args[0])
	case "disconnect":
		return "cDbDisconnect()"
	case "find":
		if len(args) > 1 {
			return fmt.Sprintf("cDbFind(%s, %s.(*CodongMap))", args[0], args[1])
		}
		return fmt.Sprintf("cDbFind(%s, nil)", args[0])
	case "find_one":
		if len(args) > 1 {
			return fmt.Sprintf("cDbFindOne(%s, %s.(*CodongMap))", args[0], args[1])
		}
		return fmt.Sprintf("cDbFindOne(%s, nil)", args[0])
	case "insert":
		return fmt.Sprintf("cDbInsert(%s, %s.(*CodongMap))", args[0], args[1])
	case "update":
		return fmt.Sprintf("cDbUpdate(%s, %s.(*CodongMap), %s.(*CodongMap))", args[0], args[1], args[2])
	case "delete":
		return fmt.Sprintf("cDbDelete(%s, %s.(*CodongMap))", args[0], args[1])
	case "query":
		if len(args) > 1 {
			return fmt.Sprintf("cDbQuery(%s, %s.(*CodongList).Elements...)", args[0], args[1])
		}
		return fmt.Sprintf("cDbQuery(%s)", args[0])
	case "count":
		// Simplify: use raw query
		if len(args) > 1 {
			return fmt.Sprintf("cDbQuery(\"SELECT COUNT(*) as count FROM \"+toString(%s))", args[0])
		}
		return fmt.Sprintf("cDbQuery(\"SELECT COUNT(*) as count FROM \"+toString(%s))", args[0])
	}
	return "nil"
}

func (g *Generator) genHttpCall(method string, args []string) string {
	switch method {
	case "get":
		return fmt.Sprintf("cHttpGet(toString(%s))", args[0])
	case "post":
		if len(args) > 1 {
			return fmt.Sprintf("cHttpPost(toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cHttpPost(toString(%s), nil)", args[0])
	}
	return "nil"
}

func (g *Generator) genErrorCall(method string, args []string, named map[string]parser.Expression) string {
	switch method {
	case "new":
		opts := []string{}
		if named != nil {
			for k, v := range named {
				opts = append(opts, fmt.Sprintf("%q, %s", k, g.genExpr(v)))
			}
		}
		allArgs := strings.Join(args, ", ")
		if len(opts) > 0 {
			allArgs += ", " + strings.Join(opts, ", ")
		}
		return fmt.Sprintf("cError(%s)", allArgs)
	case "wrap":
		return fmt.Sprintf("func() *CodongError { e := cError(%s.(*CodongError).Code, toString(%s)); e.Cause = %s.(*CodongError); return e }()", args[0], args[1], args[0])
	case "is":
		return fmt.Sprintf("func() bool { e := %s.(*CodongError); for e != nil { if e.Code == toString(%s) { return true }; e = e.Cause }; return false }()", args[0], args[1])
	case "unwrap":
		return fmt.Sprintf("func() Value { if e, ok := %s.(*CodongError); ok && e.Cause != nil { return e.Cause }; return nil }()", args[0])
	}
	return "nil"
}

func (g *Generator) genFuncLiteral(e *parser.FunctionLiteral) string {
	params := g.genParams(e.Params)
	_ = params
	var body strings.Builder
	body.WriteString("func(args ...Value) Value {\n")
	g.indent++
	for i, p := range e.Params {
		name := p.Name
		g.output.WriteString("") // no-op
		body.WriteString(strings.Repeat("\t", g.indent))
		body.WriteString(fmt.Sprintf("var %s Value; if len(args) > %d { %s = args[%d] }\n", name, i, name, i))
	}
	if e.ArrowExpr != nil {
		body.WriteString(strings.Repeat("\t", g.indent))
		body.WriteString(fmt.Sprintf("return %s\n", g.genExpr(e.ArrowExpr)))
	} else if e.Body != nil {
		for _, stmt := range e.Body.Statements {
			body.WriteString(strings.Repeat("\t", g.indent))
			body.WriteString(g.genStatementStr(stmt))
			body.WriteString("\n")
		}
		body.WriteString(strings.Repeat("\t", g.indent))
		body.WriteString("return nil\n")
	}
	g.indent--
	body.WriteString(strings.Repeat("\t", g.indent))
	body.WriteString("}")
	return body.String()
}

func (g *Generator) genStatementStr(stmt parser.Statement) string {
	// Simple inline statement generation for function bodies
	switch s := stmt.(type) {
	case *parser.ReturnStatement:
		if s.Value != nil {
			return fmt.Sprintf("return %s", g.genExpr(s.Value))
		}
		return "return nil"
	case *parser.ExpressionStatement:
		return g.genExpr(s.Expression)
	case *parser.AssignStatement:
		return fmt.Sprintf("var %s Value = %s", s.Name.Value, g.genExpr(s.Value))
	case *parser.CompoundAssignStatement:
		return fmt.Sprintf("%s %s %s", g.genExpr(s.Target), s.Operator, g.genExpr(s.Value))
	case *parser.PropertyAssignStatement:
		return fmt.Sprintf("cSet(%s, %q, %s)", g.genExpr(s.Object), s.Property.Value, g.genExpr(s.Value))
	}
	return "// unsupported statement"
}

func (g *Generator) genStringInterp(e *parser.StringInterpolation) string {
	parts := make([]string, len(e.Parts))
	for i, part := range e.Parts {
		if sl, ok := part.(*parser.StringLiteral); ok {
			parts[i] = fmt.Sprintf("%q", sl.Value)
		} else {
			parts[i] = fmt.Sprintf("toString(%s)", g.genExpr(part))
		}
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, " + ")
}

func (g *Generator) genMemberAccess(e *parser.MemberAccessExpression) string {
	obj := g.genExpr(e.Object)
	prop := e.Property.Value
	return fmt.Sprintf("cGet(%s, %q)", obj, prop)
}

func (g *Generator) genParams(params []*parser.TypedIdentifier) []string {
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	return names
}
