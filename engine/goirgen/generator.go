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
	// Recover unhandled ? propagation
	g.write("defer func() {")
	g.indent++
	g.write("if r := recover(); r != nil {")
	g.indent++
	g.write("if rs, ok := r.(*cReturnSignal); ok {")
	g.indent++
	g.write("if ce, ok := rs.Value.(*CodongError); ok {")
	g.indent++
	g.write("fmt.Fprintln(os.Stderr, ce.Error())")
	g.write("os.Exit(1)")
	g.indent--
	g.write("}")
	g.indent--
	g.write("}")
	g.write("panic(r)")
	g.indent--
	g.write("}")
	g.indent--
	g.write("}()")
	// Forward-declare all top-level functions (enables mutual recursion)
	for _, stmt := range program.Statements {
		if fd, ok := stmt.(*parser.FunctionDefinition); ok {
			g.writef("var %s func(args ...Value) Value", fd.Name.Value)
			g.declared[fd.Name.Value] = true
		}
	}
	for _, stmt := range program.Statements {
		g.genStatement(stmt)
	}
	// Start web servers after all routes are registered
	g.write("cWebServeAll()")
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
		expr := g.genExpr(s.Expression)
		if strings.HasPrefix(expr, "cPrint(") || strings.HasPrefix(expr, "cPrintV(") {
			g.write(expr)
		} else {
			g.writef("cDiscard(%s)", expr)
		}
	case *parser.AssignStatement:
		name := s.Name.Value
		if name == "_" {
			g.writef("_ = %s", g.genExpr(s.Value))
		} else if g.declared[name] {
			g.writef("%s = %s", name, g.genExpr(s.Value))
		} else {
			g.declared[name] = true
			g.writef("var %s Value = %s; _ = %s", name, g.genExpr(s.Value), name)
		}
	case *parser.ConstStatement:
		g.declared[s.Name.Value] = true
		g.writef("var %s Value = %s", s.Name.Value, g.genExpr(s.Value))
	case *parser.CompoundAssignStatement:
		val := g.genExpr(s.Value)
		opFn := map[string]string{"+=": "cAdd", "-=": "cSub", "*=": "cMul", "/=": "cDiv"}[s.Operator]
		switch t := s.Target.(type) {
		case *parser.IndexExpression:
			obj := g.genExpr(t.Left)
			idx := g.genExpr(t.Index)
			g.writef("cSetIndex(%s, %s, %s(cIndex(%s, %s), %s))", obj, idx, opFn, obj, idx, val)
		case *parser.MemberAccessExpression:
			obj := g.genExpr(t.Object)
			prop := t.Property.Value
			g.writef("cSet(%s, %q, %s(cGet(%s, %q), %s))", obj, prop, opFn, obj, prop, val)
		default:
			target := g.genExpr(s.Target)
			g.writef("%s = %s(%s, %s)", target, opFn, target, val)
		}
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
	if !g.declared[s.Name.Value] {
		g.writef("var %s func(args ...Value) Value", s.Name.Value)
		g.declared[s.Name.Value] = true
	}
	g.writef("%s = func(args ...Value) Value {", s.Name.Value)
	g.indent++
	// Bind parameters
	for i, p := range s.Params {
		name := p.Name
		if s.Defaults != nil {
			if defExpr, ok := s.Defaults[name]; ok {
				g.writef("var %s Value; if len(args) > %d { %s = args[%d] } else { %s = %s }; _ = %s",
					name, i, name, i, name, g.genExpr(defExpr), name)
				continue
			}
		}
		g.writef("var %s Value; if len(args) > %d { %s = args[%d] }; _ = %s", name, i, name, i, name)
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
	g.writef("for _, _item := range toList(%s).Elements {", iter)
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
	if subj == "nil" {
		g.write("var _match_subj Value = nil")
	} else {
		g.writef("var _match_subj Value = %s", subj)
	}
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
	if !g.declared[catchVar] {
		g.writef("var %s Value", catchVar)
		g.declared[catchVar] = true
	}
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
			// Extract key string: Identifier → name, StringLiteral → value
			var keyStr string
			switch k := entry.Key.(type) {
			case *parser.Identifier:
				keyStr = k.Value
			case *parser.StringLiteral:
				keyStr = k.Value
			default:
				keyStr = entry.Key.String()
			}
			kvs = append(kvs, fmt.Sprintf("%q", keyStr), g.genExpr(entry.Value))
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
	case "_":
		return "_blank"
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

	// Built-in functions — direct call (not cCallFn)
	switch fn {
	case "cPrint":
		if len(args) > 0 {
			return fmt.Sprintf("cPrintV(%s)", args[0])
		}
		return "cPrintV(nil)"
	case "cRange":
		if len(args) >= 2 {
			return fmt.Sprintf("cRange(toFloat(%s), toFloat(%s))", args[0], args[1])
		}
	case "typeOf":
		return fmt.Sprintf("typeOf(%s)", args[0])
	case "toString":
		return fmt.Sprintf("toString(%s)", args[0])
	case "toFloat":
		if len(args) > 0 {
			return fmt.Sprintf("toNumber(%s)", args[0])
		}
	case "toBool":
		return fmt.Sprintf("toBoolV(%s)", args[0])
	}

	// Append named args as trailing map
	if e.Named != nil && len(e.Named) > 0 {
		namedParts := []string{}
		for k, v := range e.Named {
			namedParts = append(namedParts, fmt.Sprintf("%q, %s", k, g.genExpr(v)))
		}
		args = append(args, fmt.Sprintf("cMap(%s)", strings.Join(namedParts, ", ")))
	}

	// Generic function call — use cCallFn for dynamic dispatch
	if len(args) == 0 {
		return fmt.Sprintf("cCallFn(%s)", fn)
	}
	return fmt.Sprintf("cCallFn(%s, %s)", fn, strings.Join(args, ", "))
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
		case "llm":
			return g.genLlmCall(method, args, named)
		}
	}

	// Server/group object method calls are handled at runtime by cCall
	// which checks _type on the map. No special-casing needed here.

	// Object method call: obj.method(args)
	return fmt.Sprintf("cCall(%s, %q, %s)", obj, method, strings.Join(args, ", "))
}

func (g *Generator) genWebCall(method string, args []string, named map[string]parser.Expression) string {
	switch method {
	case "get", "post", "put", "delete", "patch":
		goMethod := strings.ToUpper(method)
		return fmt.Sprintf("cWebRoute(\"%s\", %s, %s)", goMethod, args[0], args[1])
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
		return fmt.Sprintf("cWebMakeServer(%s)", port)
	case "json":
		return fmt.Sprintf("cWebJson(%s)", strings.Join(args, ", "))
	case "text":
		return fmt.Sprintf("cWebText(%s)", args[0])
	case "html":
		return fmt.Sprintf("cWebHtml(%s)", args[0])
	case "redirect":
		return fmt.Sprintf("cMap(\"_type\", \"redirect\", \"url\", %s, \"status\", float64(302))", args[0])
	case "response":
		return fmt.Sprintf("cWebResponse(%s)", strings.Join(args, ", "))
	}
	return "nil"
}

func (g *Generator) genDbCall(method string, args []string, named map[string]parser.Expression) string {
	switch method {
	case "connect":
		return fmt.Sprintf("cDbConnect(toString(%s))", args[0])
	case "disconnect":
		return "cDbDisconnectRT()"
	case "find":
		if len(args) > 1 {
			return fmt.Sprintf("cDbFind(toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cDbFind(toString(%s), nil)", args[0])
	case "find_one":
		if len(args) > 1 {
			return fmt.Sprintf("cDbFindOne(toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cDbFindOne(toString(%s), nil)", args[0])
	case "insert":
		return fmt.Sprintf("cDbInsert(toString(%s), %s)", args[0], args[1])
	case "update":
		return fmt.Sprintf("cDbUpdate(toString(%s), %s, %s)", args[0], args[1], args[2])
	case "delete":
		return fmt.Sprintf("cDbDelete(toString(%s), %s)", args[0], args[1])
	case "query":
		if len(args) > 1 {
			return fmt.Sprintf("cDbQuery(toString(%s), toList(%s).Elements...)", args[0], args[1])
		}
		return fmt.Sprintf("cDbQuery(toString(%s))", args[0])
	case "count":
		if len(args) > 1 {
			return fmt.Sprintf("cDbCount(toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cDbCount(toString(%s), nil)", args[0])
	case "exists":
		if len(args) > 1 {
			return fmt.Sprintf("cDbExists(toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cDbExists(toString(%s), nil)", args[0])
	}
	return "nil"
}

func (g *Generator) genHttpCall(method string, args []string) string {
	switch method {
	case "get":
		if len(args) > 1 {
			return fmt.Sprintf("cHttpDo(\"GET\", toString(%s), nil, %s)", args[0], args[1])
		}
		return fmt.Sprintf("cHttpGet(toString(%s))", args[0])
	case "post":
		if len(args) > 1 {
			return fmt.Sprintf("cHttpPost(toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cHttpPost(toString(%s), nil)", args[0])
	case "put":
		if len(args) > 1 {
			return fmt.Sprintf("cHttpDo(\"PUT\", toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cHttpDo(\"PUT\", toString(%s), nil)", args[0])
	case "delete":
		return fmt.Sprintf("cHttpDo(\"DELETE\", toString(%s), nil)", args[0])
	case "patch":
		if len(args) > 1 {
			return fmt.Sprintf("cHttpDo(\"PATCH\", toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cHttpDo(\"PATCH\", toString(%s), nil)", args[0])
	case "request":
		if len(args) >= 2 {
			if len(args) > 2 {
				return fmt.Sprintf("cHttpDo(toString(%s), toString(%s), %s)", args[0], args[1], args[2])
			}
			return fmt.Sprintf("cHttpDo(toString(%s), toString(%s), nil)", args[0], args[1])
		}
	}
	return "nil"
}

func (g *Generator) genLlmCall(method string, args []string, named map[string]parser.Expression) string {
	switch method {
	case "ask":
		allArgs := make([]string, len(args))
		copy(allArgs, args)
		if named != nil {
			namedParts := []string{}
			for k, v := range named {
				namedParts = append(namedParts, fmt.Sprintf("%q, %s", k, g.genExpr(v)))
			}
			if len(namedParts) > 0 {
				allArgs = append(allArgs, fmt.Sprintf("cMap(%s)", strings.Join(namedParts, ", ")))
			}
		}
		return fmt.Sprintf("cLlmAsk(%s)", strings.Join(allArgs, ", "))
	case "count_tokens":
		if len(args) > 0 {
			return fmt.Sprintf("cLlmCountTokens(toString(%s))", args[0])
		}
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
	case "to_json":
		return fmt.Sprintf("cErrorToJson(%s)", args[0])
	case "to_compact":
		return fmt.Sprintf("cErrorToCompact(%s)", args[0])
	case "set_format":
		return fmt.Sprintf("cErrorSetFormat(%s)", args[0])
	case "handle":
		if len(args) >= 2 {
			return fmt.Sprintf("cErrorHandle(%s, %s)", args[0], args[1])
		}
	case "retry":
		if len(args) >= 2 {
			return fmt.Sprintf("cErrorRetry(%s, %s)", args[0], args[1])
		}
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
		body.WriteString(strings.Repeat("\t", g.indent))
		body.WriteString(fmt.Sprintf("var %s Value; if len(args) > %d { %s = args[%d] }; _ = %s\n", name, i, name, i, name))
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
	// Module member access without call (e.g., web.json as reference)
	if ident, ok := e.Object.(*parser.Identifier); ok {
		switch ident.Value {
		case "web":
			switch e.Property.Value {
			case "json":
				return "func(args ...Value) Value { return cWebJson(args...) }"
			case "text":
				return "func(args ...Value) Value { return cWebText(args[0]) }"
			case "html":
				return "func(args ...Value) Value { return cWebHtml(args[0]) }"
			case "middleware":
				return "cWebMiddlewareNS"
			}
		}
	}
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
