package goirgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/codong-lang/codong/engine/lexer"
	"github.com/codong-lang/codong/engine/parser"
)

// Generator converts a Codong AST to Go source code.
type Generator struct {
	indent      int
	output      strings.Builder
	declared    map[string]bool // tracks declared variables
	consts      map[string]bool // tracks const bindings
	inTryCatch  bool            // true when generating code inside a try/catch block
	inLoop      bool            // true when generating code inside a for/while loop
	tcCounter   int             // unique counter for try/catch flow control variables
	sourceDir   string          // directory of the main .cod file (for resolving imports)
	imported    map[string]bool // tracks already imported files to prevent cycles
}

// Generate produces a complete Go program from a Codong AST.
// sourceDir is used to resolve import paths (optional, defaults to cwd).
func Generate(program *parser.Program, sourceDirs ...string) string {
	srcDir := ""
	if len(sourceDirs) > 0 {
		srcDir = sourceDirs[0]
	}
	g := &Generator{declared: map[string]bool{}, consts: map[string]bool{}, sourceDir: srcDir, imported: map[string]bool{}}
	g.output.WriteString(RuntimeSource)
	g.output.WriteString("\n\nfunc main() {\n")
	g.indent = 1
	// Set cFsWorkDir to source file's directory for relative path resolution
	// But do NOT chdir - keep the original working directory for fs.cwd()
	if srcDir != "" {
		g.write(fmt.Sprintf("cFsWorkDir = %q", srcDir))
	}
	// Recover unhandled ? propagation
	g.write("defer func() {")
	g.indent++
	g.write("if r := recover(); r != nil {")
	g.indent++
	g.write("if ce, ok := r.(*CodongError); ok {")
	g.indent++
	g.write("cPanicExit(ce)")
	g.indent--
	g.write("}")
	g.write("if rs, ok := r.(*cReturnSignal); ok {")
	g.indent++
	g.write("if ce, ok := rs.Value.(*CodongError); ok {")
	g.indent++
	g.write("cPanicExit(ce)")
	g.indent--
	g.write("}")
	g.indent--
	g.write("}")
	g.write("panic(r)")
	g.indent--
	g.write("}")
	g.indent--
	g.write("}()")
	// Forward-declare all top-level functions (enables mutual recursion + hoisting)
	for _, stmt := range program.Statements {
		if fd, ok := stmt.(*parser.FunctionDefinition); ok {
			goName := escapeGoName(fd.Name.Value)
			g.writef("var %s func(args ...Value) Value; _ = %s", goName, goName)
			g.declared[fd.Name.Value] = true
		}
	}
	// Pre-scan all top-level assignments and pre-declare them so function bodies
	// can close over them (Go closures capture by reference; variable must exist before closure body executes)
	for _, stmt := range program.Statements {
		switch s := stmt.(type) {
		case *parser.FunctionDefinition:
			// already forward-declared above
		case *parser.AssignStatement:
			if !g.declared[s.Name.Value] {
				gn := escapeGoName(s.Name.Value)
				g.writef("var %s Value; _ = %s", gn, gn)
				g.declared[s.Name.Value] = true
			}
		case *parser.ConstStatement:
			if !g.declared[s.Name.Value] {
				gn := escapeGoName(s.Name.Value)
				g.writef("var %s Value; _ = %s", gn, gn)
				g.declared[s.Name.Value] = true
			}
		}
	}
	// Pass 1: emit import statements first so imported symbols are available
	for _, stmt := range program.Statements {
		if _, ok := stmt.(*parser.ImportStatement); ok {
			g.genStatement(stmt)
		}
	}
	// Pass 2: hoist all function definitions so they can be called in any order
	for _, stmt := range program.Statements {
		if _, ok := stmt.(*parser.FunctionDefinition); ok {
			g.genStatement(stmt)
		}
	}
	// Pass 3: emit all remaining statements (non-import, non-fn)
	for _, stmt := range program.Statements {
		switch stmt.(type) {
		case *parser.ImportStatement, *parser.FunctionDefinition:
			// already emitted
		default:
			g.genStatement(stmt)
		}
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
		} else if strings.HasPrefix(expr, "cPropagate(") {
			// Standalone ? expression: propagate error (panic) instead of assign
			inner := expr[len("cPropagate("):len(expr)-1]
			g.writef("cPropagateStmt(%s)", inner)
		} else {
			g.writef("cDiscard(%s)", expr)
		}
	case *parser.AssignStatement:
		name := s.Name.Value
		goName := escapeGoName(name)
		if name == "_" {
			g.writef("_ = %s", g.genExpr(s.Value))
		} else if g.consts[name] {
			// const reassignment → runtime error
			g.writef("cPrintError(\"E1001_SYNTAX_ERROR\", \"cannot assign to const '%s'\", \"remove const declaration or use a different variable name\")", name)
		} else if g.declared[name] || g.consts[name] {
			g.writef("%s = %s", goName, g.genExpr(s.Value))
		} else {
			g.declared[name] = true
			g.writef("var %s Value = %s; _ = %s", goName, g.genExpr(s.Value), goName)
		}
	case *parser.ConstStatement:
		goName := escapeGoName(s.Name.Value)
		if g.declared[s.Name.Value] {
			// Already declared (e.g., outer scope const) — just reassign in this scope
			g.writef("%s = %s", goName, g.genExpr(s.Value))
		} else {
			g.declared[s.Name.Value] = true
			g.writef("var %s Value = %s; _ = %s", goName, g.genExpr(s.Value), goName)
		}
		g.consts[s.Name.Value] = true
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
		if g.inTryCatch {
			// Inside try/catch closure, use panic to propagate return to outer function
			if s.Value != nil {
				g.writef("panic(&cReturnSignal{Value: %s})", g.genExpr(s.Value))
			} else {
				g.write("panic(&cReturnSignal{Value: nil})")
			}
		} else {
			if s.Value != nil {
				g.writef("return %s", g.genExpr(s.Value))
			} else {
				g.write("return nil")
			}
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
		if g.inTryCatch {
			g.write("panic(\"__codong_break__\")")
		} else {
			g.write("break")
		}
	case *parser.ContinueStatement:
		if g.inTryCatch {
			g.write("panic(\"__codong_continue__\")")
		} else {
			g.write("continue")
		}
	case *parser.BlockStatement:
		for _, inner := range s.Statements {
			g.genStatement(inner)
		}
	case *parser.GoStatement:
		g.writef("go func() { %s }()", g.genExpr(s.Call))
	case *parser.ImportStatement:
		g.genImport(s)
	case *parser.ExportStatement:
		// Export is transparent in compiled mode — just compile the inner statement
		if s.Statement != nil {
			g.genStatement(s.Statement)
		}
	case *parser.TypeDeclaration, *parser.InterfaceDeclaration:
		// no-op
	}
}

// genImport handles import statements by reading, parsing, and inlining the imported .cod file.
// Only exported (via `export` keyword) functions/consts are made available by their name.
func (g *Generator) genImport(s *parser.ImportStatement) {
	if g.sourceDir == "" {
		g.write("// import: source directory not set, skipping " + s.Path)
		return
	}

	// Resolve the import path relative to sourceDir
	importPath := s.Path
	if !filepath.IsAbs(importPath) {
		importPath = filepath.Join(g.sourceDir, importPath)
	}
	importPath = filepath.Clean(importPath)

	// Prevent circular imports
	if g.imported[importPath] {
		return
	}
	g.imported[importPath] = true

	// Read the imported file
	source, err := os.ReadFile(importPath)
	if err != nil {
		g.writef("// import error: cannot read %s: %v", s.Path, err)
		return
	}

	// Parse the imported file
	l := lexer.New(string(source))
	p := parser.New(l)
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		g.writef("// import error: parse errors in %s", s.Path)
		return
	}

	// Collect exported names
	exportedNames := map[string]bool{}
	for _, stmt := range program.Statements {
		if es, ok := stmt.(*parser.ExportStatement); ok {
			switch inner := es.Statement.(type) {
			case *parser.FunctionDefinition:
				exportedNames[inner.Name.Value] = true
			case *parser.ConstStatement:
				exportedNames[inner.Name.Value] = true
			case *parser.AssignStatement:
				exportedNames[inner.Name.Value] = true
			}
		}
	}

	// Build set of requested names
	requestedNames := map[string]bool{}
	for _, name := range s.Names {
		requestedNames[name] = true
	}

	g.writef("// --- import from %s ---", s.Path)

	// Process the imported file's statements
	// Handle nested imports first
	importDir := filepath.Dir(importPath)
	oldSourceDir := g.sourceDir
	g.sourceDir = importDir

	for _, stmt := range program.Statements {
		switch inner := stmt.(type) {
		case *parser.ImportStatement:
			g.genImport(inner)
		case *parser.ExportStatement:
			// Generate the inner statement (it will be visible in scope)
			g.genStatement(inner.Statement)
		case *parser.FunctionDefinition:
			// Only generate non-exported helpers that are directly requested
			// (exported functions were already generated by the ExportStatement handler above)
			if !exportedNames[inner.Name.Value] && requestedNames[inner.Name.Value] {
				g.genStatement(stmt)
			}
		default:
			// Generate all top-level statements (they may have side effects)
			g.genStatement(stmt)
		}
	}

	g.sourceDir = oldSourceDir
	// Create aliases for "import { x as y }" and ensure all names are "used"
	for _, name := range s.Names {
		goName := escapeGoName(name)
		if alias, hasAlias := s.Aliases[name]; hasAlias {
			aliasGo := escapeGoName(alias)
			if !g.declared[alias] {
				g.writef("var %s = %s", aliasGo, goName)
				g.declared[alias] = true
			} else {
				g.writef("%s = %s", aliasGo, goName)
			}
			g.writef("_ = %s", aliasGo)
		}
		if exportedNames[name] {
			g.writef("_ = %s", goName)
		}
	}
	g.writef("// --- end import from %s ---", s.Path)
}

func (g *Generator) genFuncDef(s *parser.FunctionDefinition) {
	goName := escapeGoName(s.Name.Value)
	if !g.declared[s.Name.Value] {
		g.writef("var %s func(args ...Value) Value", goName)
		g.declared[s.Name.Value] = true
	}
	// Named function definitions create isolated scope (no closure capture of assignments)
	outerDeclared := g.declared
	g.declared = map[string]bool{}
	// Copy param names as declared
	for _, p := range s.Params { g.declared[p.Name] = true }
	// Also copy outer function names so they can be called
	for k := range outerDeclared { g.declared[k] = true }
	g.writef("%s = func(args ...Value) (_ret Value) {", goName)
	g.indent++
	// Recover ? propagation — return error instead of panicking
	g.write("defer func() {")
	g.indent++
	g.write("if _r := recover(); _r != nil {")
	g.indent++
	g.write("if _rs, ok := _r.(*cReturnSignal); ok { _ret = _rs.Value; return }")
	g.write("panic(_r)")
	g.indent--
	g.write("}")
	g.indent--
	g.write("}()")
	// Pre-declare all new variables in function body (skip outer-scope captures)
	bodyVars := collectAssignedVars(s.Body)
	for _, p := range s.Params { delete(bodyVars, p.Name) }
	for v := range bodyVars {
		if outerDeclared[v] { continue } // captured from outer scope — not a new local
		gn := escapeGoName(v)
		g.writef("var %s Value; _ = %s", gn, gn)
		g.declared[v] = true
	}
	// Extract named args map (last arg if it's a CodongMap with named params)
	g.write("var _named *CodongMap; if len(args) > 0 { if _nm, ok := args[len(args)-1].(*CodongMap); ok { _named = _nm } }; _ = _named")
	// Bind parameters
	for i, p := range s.Params {
		name := escapeGoName(p.Name)
		origName := p.Name
		if s.Defaults != nil {
			if defExpr, ok := s.Defaults[origName]; ok {
				g.writef("var %s Value; if len(args) > %d { %s = args[%d] } else { %s = %s }; if _named != nil { if _nv, ok := _named.Entries[%q]; ok { %s = _nv } }; _ = %s",
					name, i, name, i, name, g.genExpr(defExpr), origName, name, name)
				continue
			}
		}
		g.writef("var %s Value; if len(args) > %d { %s = args[%d] }; if _named != nil { if _nv, ok := _named.Entries[%q]; ok { %s = _nv } }; _ = %s", name, i, name, i, origName, name, name)
	}
	g.genBlock(s.Body)
	g.write("return nil")
	g.indent--
	g.write("}")
	// Restore outer declared state
	g.declared = outerDeclared
}

// collectAssignedVars finds all variable names assigned in a block (recursively).
func collectAssignedVars(block *parser.BlockStatement) map[string]bool {
	vars := map[string]bool{}
	if block == nil { return vars }
	collectVarsFromStmts(block.Statements, vars)
	return vars
}

func collectVarsFromStmts(stmts []parser.Statement, vars map[string]bool) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *parser.AssignStatement:
			vars[s.Name.Value] = true
		case *parser.ConstStatement:
			vars[s.Name.Value] = true
		case *parser.IfStatement:
			if s.Consequence != nil { collectVarsFromStmts(s.Consequence.Statements, vars) }
			if alt, ok := s.Alternative.(*parser.BlockStatement); ok {
				collectVarsFromStmts(alt.Statements, vars)
			}
			if alt, ok := s.Alternative.(*parser.IfStatement); ok {
				collectVarsFromIf(alt, vars)
			}
		case *parser.ForInStatement:
			vars[s.Variable.Value] = true
			if s.Body != nil { collectVarsFromStmts(s.Body.Statements, vars) }
		case *parser.WhileStatement:
			if s.Body != nil { collectVarsFromStmts(s.Body.Statements, vars) }
		case *parser.BlockStatement:
			collectVarsFromStmts(s.Statements, vars)
		case *parser.TryCatchStatement:
			if s.Try != nil { collectVarsFromStmts(s.Try.Statements, vars) }
			if s.Catch != nil { collectVarsFromStmts(s.Catch.Statements, vars) }
			vars[s.CatchVar.Value] = true
		case *parser.MatchStatement:
			for _, mc := range s.Cases {
				if mc.BodyBlock != nil { collectVarsFromStmts(mc.BodyBlock.Statements, vars) }
			}
		}
	}
}

func collectVarsFromIf(s *parser.IfStatement, vars map[string]bool) {
	if s.Consequence != nil { collectVarsFromStmts(s.Consequence.Statements, vars) }
	if alt, ok := s.Alternative.(*parser.BlockStatement); ok {
		collectVarsFromStmts(alt.Statements, vars)
	}
	if alt, ok := s.Alternative.(*parser.IfStatement); ok {
		collectVarsFromIf(alt, vars)
	}
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
	goVarName := escapeGoName(varName)
	if !g.declared[varName] {
		g.writef("var %s Value", goVarName)
		g.declared[varName] = true
	}
	prevInLoop := g.inLoop
	g.inLoop = true
	g.writef("for _, _item := range toList(%s).Elements {", iter)
	g.indent++
	g.writef("%s = _item", goVarName)
	g.genBlock(s.Body)
	g.indent--
	g.write("}")
	g.inLoop = prevInLoop
	g.writef("_ = %s", goVarName)
}

func (g *Generator) genWhile(s *parser.WhileStatement) {
	prevInLoop := g.inLoop
	g.inLoop = true
	cond := g.genExpr(s.Condition)
	g.writef("for isTruthy(%s) {", cond)
	g.indent++
	g.genBlock(s.Body)
	g.indent--
	g.write("}")
	g.inLoop = prevInLoop
}

func (g *Generator) genMatch(s *parser.MatchStatement) {
	subj := g.genExpr(s.Subject)
	// Wrap in a block to avoid _match_subj redeclare when multiple match statements exist
	g.write("{")
	g.indent++
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
	g.indent--
	g.write("}")
}

func (g *Generator) genTryCatch(s *parser.TryCatchStatement) {
	catchVar := s.CatchVar.Value
	goVar := escapeGoName(catchVar)
	if !g.declared[catchVar] {
		g.writef("var %s Value", goVar)
		g.declared[catchVar] = true
	}
	g.writef("_ = %s", goVar)
	prevInTryCatch := g.inTryCatch
	g.inTryCatch = true
	// Use unique variable name for each try/catch to avoid redeclaration
	tcIdx := g.tcCounter
	g.tcCounter++
	flowVar := fmt.Sprintf("_tcFlowCtl%d", tcIdx)
	g.writef("var %s string; _ = %s", flowVar, flowVar)
	g.write("func() {")
	g.indent++
	g.write("defer func() {")
	g.indent++
	g.writef("if _r := recover(); _r != nil {")
	g.indent++
	// Catch raw *CodongError panics (e.g., division by zero, stack overflow)
	g.writef("if _ce, ok := _r.(*CodongError); ok {")
	g.indent++
	g.writef("%s = _ce", goVar)
	// Save declared state before first catch generation
	declaredBefore := make(map[string]bool)
	for k, v := range g.declared { declaredBefore[k] = v }
	g.genBlock(s.Catch)
	g.write("return")
	g.indent--
	g.write("}")
	// Catch cReturnSignal-wrapped errors (e.g., ? operator)
	g.writef("if _rs, ok := _r.(*cReturnSignal); ok {")
	g.indent++
	g.writef("if _ce, ok := _rs.Value.(*CodongError); ok {")
	g.indent++
	g.writef("%s = _ce", goVar)
	// Restore declared state so catch block vars are re-declared in this branch
	g.declared = declaredBefore
	g.genBlock(s.Catch)
	g.write("return")
	g.indent--
	g.write("}")
	// Non-error return signal — re-panic to propagate to enclosing function
	g.write("panic(_r)")
	g.indent--
	g.write("}")
	// Handle break/continue signals from inside try block — propagate via flag
	g.writef("if _bs, ok := _r.(string); ok && (_bs == \"__codong_break__\" || _bs == \"__codong_continue__\") { %s = _bs; return }", flowVar)
	// Re-panic for non-error panics
	g.write("panic(_r)")
	g.indent--
	g.write("}")
	g.indent--
	g.write("}()")
	// Generate try body
	g.genBlock(s.Try)
	g.indent--
	g.write("}()")
	// After try/catch closure — check break/continue flag (only if inside a loop)
	if g.inLoop {
		g.writef("if %s == \"__codong_break__\" { break }", flowVar)
		g.writef("if %s == \"__codong_continue__\" { continue }", flowVar)
	}
	g.inTryCatch = prevInTryCatch
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
	case *parser.MatchStatement:
		return g.genMatchExpr(e)
	}
	return "nil"
}

// genMatchExpr generates a match statement used as an expression (e.g. return match n {...}).
// It wraps the match in an IIFE that returns the matched value.
func (g *Generator) genMatchExpr(s *parser.MatchStatement) string {
	var sb strings.Builder
	sb.WriteString("func() Value {")
	subj := g.genExpr(s.Subject)
	sb.WriteString(fmt.Sprintf(" var _ms Value = %s;", subj))
	for i, mc := range s.Cases {
		if mc.IsDefault {
			if i > 0 {
				sb.WriteString("} else {")
			} else {
				sb.WriteString("{")
			}
		} else {
			pattern := g.genExpr(mc.Pattern)
			if i == 0 {
				sb.WriteString(fmt.Sprintf(" if cEq(_ms, %s) {", pattern))
			} else {
				sb.WriteString(fmt.Sprintf(" } else if cEq(_ms, %s) {", pattern))
			}
		}
		if mc.BodyBlock != nil && len(mc.BodyBlock.Statements) > 0 {
			// The arm is a block; generate last expression as return value
			lastStmt := mc.BodyBlock.Statements[len(mc.BodyBlock.Statements)-1]
			if exprStmt, ok := lastStmt.(*parser.ExpressionStatement); ok {
				sb.WriteString(fmt.Sprintf(" return %s;", g.genExpr(exprStmt.Expression)))
			} else if retStmt, ok := lastStmt.(*parser.ReturnStatement); ok && retStmt.Value != nil {
				sb.WriteString(fmt.Sprintf(" return %s;", g.genExpr(retStmt.Value)))
			} else {
				sb.WriteString(fmt.Sprintf(" return nil;"))
			}
		} else if mc.Body != nil {
			sb.WriteString(fmt.Sprintf(" return %s;", g.genExpr(mc.Body)))
		} else {
			sb.WriteString(" return nil;")
		}
	}
	if len(s.Cases) > 0 {
		sb.WriteString("}")
	}
	sb.WriteString(" return nil; }()")
	return sb.String()
}

var goReserved = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
	"string": true, "int": true, "float64": true, "bool": true, "error": true,
	"len": true, "cap": true, "make": true, "new": true, "append": true,
	"copy": true, "delete": true, "panic": true, "recover": true, "close": true,
	"nil": true, "true": true, "false": true, "iota": true,
}

func escapeGoName(name string) string {
	if goReserved[name] { return "_" + name }
	return name
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
	case "int":
		return "cToInt"
	case "float":
		return "cToFloat"
	case "str":
		return "cToStr"
	case "chr":
		return "cChr"
	case "base64_decode":
		return "cBase64Decode"
	case "bool":
		return "cToBool"
	case "len":
		return "cLen"
	case "sort":
		return "cSort"
	case "grep":
		return "cGrep"
	case "rand":
		return "cRand"
	case "true":
		return "true"
	case "false":
		return "false"
	case "null":
		return "nil"
	case "_":
		return "_blank"
	}
	return escapeGoName(name)
}

func (g *Generator) genInfix(op, left, right string) string {
	switch op {
	case "+":
		return fmt.Sprintf("cAdd(%s, %s)", left, right)
	case "-":
		return fmt.Sprintf("cSub(%s, %s)", left, right)
	case "*":
		return fmt.Sprintf("cMul(%s, %s)", left, right)
	case "**":
		return fmt.Sprintf("cPow(%s, %s)", left, right)
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
		return fmt.Sprintf("cAnd(%s, func() Value { return %s })", left, right)
	case "||":
		return fmt.Sprintf("cOr(%s, func() Value { return %s })", left, right)
	}
	return "nil"
}

func (g *Generator) genCall(e *parser.CallExpression) string {
	// Check if it's a method call on a module: web.serve(), db.connect(), etc.
	if member, ok := e.Function.(*parser.MemberAccessExpression); ok {
		return g.genMethodCall(member, e.Arguments, e.Named)
	}

	// IIFE: (fn(){...})() — directly invoke the function literal
	if fl, ok := e.Function.(*parser.FunctionLiteral); ok {
		fnCode := g.genFuncLiteral(fl)
		args := make([]string, len(e.Arguments))
		for i, a := range e.Arguments {
			args[i] = g.genExpr(a)
		}
		if len(args) == 0 {
			return fmt.Sprintf("(%s)()", fnCode)
		}
		return fmt.Sprintf("(%s)(%s)", fnCode, strings.Join(args, ", "))
	}

	fn := g.genExpr(e.Function)
	args := make([]string, len(e.Arguments))
	for i, a := range e.Arguments {
		args[i] = g.genExpr(a)
	}

	// Built-in functions — direct call (not cCallFn)
	switch fn {
	case "cPrint":
		if len(args) > 1 {
			return fmt.Sprintf("cPrintMultiErr(%d)", len(args))
		}
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
	case "cToInt":
		if len(args) > 0 { return fmt.Sprintf("cToInt(%s)", args[0]) }
	case "cToFloat":
		if len(args) > 0 { return fmt.Sprintf("cToFloat(%s)", args[0]) }
	case "cToStr":
		if len(args) > 0 { return fmt.Sprintf("cToStr(%s)", args[0]) }
	case "cChr":
		if len(args) > 0 { return fmt.Sprintf("cChr(%s)", args[0]) }
	case "cBase64Decode":
		if len(args) > 0 { return fmt.Sprintf("cBase64Decode(%s)", args[0]) }
	case "cToBool":
		if len(args) > 0 { return fmt.Sprintf("cToBool(%s)", args[0]) }
	case "cLen":
		if len(args) > 0 { return fmt.Sprintf("cLen(%s)", args[0]) }
	case "cSort":
		if len(args) > 0 { return fmt.Sprintf("cSort(%s)", strings.Join(args, ", ")) }
	case "cGrep":
		if len(args) >= 2 { return fmt.Sprintf("cGrep(%s)", strings.Join(args, ", ")) }
	case "cRand":
		if len(args) >= 2 { return fmt.Sprintf("cRand(toFloat(%s), toFloat(%s))", args[0], args[1]) }
		if len(args) == 1 { return fmt.Sprintf("cRand(0, toFloat(%s))", args[0]) }
		return "cRand(0, 1)"
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
			return g.genHttpCall(method, args, named)
		case "error":
			return g.genErrorCall(method, args, named)
		case "llm":
			return g.genLlmCall(method, args, named)
		case "fs":
			return g.genFsCall(method, args, named)
		case "json":
			return g.genJsonCall(method, args, named)
		case "env":
			return g.genEnvCall(method, args, named)
		case "args":
			return g.genArgsCall(method, args, named)
		case "time":
			return g.genTimeCall(method, args, named)
		case "redis":
			return g.genRedisCall(method, args, named)
		case "image":
			return g.genImageCall(method, args, named)
		case "oauth":
			return g.genOAuthCall(method, args, named)
		}
	}

	// Two-level module calls: oauth.pkce.*, oauth.rbac.*
	if subMember, ok := member.Object.(*parser.MemberAccessExpression); ok {
		if rootIdent, ok := subMember.Object.(*parser.Identifier); ok {
			subModule := subMember.Property.Value
			switch rootIdent.Value {
			case "oauth":
				return g.genOAuthSubCall(subModule, method, args, named)
			}
		}
	}

	// Server/group object method calls are handled at runtime by cCall
	// which checks _type on the map. No special-casing needed here.

	// Append named args as trailing map for generic cCall dispatch
	if named != nil && len(named) > 0 {
		namedParts := []string{}
		for k, v := range named {
			namedParts = append(namedParts, fmt.Sprintf("%q, %s", k, g.genExpr(v)))
		}
		args = append(args, fmt.Sprintf("cMap(%s)", strings.Join(namedParts, ", ")))
	}

	// Object method call: obj.method(args)
	if len(args) == 0 {
		return fmt.Sprintf("cCall(%s, %q)", obj, method)
	}
	return fmt.Sprintf("cCall(%s, %q, %s)", obj, method, strings.Join(args, ", "))
}

func (g *Generator) genWebCall(method string, args []string, named map[string]parser.Expression) string {
	switch method {
	case "get", "post", "put", "delete", "patch":
		goMethod := strings.ToUpper(method)
		return fmt.Sprintf("cWebRoute(\"%s\", %s, %s)", goMethod, args[0], args[1])
	case "catch_all":
		return fmt.Sprintf("cWebCatchAll(%s)", args[0])
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
	case "sse":
		if len(args) > 0 {
			return fmt.Sprintf("cWebSSE(%s)", args[0])
		}
		return "nil"
	case "static":
		return fmt.Sprintf("cWebStatic(%s)", strings.Join(args, ", "))
	case "set_cookie":
		return fmt.Sprintf("cWebSetCookie(%s)", strings.Join(args, ", "))
	case "delete_cookie":
		return fmt.Sprintf("cWebDeleteCookie(%s)", strings.Join(args, ", "))
	case "use":
		return fmt.Sprintf("func() Value { cWebMiddlewares = append(cWebMiddlewares, %s); return nil }()", args[0])
	case "middleware":
		return "cWebMiddlewareNS"
	}
	return "nil"
}

func (g *Generator) genDbCall(method string, args []string, named map[string]parser.Expression) string {
	switch method {
	case "connect":
		if len(args) > 1 {
			return fmt.Sprintf("cDbConnect(toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cDbConnect(toString(%s))", args[0])
	case "disconnect":
		return "cDbDisconnectRT()"
	case "find":
		if len(args) > 2 {
			return fmt.Sprintf("cDbFindOpts(toString(%s), %s, %s)", args[0], args[1], args[2])
		}
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
	case "ping":
		return "cDbPing()"
	case "stats":
		return "cDbStats()"
	case "create_table":
		return fmt.Sprintf("cDbCreateTable(toString(%s), %s)", args[0], args[1])
	case "create_index":
		if len(args) > 1 {
			return fmt.Sprintf("cDbCreateIndex(toString(%s), %s)", args[0], args[1])
		}
	case "insert_batch":
		return fmt.Sprintf("cDbInsertBatch(toString(%s), %s)", args[0], args[1])
	case "upsert":
		return fmt.Sprintf("cDbUpsert(toString(%s), %s, %s)", args[0], args[1], args[2])
	case "query_one":
		if len(args) > 1 {
			return fmt.Sprintf("cDbQueryOne(toString(%s), toList(%s).Elements...)", args[0], args[1])
		}
		return fmt.Sprintf("cDbQueryOne(toString(%s))", args[0])
	case "transaction":
		if len(args) > 1 {
			return fmt.Sprintf("cDbTransaction(%s, %s)", args[0], args[1])
		}
		return fmt.Sprintf("cDbTransaction(%s)", args[0])
	case "sort":
		if len(args) > 2 {
			return fmt.Sprintf("cDbSort(toString(%s), %s, toString(%s))", args[0], args[1], args[2])
		}
		if len(args) > 1 {
			return fmt.Sprintf("cDbSort(toString(%s), %s, \"asc\")", args[0], args[1])
		}
	case "migrate":
		return fmt.Sprintf("cDbMigrate(%s)", args[0])
	case "migration_status":
		return "cDbMigrationStatus()"
	case "using":
		return fmt.Sprintf("cDbUsing(toString(%s))", args[0])
	case "last_insert_id":
		return "cDbLastInsertId()"
	case "sum":
		if len(args) > 2 {
			return fmt.Sprintf("cDbAggregate(\"SUM\", toString(%s), toString(%s), %s)", args[0], args[1], args[2])
		}
		return fmt.Sprintf("cDbAggregate(\"SUM\", toString(%s), toString(%s), nil)", args[0], args[1])
	case "avg":
		if len(args) > 2 {
			return fmt.Sprintf("cDbAggregate(\"AVG\", toString(%s), toString(%s), %s)", args[0], args[1], args[2])
		}
		return fmt.Sprintf("cDbAggregate(\"AVG\", toString(%s), toString(%s), nil)", args[0], args[1])
	case "min":
		if len(args) > 2 {
			return fmt.Sprintf("cDbAggregate(\"MIN\", toString(%s), toString(%s), %s)", args[0], args[1], args[2])
		}
		return fmt.Sprintf("cDbAggregate(\"MIN\", toString(%s), toString(%s), nil)", args[0], args[1])
	case "max":
		if len(args) > 2 {
			return fmt.Sprintf("cDbAggregate(\"MAX\", toString(%s), toString(%s), %s)", args[0], args[1], args[2])
		}
		return fmt.Sprintf("cDbAggregate(\"MAX\", toString(%s), toString(%s), nil)", args[0], args[1])
	case "batch_insert":
		return fmt.Sprintf("cDbInsertBatch(toString(%s), %s)", args[0], args[1])
	case "pg_copy":
		if len(args) > 2 {
			return fmt.Sprintf("cDbPgCopy(toString(%s), %s, %s)", args[0], args[1], args[2])
		}
		return "nil"
	}
	return "nil"
}

func (g *Generator) genRedisCall(method string, args []string, named map[string]parser.Expression) string {
	// Build named args map if present
	namedArg := ""
	if named != nil && len(named) > 0 {
		namedParts := []string{}
		for k, v := range named {
			namedParts = append(namedParts, fmt.Sprintf("%q, %s", k, g.genExpr(v)))
		}
		namedArg = fmt.Sprintf("cMap(%s)", strings.Join(namedParts, ", "))
	}

	switch method {
	case "connect":
		if namedArg != "" {
			return fmt.Sprintf("cRedisConnect(toString(%s), %s)", args[0], namedArg)
		}
		if len(args) > 1 {
			return fmt.Sprintf("cRedisConnect(toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cRedisConnect(toString(%s), nil)", args[0])
	case "disconnect":
		return "cRedisDisconnect()"
	case "ping":
		return "cRedisPing()"
	case "pipeline":
		return fmt.Sprintf("cRedisPipeline(%s)", args[0])
	case "using":
		return fmt.Sprintf("cRedisUsing(toString(%s))", args[0])
	case "set":
		if namedArg != "" {
			return fmt.Sprintf("cRedisSet(toString(%s), toString(%s), %s)", args[0], args[1], namedArg)
		}
		if len(args) > 2 {
			return fmt.Sprintf("cRedisSet(toString(%s), toString(%s), %s)", args[0], args[1], args[2])
		}
		return fmt.Sprintf("cRedisSet(toString(%s), toString(%s), nil)", args[0], args[1])
	case "get":
		if len(args) > 1 {
			return fmt.Sprintf("cRedisGet(toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cRedisGet(toString(%s), nil)", args[0])
	case "delete":
		return fmt.Sprintf("cRedisDelete(%s)", args[0])
	case "exists":
		return fmt.Sprintf("cRedisExists(toString(%s))", args[0])
	case "expire":
		return fmt.Sprintf("cRedisExpire(toString(%s), toString(%s))", args[0], args[1])
	case "ttl":
		return fmt.Sprintf("cRedisTTL(toString(%s))", args[0])
	case "incr":
		return fmt.Sprintf("cRedisIncr(toString(%s))", args[0])
	case "incr_by":
		return fmt.Sprintf("cRedisIncrBy(toString(%s), %s)", args[0], args[1])
	case "decr":
		return fmt.Sprintf("cRedisDecr(toString(%s))", args[0])
	case "cache":
		if namedArg != "" {
			return fmt.Sprintf("cRedisCache(toString(%s), %s, %s)", args[0], args[1], namedArg)
		}
		return fmt.Sprintf("cRedisCache(toString(%s), %s, nil)", args[0], args[1])
	case "invalidate":
		return fmt.Sprintf("cRedisInvalidate(toString(%s))", args[0])
	case "invalidate_pattern":
		return fmt.Sprintf("cRedisInvalidatePattern(toString(%s))", args[0])
	case "lock":
		if namedArg != "" {
			return fmt.Sprintf("cRedisLock(toString(%s), %s)", args[0], namedArg)
		}
		if len(args) > 1 {
			return fmt.Sprintf("cRedisLock(toString(%s), %s)", args[0], args[1])
		}
		return fmt.Sprintf("cRedisLock(toString(%s), nil)", args[0])
	case "publish":
		return fmt.Sprintf("cRedisPublish(toString(%s), toString(%s))", args[0], args[1])
	case "subscribe":
		return fmt.Sprintf("cRedisSubscribe(toString(%s), %s)", args[0], args[1])
	case "zadd":
		return fmt.Sprintf("cRedisZadd(toString(%s), %s)", args[0], args[1])
	case "zrange":
		return fmt.Sprintf("cRedisZrange(toString(%s), %s, %s)", args[0], args[1], args[2])
	case "zrevrange":
		if namedArg != "" {
			return fmt.Sprintf("cRedisZrevrange(toString(%s), %s, %s, %s)", args[0], args[1], args[2], namedArg)
		}
		return fmt.Sprintf("cRedisZrevrange(toString(%s), %s, %s, nil)", args[0], args[1], args[2])
	case "zcard":
		return fmt.Sprintf("cRedisZcard(toString(%s))", args[0])
	case "zrank":
		return fmt.Sprintf("cRedisZrank(toString(%s), toString(%s))", args[0], args[1])
	case "zrevrank":
		return fmt.Sprintf("cRedisZrevrank(toString(%s), toString(%s))", args[0], args[1])
	case "zscore":
		return fmt.Sprintf("cRedisZscore(toString(%s), toString(%s))", args[0], args[1])
	case "zincrby":
		return fmt.Sprintf("cRedisZincrby(toString(%s), toString(%s), %s)", args[0], args[1], args[2])
	case "rate_limiter":
		return fmt.Sprintf("cRedisRateLimiter(%s)", args[0])
	case "rate_limit":
		if namedArg != "" {
			return fmt.Sprintf("cRedisRateLimiter(%s)", namedArg)
		}
		return fmt.Sprintf("cRedisRateLimiter(%s)", args[0])
	case "hset":
		return fmt.Sprintf("cRedisHSet(toString(%s), toString(%s), toString(%s))", args[0], args[1], args[2])
	case "hget":
		return fmt.Sprintf("cRedisHGet(toString(%s), toString(%s))", args[0], args[1])
	case "hgetall":
		return fmt.Sprintf("cRedisHGetAll(toString(%s))", args[0])
	case "hdel":
		return fmt.Sprintf("cRedisHDel(toString(%s), toString(%s))", args[0], args[1])
	case "lpush":
		return fmt.Sprintf("cRedisLPush(toString(%s), toString(%s))", args[0], args[1])
	case "rpush":
		return fmt.Sprintf("cRedisRPush(toString(%s), toString(%s))", args[0], args[1])
	case "lpop":
		return fmt.Sprintf("cRedisLPop(toString(%s))", args[0])
	case "rpop":
		return fmt.Sprintf("cRedisRPop(toString(%s))", args[0])
	case "lrange":
		return fmt.Sprintf("cRedisLRange(toString(%s), %s, %s)", args[0], args[1], args[2])
	case "llen":
		return fmt.Sprintf("cRedisLLen(toString(%s))", args[0])
	case "zcount":
		return fmt.Sprintf("cRedisZCount(toString(%s), %s, %s)", args[0], args[1], args[2])
	case "zrem":
		return fmt.Sprintf("cRedisZRem(toString(%s), toString(%s))", args[0], args[1])
	}
	return "nil"
}

func (g *Generator) genImageCall(method string, args []string, named map[string]parser.Expression) string {
	switch method {
	case "open":
		return fmt.Sprintf("cImageOpen(toString(%s))", args[0])
	case "from_bytes":
		if len(args) > 1 {
			return fmt.Sprintf("cImageFromBytes(toString(%s), toString(%s))", args[0], args[1])
		}
		return fmt.Sprintf("cImageFromBytes(toString(%s))", args[0])
	case "info":
		return fmt.Sprintf("cImageInfo(toString(%s))", args[0])
	case "read_exif":
		return fmt.Sprintf("cImageReadExif(toString(%s))", args[0])
	case "create":
		if len(args) >= 3 {
			return fmt.Sprintf("cImageCreate(%s, %s, toString(%s))", args[0], args[1], args[2])
		}
		if len(args) >= 2 {
			return fmt.Sprintf("cImageCreate(%s, %s, \"#ffffff\")", args[0], args[1])
		}
	}
	return "nil"
}

func (g *Generator) genOAuthCall(method string, args []string, named map[string]parser.Expression) string {
	namedArg := ""
	if named != nil && len(named) > 0 {
		namedParts := []string{}
		for k, v := range named {
			namedParts = append(namedParts, fmt.Sprintf("%q, %s", k, g.genExpr(v)))
		}
		namedArg = fmt.Sprintf("cMap(%s)", strings.Join(namedParts, ", "))
	}

	switch method {
	case "provider":
		return fmt.Sprintf("cOAuthProvider(toString(%s), %s)", args[0], args[1])
	case "configure_jwt":
		return fmt.Sprintf("cOAuthConfigureJWT(%s)", args[0])
	case "authorization_url":
		if namedArg != "" {
			return fmt.Sprintf("cOAuthAuthorizationURL(toString(%s), %s)", args[0], namedArg)
		}
		return fmt.Sprintf("cOAuthAuthorizationURL(toString(%s), nil)", args[0])
	case "exchange_code":
		if namedArg != "" {
			return fmt.Sprintf("cOAuthExchangeCode(toString(%s), toString(%s), %s)", args[0], args[1], namedArg)
		}
		return fmt.Sprintf("cOAuthExchangeCode(toString(%s), toString(%s), nil)", args[0], args[1])
	case "get_profile":
		return fmt.Sprintf("cOAuthGetProfile(toString(%s), toString(%s))", args[0], args[1])
	case "sign_jwt":
		if namedArg != "" {
			return fmt.Sprintf("cOAuthSignJWT(%s, %s)", args[0], namedArg)
		}
		return fmt.Sprintf("cOAuthSignJWT(%s, nil)", args[0])
	case "sign_refresh_token":
		return fmt.Sprintf("cOAuthSignRefreshToken(%s)", args[0])
	case "verify_jwt":
		return fmt.Sprintf("cOAuthVerifyJWT(toString(%s))", args[0])
	case "verify_refresh_token":
		return fmt.Sprintf("cOAuthVerifyJWT(toString(%s))", args[0])
	case "decode_jwt":
		return fmt.Sprintf("cOAuthDecodeJWT(toString(%s))", args[0])
	case "revoke_jwt":
		return fmt.Sprintf("cOAuthRevokeJWT(toString(%s))", args[0])
	case "is_revoked":
		return fmt.Sprintf("cOAuthIsRevoked(toString(%s))", args[0])
	case "generate_state":
		return "cOAuthGenerateState()"
	case "generate_pkce":
		return "cOAuthGeneratePKCE()"
	case "hash_token":
		return fmt.Sprintf("cOAuthHashToken(toString(%s))", args[0])
	case "define_roles":
		return fmt.Sprintf("cOAuthDefineRoles(%s)", args[0])
	case "has_permission":
		return fmt.Sprintf("cOAuthHasPermission(%s, toString(%s))", args[0], args[1])
	case "check_permission":
		return fmt.Sprintf("cOAuthHasPermission(%s, toString(%s))", args[0], args[1])
	}
	return "nil"
}

func (g *Generator) genOAuthSubCall(subModule, method string, args []string, named map[string]parser.Expression) string {
	switch subModule {
	case "pkce":
		switch method {
		case "verifier":
			return "cOAuthPKCEVerifier()"
		case "challenge":
			if len(args) > 0 {
				return fmt.Sprintf("cOAuthPKCEChallenge(toString(%s))", args[0])
			}
			return "nil"
		}
	case "rbac":
		switch method {
		case "define":
			if len(args) > 0 {
				return fmt.Sprintf("cOAuthRBACDefine(%s)", args[0])
			}
			return "nil"
		case "assign":
			if len(args) >= 2 {
				return fmt.Sprintf("cOAuthRBACAssign(toString(%s), toString(%s))", args[0], args[1])
			}
			return "nil"
		case "check":
			if len(args) >= 2 {
				return fmt.Sprintf("cOAuthRBACCheck(toString(%s), toString(%s))", args[0], args[1])
			}
			return "nil"
		}
	}
	return "nil"
}

func (g *Generator) genHttpCall(method string, args []string, named map[string]parser.Expression) string {
	// Build named args map if present
	namedArg := ""
	if named != nil && len(named) > 0 {
		namedParts := []string{}
		for k, v := range named {
			namedParts = append(namedParts, fmt.Sprintf("%q, %s", k, g.genExpr(v)))
		}
		namedArg = fmt.Sprintf("cMap(%s)", strings.Join(namedParts, ", "))
	}
	switch method {
	case "get":
		if namedArg != "" {
			return fmt.Sprintf("cHttpDo(\"GET\", toString(%s), nil, %s)", args[0], namedArg)
		}
		if len(args) > 1 {
			return fmt.Sprintf("cHttpDo(\"GET\", toString(%s), nil, %s)", args[0], args[1])
		}
		return fmt.Sprintf("cHttpGet(toString(%s))", args[0])
	case "post":
		if namedArg != "" {
			if len(args) > 1 {
				return fmt.Sprintf("cHttpDo(\"POST\", toString(%s), %s, %s)", args[0], args[1], namedArg)
			}
			return fmt.Sprintf("cHttpDo(\"POST\", toString(%s), nil, %s)", args[0], namedArg)
		}
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
		if len(args) >= 1 {
			return fmt.Sprintf("cHttpRequest(%s)", args[0])
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
	case "from_json":
		if len(args) >= 1 {
			return fmt.Sprintf("cErrorFromJson(%s)", args[0])
		}
	case "from_compact":
		if len(args) >= 1 {
			return fmt.Sprintf("cErrorFromCompact(%s)", args[0])
		}
	}
	return "nil"
}

func (g *Generator) genFsCall(method string, args []string, named map[string]parser.Expression) string {
	allArgs := strings.Join(args, ", ")
	switch method {
	case "read":
		return fmt.Sprintf("cFsRead(%s)", allArgs)
	case "write":
		return fmt.Sprintf("cFsWrite(%s)", allArgs)
	case "append":
		return fmt.Sprintf("cFsAppend(%s)", allArgs)
	case "exists":
		return fmt.Sprintf("cFsExists(%s)", allArgs)
	case "delete":
		return fmt.Sprintf("cFsDelete(%s)", allArgs)
	case "copy":
		return fmt.Sprintf("cFsCopy(%s)", allArgs)
	case "move":
		return fmt.Sprintf("cFsMove(%s)", allArgs)
	case "list":
		return fmt.Sprintf("cFsList(%s)", allArgs)
	case "mkdir":
		return fmt.Sprintf("cFsMkdir(%s)", allArgs)
	case "rmdir":
		// Check for named recursive arg
		if named != nil {
			if r, ok := named["recursive"]; ok {
				if len(args) > 0 {
					return fmt.Sprintf("cFsRmdir(%s, %s)", args[0], g.genExpr(r))
				}
			}
		}
		return fmt.Sprintf("cFsRmdir(%s)", allArgs)
	case "stat":
		return fmt.Sprintf("cFsStat(%s)", allArgs)
	case "read_json":
		return fmt.Sprintf("cFsReadJson(%s)", allArgs)
	case "write_json":
		return fmt.Sprintf("cFsWriteJson(%s)", allArgs)
	case "read_lines":
		return fmt.Sprintf("cFsReadLines(%s)", allArgs)
	case "write_lines":
		return fmt.Sprintf("cFsWriteLines(%s)", allArgs)
	case "join":
		return fmt.Sprintf("cFsJoin(%s)", allArgs)
	case "cwd":
		return "cFsCwd()"
	case "basename":
		return fmt.Sprintf("cFsBasename(%s)", allArgs)
	case "dirname":
		return fmt.Sprintf("cFsDirname(%s)", allArgs)
	case "extension":
		return fmt.Sprintf("cFsExtension(%s)", allArgs)
	case "safe_join":
		return fmt.Sprintf("cFsSafeJoin(%s)", allArgs)
	case "temp_file":
		return "cFsTempFile()"
	case "temp_dir":
		return "cFsTempDir()"
	case "is_dir":
		return fmt.Sprintf("cFsIsDir(%s)", allArgs)
	case "is_file":
		return fmt.Sprintf("cFsIsFile(%s)", allArgs)
	case "ls":
		return fmt.Sprintf("cFsLs(%s)", allArgs)
	case "rename":
		return fmt.Sprintf("cFsRename(%s)", allArgs)
	case "ext":
		return fmt.Sprintf("cFsExt(%s)", allArgs)
	}
	return "nil"
}

func (g *Generator) genJsonCall(method string, args []string, named map[string]parser.Expression) string {
	allArgs := strings.Join(args, ", ")
	switch method {
	case "parse":
		return fmt.Sprintf("cJsonParse(%s)", allArgs)
	case "stringify":
		// Check for named indent arg
		if named != nil {
			if indent, ok := named["indent"]; ok {
				if len(args) > 0 {
					return fmt.Sprintf("cJsonStringify(%s, %s)", args[0], g.genExpr(indent))
				}
			}
		}
		return fmt.Sprintf("cJsonStringify(%s)", allArgs)
	case "pretty":
		return fmt.Sprintf("cJsonPretty(%s)", allArgs)
	case "valid":
		return fmt.Sprintf("cJsonValid(%s)", allArgs)
	case "merge":
		return fmt.Sprintf("cJsonMerge(%s)", allArgs)
	case "get":
		return fmt.Sprintf("cJsonGet(%s)", allArgs)
	case "set":
		return fmt.Sprintf("cJsonSet(%s)", allArgs)
	case "flatten", "flat":
		return fmt.Sprintf("cJsonFlatten(%s)", allArgs)
	case "unflatten":
		return fmt.Sprintf("cJsonUnflatten(%s)", allArgs)
	}
	return "nil"
}

func (g *Generator) genEnvCall(method string, args []string, named map[string]parser.Expression) string {
	allArgs := strings.Join(args, ", ")
	switch method {
	case "get":
		return fmt.Sprintf("cEnvGet(%s)", allArgs)
	case "set":
		return fmt.Sprintf("cEnvSet(%s)", allArgs)
	case "require":
		return fmt.Sprintf("cEnvRequire(%s)", allArgs)
	case "has":
		return fmt.Sprintf("cEnvHas(%s)", allArgs)
	case "all":
		return "cEnvAll()"
	case "load":
		return fmt.Sprintf("cEnvLoad(%s)", allArgs)
	}
	return "nil"
}

func (g *Generator) genArgsCall(method string, args []string, named map[string]parser.Expression) string {
	allArgs := strings.Join(args, ", ")
	switch method {
	case "all":
		return "cArgsAll()"
	case "get":
		return fmt.Sprintf("cArgsGet(%s)", allArgs)
	case "has":
		return fmt.Sprintf("cArgsHas(%s)", allArgs)
	case "len":
		return "cArgsLen()"
	}
	return "nil"
}

func (g *Generator) genTimeCall(method string, args []string, named map[string]parser.Expression) string {
	allArgs := strings.Join(args, ", ")
	switch method {
	case "sleep":
		return fmt.Sprintf("cTimeSleep(%s)", allArgs)
	case "now":
		return "cTimeNow()"
	case "now_iso":
		return "cTimeNowIso()"
	case "unix":
		return "cTimeUnix()"
	case "format":
		return fmt.Sprintf("cTimeFormat(%s)", allArgs)
	case "parse":
		return fmt.Sprintf("cTimeParse(%s)", allArgs)
	case "diff":
		return fmt.Sprintf("cTimeDiff(%s)", allArgs)
	case "since":
		return fmt.Sprintf("cTimeSince(%s)", allArgs)
	case "until":
		return fmt.Sprintf("cTimeUntil(%s)", allArgs)
	case "add":
		return fmt.Sprintf("cTimeAdd(%s)", allArgs)
	case "is_before", "before":
		return fmt.Sprintf("cTimeBefore(%s)", allArgs)
	case "is_after", "after":
		return fmt.Sprintf("cTimeAfter(%s)", allArgs)
	case "weekday":
		return fmt.Sprintf("cTimeWeekday(%s)", allArgs)
	case "quarter":
		return fmt.Sprintf("cTimeQuarter(%s)", allArgs)
	case "timezone":
		return fmt.Sprintf("cTimeTimezone(%s)", allArgs)
	case "today_start":
		return "cTimeTodayStart()"
	case "today_end":
		return "cTimeTodayEnd()"
	}
	return "nil"
}

func (g *Generator) genFuncLiteral(e *parser.FunctionLiteral) string {
	// Save current output and declared state
	savedOutput := g.output
	savedIndent := g.indent
	outerDeclared := g.declared
	g.declared = map[string]bool{}
	// Copy outer scope for closure capture
	for k := range outerDeclared { g.declared[k] = true }
	for _, p := range e.Params { g.declared[p.Name] = true }
	g.output = strings.Builder{}
	g.output.WriteString("func(args ...Value) Value {\n")
	g.indent++
	// Pre-declare variables that are new to this function
	if e.Body != nil {
		bodyVars := collectAssignedVars(e.Body)
		for _, p := range e.Params { delete(bodyVars, p.Name) }
		for v := range bodyVars {
			if outerDeclared[v] { continue } // captured from outer scope
			gn := escapeGoName(v)
			g.writef("var %s Value; _ = %s", gn, gn)
			g.declared[v] = true
		}
	}
	for i, p := range e.Params {
		name := escapeGoName(p.Name)
		g.writef("var %s Value; if len(args) > %d { %s = args[%d] }; _ = %s", name, i, name, i, name)
	}
	if e.ArrowExpr != nil {
		g.writef("return %s", g.genExpr(e.ArrowExpr))
	} else if e.Body != nil {
		for _, stmt := range e.Body.Statements {
			g.genStatement(stmt)
		}
		g.write("return nil")
	}
	g.indent--
	// Write closing brace without trailing newline so it can be used inline
	g.output.WriteString(strings.Repeat("\t", g.indent))
	g.output.WriteString("}")
	result := g.output.String()
	g.output = savedOutput
	g.indent = savedIndent
	g.declared = outerDeclared
	return result
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
		return fmt.Sprintf("var %s Value = %s; _ = %s", s.Name.Value, g.genExpr(s.Value), s.Name.Value)
	case *parser.CompoundAssignStatement:
		opFn := map[string]string{"+=": "cAdd", "-=": "cSub", "*=": "cMul", "/=": "cDiv"}[s.Operator]
		target := g.genExpr(s.Target)
		return fmt.Sprintf("%s = %s(%s, %s)", target, opFn, target, g.genExpr(s.Value))
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
