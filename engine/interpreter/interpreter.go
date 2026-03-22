package interpreter

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/codong-lang/codong/engine/parser"
	"github.com/codong-lang/codong/stdlib/codongerror"
)

// Object is the base interface for all runtime values.
type Object interface {
	Type() string
	Inspect() string
}

// --- Object types ---

type NumberObject struct{ Value float64 }

func (n *NumberObject) Type() string { return "number" }
func (n *NumberObject) Inspect() string {
	if n.Value == math.Trunc(n.Value) {
		return strconv.FormatInt(int64(n.Value), 10)
	}
	return strconv.FormatFloat(n.Value, 'f', -1, 64)
}

type StringObject struct{ Value string }

func (s *StringObject) Type() string    { return "string" }
func (s *StringObject) Inspect() string { return s.Value }

type BoolObject struct{ Value bool }

func (b *BoolObject) Type() string { return "bool" }
func (b *BoolObject) Inspect() string {
	if b.Value {
		return "true"
	}
	return "false"
}

type NullObject struct{}

func (n *NullObject) Type() string    { return "null" }
func (n *NullObject) Inspect() string { return "null" }

type ListObject struct{ Elements []Object }

func (l *ListObject) Type() string    { return "list" }
func (l *ListObject) Inspect() string { return "[...]" }

type MapObject struct {
	Entries map[string]Object
	Order   []string // preserve insertion order
}

func (m *MapObject) Type() string    { return "map" }
func (m *MapObject) Inspect() string { return "{...}" }

type FunctionObject struct {
	Name     string
	Params   []string
	DefaultExprs map[string]parser.Expression // evaluated at call time, not definition time
	Body     *parser.BlockStatement
	ArrowExpr parser.Expression
	Env      *Environment
}

func (f *FunctionObject) Type() string    { return "fn" }
func (f *FunctionObject) Inspect() string { return fmt.Sprintf("fn %s(...)", f.Name) }

type ReturnValue struct{ Value Object }

func (rv *ReturnValue) Type() string    { return "return" }
func (rv *ReturnValue) Inspect() string { return rv.Value.Inspect() }

type BreakSignal struct{}

func (b *BreakSignal) Type() string    { return "break" }
func (b *BreakSignal) Inspect() string { return "break" }

type ContinueSignal struct{}

func (c *ContinueSignal) Type() string    { return "continue" }
func (c *ContinueSignal) Inspect() string { return "continue" }

type ErrorObject struct {
	Error     *codongerror.CodongError
	IsRuntime bool // true = internal runtime error (propagates); false = user-created (assignable)
}

func (e *ErrorObject) Type() string    { return "error" }
func (e *ErrorObject) Inspect() string { return e.Error.Error() }
func (e *ErrorObject) IsError() bool   { return true }

// Singletons
var (
	NULL_OBJ  = &NullObject{}
	TRUE_OBJ  = &BoolObject{Value: true}
	FALSE_OBJ = &BoolObject{Value: false}
)

// --- Environment ---

type Environment struct {
	store  map[string]Object
	consts map[string]bool
	outer  *Environment
}

func NewEnvironment() *Environment {
	return &Environment{store: make(map[string]Object), consts: make(map[string]bool)}
}

func NewEnclosedEnvironment(outer *Environment) *Environment {
	env := NewEnvironment()
	env.outer = outer
	return env
}

func (e *Environment) Get(name string) (Object, bool) {
	obj, ok := e.store[name]
	if !ok && e.outer != nil {
		obj, ok = e.outer.Get(name)
	}
	return obj, ok
}

func (e *Environment) Set(name string, val Object) Object {
	// If variable exists in an outer scope, update it there
	if _, ok := e.store[name]; !ok && e.outer != nil {
		if _, exists := e.outer.Get(name); exists {
			return e.outer.Set(name, val)
		}
	}
	e.store[name] = val
	return val
}

func (e *Environment) SetConst(name string, val Object) Object {
	e.store[name] = val
	e.consts[name] = true
	return val
}

func (e *Environment) IsConst(name string) bool {
	if c, ok := e.consts[name]; ok {
		return c
	}
	if e.outer != nil {
		return e.outer.IsConst(name)
	}
	return false
}

// --- Interpreter ---

const maxCallDepth = 1000

type Interpreter struct {
	output    []string        // captured print() output
	callDepth int             // current recursion depth
	mu        sync.Mutex      // protects eval from concurrent HTTP handlers
	servers   []*ServerObject // active web servers
}

func New() *Interpreter {
	return &Interpreter{}
}

// Output returns all captured print() output.
func (i *Interpreter) Output() []string { return i.output }

// Eval evaluates an AST node in the given environment.
func (i *Interpreter) Eval(node parser.Node, env *Environment) Object {

	switch node := node.(type) {
	case *parser.Program:
		return i.evalProgram(node, env)
	case *parser.ExpressionStatement:
		return i.Eval(node.Expression, env)
	case *parser.AssignStatement:
		return i.evalAssign(node, env)
	case *parser.ConstStatement:
		val := i.Eval(node.Value, env)
		if isError(val) {
			return val
		}
		env.SetConst(node.Name.Value, val)
		return val
	case *parser.CompoundAssignStatement:
		return i.evalCompoundAssign(node, env)
	case *parser.PropertyAssignStatement:
		return i.evalPropertyAssign(node, env)
	case *parser.IndexAssignStatement:
		return i.evalIndexAssign(node, env)
	case *parser.ReturnStatement:
		if node.Value != nil {
			val := i.Eval(node.Value, env)
			if isError(val) {
				return val
			}
			// If ? already wrapped in ReturnValue, don't double-wrap
			if rv, ok := val.(*ReturnValue); ok {
				return rv
			}
			return &ReturnValue{Value: val}
		}
		return &ReturnValue{Value: NULL_OBJ}
	case *parser.BlockStatement:
		return i.evalBlock(node, env)
	case *parser.IfStatement:
		return i.evalIf(node, env)
	case *parser.ForInStatement:
		return i.evalForIn(node, env)
	case *parser.WhileStatement:
		return i.evalWhile(node, env)
	case *parser.BreakStatement:
		return &BreakSignal{}
	case *parser.ContinueStatement:
		return &ContinueSignal{}
	case *parser.GoStatement, *parser.SelectStatement, *parser.ChannelReceiveExpression:
		return newRuntimeError(codongerror.E1001_SYNTAX_ERROR,
			"concurrency is not supported in eval mode",
			"use 'codong run' for goroutines and channels")
	case *parser.ImportStatement, *parser.ExportStatement:
		return newRuntimeError(codongerror.E1001_SYNTAX_ERROR,
			"module imports are not supported in eval mode",
			"use 'codong run' for multi-file projects")
	case *parser.TypeDeclaration, *parser.InterfaceDeclaration:
		return NULL_OBJ // type declarations are no-op in dynamic interpreter
	case *parser.FunctionDefinition:
		fn := &FunctionObject{
			Name:         node.Name.Value,
			Params:       extractParamNames(node.Params),
			Body:         node.Body,
			DefaultExprs: node.Defaults, // store expressions, evaluate at call time
			Env:          env,
		}
		env.Set(node.Name.Value, fn)
		return fn
	case *parser.TryCatchStatement:
		return i.evalTryCatch(node, env)
	case *parser.MatchStatement:
		return i.evalMatch(node, env)

	// Expressions
	case *parser.NumberLiteral:
		return &NumberObject{Value: node.Value}
	case *parser.StringLiteral:
		return &StringObject{Value: node.Value}
	case *parser.StringInterpolation:
		return i.evalStringInterpolation(node, env)
	case *parser.BoolLiteral:
		return nativeBoolToObject(node.Value)
	case *parser.NullLiteral:
		return NULL_OBJ
	case *parser.Identifier:
		return i.evalIdentifier(node, env)
	case *parser.ListLiteral:
		elements := i.evalExpressions(node.Elements, env)
		if len(elements) == 1 && isError(elements[0]) {
			return elements[0]
		}
		return &ListObject{Elements: elements}
	case *parser.MapLiteral:
		return i.evalMapLiteral(node, env)
	case *parser.FunctionLiteral:
		return &FunctionObject{
			Params:       extractParamNames(node.Params),
			Body:         node.Body,
			ArrowExpr:    node.ArrowExpr,
			Env:          env,
			DefaultExprs: node.Defaults,
		}
	case *parser.CallExpression:
		return i.evalCall(node, env)
	case *parser.MemberAccessExpression:
		return i.evalMemberAccess(node, env)
	case *parser.IndexExpression:
		return i.evalIndex(node, env)
	case *parser.InfixExpression:
		left := i.Eval(node.Left, env)
		if isError(left) {
			return left
		}
		// Short-circuit && and ||
		if node.Operator == "&&" {
			if !isTruthy(left) {
				return left
			}
			return i.Eval(node.Right, env)
		}
		if node.Operator == "||" {
			if isTruthy(left) {
				return left
			}
			return i.Eval(node.Right, env)
		}
		right := i.Eval(node.Right, env)
		if isError(right) {
			return right
		}
		return i.evalInfix(node.Operator, left, right)
	case *parser.PrefixExpression:
		right := i.Eval(node.Right, env)
		if isError(right) {
			return right
		}
		return i.evalPrefix(node.Operator, right)
	case *parser.ErrorPropagationExpression:
		val := i.Eval(node.Expr, env)
		if _, ok := val.(*ErrorObject); ok {
			return &ReturnValue{Value: val}
		}
		return val
	}
	return NULL_OBJ
}

func (i *Interpreter) evalProgram(prog *parser.Program, env *Environment) Object {
	var result Object
	for _, stmt := range prog.Statements {
		result = i.Eval(stmt, env)
		switch result := result.(type) {
		case *ReturnValue:
			return result.Value
		case *ErrorObject:
			if result.IsRuntime {
				return result
			}
		}
	}
	return result
}

func (i *Interpreter) evalBlock(block *parser.BlockStatement, env *Environment) Object {
	var result Object
	for _, stmt := range block.Statements {
		result = i.Eval(stmt, env)
		if result != nil {
			switch r := result.(type) {
			case *ReturnValue, *BreakSignal, *ContinueSignal:
				return result
			case *ErrorObject:
				if r.IsRuntime {
					return result
				}
			}
		}
	}
	return result
}

func (i *Interpreter) evalAssign(node *parser.AssignStatement, env *Environment) Object {
	val := i.Eval(node.Value, env)
	if isRuntimeError(val) {
		return val
	}
	// _ = expr discards the return value (side-effect only)
	if node.Name.Value == "_" {
		return NULL_OBJ
	}
	if env.IsConst(node.Name.Value) {
		return newRuntimeError(codongerror.E1001_SYNTAX_ERROR,
			fmt.Sprintf("cannot assign to const '%s'", node.Name.Value),
			"remove const declaration or use a different variable name")
	}
	env.Set(node.Name.Value, val)
	return val
}

func (i *Interpreter) evalCompoundAssign(node *parser.CompoundAssignStatement, env *Environment) Object {
	// Resolve target: returns (current value, write-back closure, or error)
	var current Object
	var writeback func(Object)

	switch target := node.Target.(type) {
	case *parser.Identifier:
		if env.IsConst(target.Value) {
			return newRuntimeError(codongerror.E1001_SYNTAX_ERROR,
				fmt.Sprintf("cannot assign to const '%s'", target.Value), "")
		}
		var ok bool
		current, ok = env.Get(target.Value)
		if !ok {
			return newRuntimeError(codongerror.E1003_UNDEFINED_VAR,
				fmt.Sprintf("variable '%s' is not defined", target.Value), "")
		}
		name := target.Value
		writeback = func(v Object) { env.Set(name, v) }

	case *parser.MemberAccessExpression:
		obj := i.Eval(target.Object, env)
		if isError(obj) { return obj }
		m, ok := obj.(*MapObject)
		if !ok {
			return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "cannot compound-assign on "+obj.Type(), "")
		}
		key := target.Property.Value
		current = m.Entries[key]
		if current == nil { current = NULL_OBJ }
		writeback = func(v Object) {
			if _, exists := m.Entries[key]; !exists {
				m.Order = append(m.Order, key)
			}
			m.Entries[key] = v
		}

	case *parser.IndexExpression:
		obj := i.Eval(target.Left, env)
		if isError(obj) { return obj }
		idxObj := i.Eval(target.Index, env)
		if isError(idxObj) { return idxObj }
		switch o := obj.(type) {
		case *ListObject:
			numIdx, ok := idxObj.(*NumberObject)
			if !ok {
				return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "list index must be a number", "")
			}
			pos := int(numIdx.Value)
			if pos < 0 { pos = len(o.Elements) + pos }
			if pos < 0 || pos >= len(o.Elements) {
				return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
					fmt.Sprintf("index %d out of range (length %d)", int(numIdx.Value), len(o.Elements)), "")
			}
			current = o.Elements[pos]
			writeback = func(v Object) { o.Elements[pos] = v }
		case *MapObject:
			strIdx, ok := idxObj.(*StringObject)
			if !ok {
				return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "map key must be a string", "")
			}
			key := strIdx.Value
			current = o.Entries[key]
			if current == nil { current = NULL_OBJ }
			writeback = func(v Object) {
				if _, exists := o.Entries[key]; !exists {
					o.Order = append(o.Order, key)
				}
				o.Entries[key] = v
			}
		default:
			return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "cannot index "+obj.Type(), "")
		}

	default:
		return newRuntimeError(codongerror.E1001_SYNTAX_ERROR, "invalid compound assignment target", "")
	}

	// Evaluate RHS
	val := i.Eval(node.Value, env)
	if isError(val) { return val }

	// String += concatenation
	if node.Operator == "+=" {
		if ls, ok := current.(*StringObject); ok {
			if rs, ok2 := val.(*StringObject); ok2 {
				result := &StringObject{Value: ls.Value + rs.Value}
				writeback(result)
				return result
			}
		}
	}

	// Numeric compound assignment
	left, lok := current.(*NumberObject)
	right, rok := val.(*NumberObject)
	if !lok || !rok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "compound assignment requires number operands", "")
	}
	var numResult float64
	switch node.Operator {
	case "+=":
		numResult = left.Value + right.Value
	case "-=":
		numResult = left.Value - right.Value
	case "*=":
		numResult = left.Value * right.Value
	case "/=":
		if right.Value == 0 {
			return newRuntimeError(codongerror.E9003_PANIC, "division by zero", "")
		}
		numResult = left.Value / right.Value
	}
	result := &NumberObject{Value: numResult}
	writeback(result)
	return result
}

func (i *Interpreter) evalPropertyAssign(node *parser.PropertyAssignStatement, env *Environment) Object {
	obj := i.Eval(node.Object, env)
	if isError(obj) {
		return obj
	}
	val := i.Eval(node.Value, env)
	if isError(val) {
		return val
	}
	m, ok := obj.(*MapObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH,
			"cannot set property on "+obj.Type(), "property assignment only works on maps")
	}
	key := node.Property.Value
	if _, exists := m.Entries[key]; !exists {
		m.Order = append(m.Order, key)
	}
	m.Entries[key] = val
	return val
}

func (i *Interpreter) evalIndexAssign(node *parser.IndexAssignStatement, env *Environment) Object {
	obj := i.Eval(node.Left, env)
	if isError(obj) {
		return obj
	}
	index := i.Eval(node.Index, env)
	if isError(index) {
		return index
	}
	val := i.Eval(node.Value, env)
	if isError(val) {
		return val
	}

	switch target := obj.(type) {
	case *ListObject:
		idx, ok := index.(*NumberObject)
		if !ok {
			return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "list index must be a number", "use an integer index")
		}
		pos := int(idx.Value)
		if pos < 0 {
			pos = len(target.Elements) + pos
		}
		if pos < 0 || pos >= len(target.Elements) {
			return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
				fmt.Sprintf("index %d out of range (length %d)", int(idx.Value), len(target.Elements)),
				"check list bounds before assigning")
		}
		target.Elements[pos] = val
		return val
	case *MapObject:
		key, ok := index.(*StringObject)
		if !ok {
			return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "map key must be a string", "use a string key")
		}
		if _, exists := target.Entries[key.Value]; !exists {
			target.Order = append(target.Order, key.Value)
		}
		target.Entries[key.Value] = val
		return val
	default:
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH,
			"cannot index-assign on "+obj.Type(), "index assignment only works on lists and maps")
	}
}

func (i *Interpreter) evalIf(node *parser.IfStatement, env *Environment) Object {
	condition := i.Eval(node.Condition, env)
	if isError(condition) {
		return condition
	}
	if isTruthy(condition) {
		return i.Eval(node.Consequence, env)
	} else if node.Alternative != nil {
		return i.Eval(node.Alternative, env)
	}
	return NULL_OBJ
}

func (i *Interpreter) evalForIn(node *parser.ForInStatement, env *Environment) Object {
	iterable := i.Eval(node.Iterable, env)
	if isError(iterable) {
		return iterable
	}
	list, ok := iterable.(*ListObject)
	if !ok {
		return &ErrorObject{IsRuntime: true, Error: codongerror.New(
			codongerror.E1002_TYPE_MISMATCH,
			"for...in requires a list",
			codongerror.WithFix("use m.keys(), m.values(), or m.entries() for maps"),
		)}
	}
	for _, elem := range list.Elements {
		env.Set(node.Variable.Value, elem)
		innerEnv := NewEnclosedEnvironment(env)
		result := i.Eval(node.Body, innerEnv)
		if _, ok := result.(*BreakSignal); ok {
			break
		}
		if _, ok := result.(*ContinueSignal); ok {
			continue
		}
		if _, ok := result.(*ReturnValue); ok {
			return result
		}
	}
	return NULL_OBJ
}

func (i *Interpreter) evalWhile(node *parser.WhileStatement, env *Environment) Object {
	for {
		condition := i.Eval(node.Condition, env)
		if isError(condition) {
			return condition
		}
		if !isTruthy(condition) {
			break
		}
		result := i.Eval(node.Body, env)
		if _, ok := result.(*BreakSignal); ok {
			break
		}
		if _, ok := result.(*ContinueSignal); ok {
			continue
		}
		if _, ok := result.(*ReturnValue); ok {
			return result
		}
	}
	return NULL_OBJ
}

func (i *Interpreter) evalTryCatch(node *parser.TryCatchStatement, env *Environment) Object {
	result := i.Eval(node.Try, env)
	// Direct error
	if errObj, ok := result.(*ErrorObject); ok {
		catchEnv := NewEnclosedEnvironment(env)
		catchEnv.Set(node.CatchVar.Value, errObj)
		return i.Eval(node.Catch, catchEnv)
	}
	// ? operator wraps error in ReturnValue — intercept it in try block
	if rv, ok := result.(*ReturnValue); ok {
		if errObj, ok := rv.Value.(*ErrorObject); ok {
			catchEnv := NewEnclosedEnvironment(env)
			catchEnv.Set(node.CatchVar.Value, errObj)
			return i.Eval(node.Catch, catchEnv)
		}
	}
	return result
}

func (i *Interpreter) evalMatch(node *parser.MatchStatement, env *Environment) Object {
	subject := i.Eval(node.Subject, env)
	if isError(subject) {
		return subject
	}
	for _, c := range node.Cases {
		if c.IsDefault {
			if c.BodyBlock != nil {
				return i.Eval(c.BodyBlock, env)
			}
			return i.Eval(c.Body, env)
		}
		pattern := i.Eval(c.Pattern, env)
		if objectsEqual(subject, pattern) {
			if c.BodyBlock != nil {
				return i.Eval(c.BodyBlock, env)
			}
			return i.Eval(c.Body, env)
		}
	}
	return NULL_OBJ
}

func (i *Interpreter) evalIdentifier(node *parser.Identifier, env *Environment) Object {
	if val, ok := env.Get(node.Value); ok {
		return val
	}
	// Special module identifiers
	if node.Value == "error" {
		return errorModuleSingleton
	}
	if node.Value == "web" {
		return webModuleSingleton
	}
	if node.Value == "db" {
		return dbModuleSingleton
	}
	if node.Value == "http" {
		return httpModuleSingleton
	}
	if node.Value == "llm" {
		return llmModuleSingleton
	}
	// Check built-in functions
	if builtin, ok := builtins[node.Value]; ok {
		return builtin
	}
	return &ErrorObject{IsRuntime: true, Error: codongerror.New(
		codongerror.E1003_UNDEFINED_VAR,
		fmt.Sprintf("variable '%s' is not defined", node.Value),
		codongerror.WithFix(fmt.Sprintf("declare %s before using it: %s = value", node.Value, node.Value)),
	)}
}

func (i *Interpreter) evalMapLiteral(node *parser.MapLiteral, env *Environment) Object {
	m := &MapObject{Entries: make(map[string]Object), Order: []string{}}
	for _, entry := range node.Entries {
		var key string
		switch k := entry.Key.(type) {
		case *parser.Identifier:
			key = k.Value
		case *parser.StringLiteral:
			key = k.Value
		default:
			keyObj := i.Eval(entry.Key, env)
			if isError(keyObj) {
				return keyObj
			}
			key = keyObj.Inspect()
		}
		val := i.Eval(entry.Value, env)
		if isError(val) {
			return val
		}
		if _, exists := m.Entries[key]; !exists {
			m.Order = append(m.Order, key)
		}
		m.Entries[key] = val
	}
	return m
}

func (i *Interpreter) evalCall(node *parser.CallExpression, env *Environment) Object {
	fn := i.Eval(node.Function, env)
	if isError(fn) {
		return fn
	}

	args := i.evalExpressions(node.Arguments, env)
	if len(args) == 1 && isError(args[0]) {
		return args[0]
	}

	// Evaluate named arguments
	named := map[string]Object{}
	for k, v := range node.Named {
		val := i.Eval(v, env)
		if isError(val) {
			return val
		}
		named[k] = val
	}

	return i.applyFunction(fn, args, named)
}

func (i *Interpreter) applyFunction(fn Object, args []Object, named ...map[string]Object) Object {
	i.callDepth++
	defer func() { i.callDepth-- }()
	if i.callDepth > maxCallDepth {
		return &ErrorObject{IsRuntime: true, Error: codongerror.New(
			codongerror.E9002_STACK_OVERFLOW,
			fmt.Sprintf("maximum call depth (%d) exceeded", maxCallDepth),
			codongerror.WithFix("check for infinite recursion"),
		)}
	}

	namedArgs := map[string]Object{}
	if len(named) > 0 && named[0] != nil {
		namedArgs = named[0]
	}

	switch fn := fn.(type) {
	case *FunctionObject:
		extEnv := NewEnclosedEnvironment(fn.Env)
		// Apply positional arguments
		for idx, param := range fn.Params {
			if idx < len(args) {
				extEnv.Set(param, args[idx])
			} else if val, ok := namedArgs[param]; ok {
				// Apply named arguments
				extEnv.Set(param, val)
			} else if fn.DefaultExprs != nil {
				if defExpr, ok := fn.DefaultExprs[param]; ok {
					// Evaluate default at call time in extEnv (so earlier params are visible)
					extEnv.Set(param, i.Eval(defExpr, extEnv))
				}
			}
		}
		if fn.ArrowExpr != nil {
			return i.Eval(fn.ArrowExpr, extEnv)
		}
		result := i.Eval(fn.Body, extEnv)
		if rv, ok := result.(*ReturnValue); ok {
			return rv.Value
		}
		// Propagate runtime errors (e.g. stack overflow, division by zero)
		if errObj, ok := result.(*ErrorObject); ok && errObj.IsRuntime {
			return errObj
		}
		return NULL_OBJ
	case *BuiltinFunction:
		// Pass named args as a trailing MapObject so builtins can access them
		if len(named) > 0 && len(named[0]) > 0 {
			namedMap := &MapObject{Entries: named[0], Order: make([]string, 0, len(named[0]))}
			for k := range named[0] {
				namedMap.Order = append(namedMap.Order, k)
			}
			args = append(args, namedMap)
		}
		return fn.Fn(i, args...)
	default:
		return &ErrorObject{IsRuntime: true, Error: codongerror.New(
			codongerror.E1004_UNDEFINED_FUNC,
			fmt.Sprintf("not a function: %s", fn.Type()),
		)}
	}
}

func (i *Interpreter) evalMemberAccess(node *parser.MemberAccessExpression, env *Environment) Object {
	obj := i.Eval(node.Object, env)
	if isError(obj) {
		return obj
	}
	prop := node.Property.Value

	// Map access and methods
	if m, ok := obj.(*MapObject); ok {
		// Field access first — user-defined fields take priority over built-in methods
		if val, exists := m.Entries[prop]; exists {
			return val
		}
		// Then check built-in map methods
		if mapMethod := i.evalMapMethod(m, prop); mapMethod != nil {
			return mapMethod
		}
		return NULL_OBJ // accessing non-existent key returns null
	}

	// String methods
	if s, ok := obj.(*StringObject); ok {
		return i.evalStringMethod(s, prop)
	}

	// List methods
	if l, ok := obj.(*ListObject); ok {
		return i.evalListMethod(l, prop)
	}

	// Error field access
	if e, ok := obj.(*ErrorObject); ok {
		switch prop {
		case "code":
			return &StringObject{Value: e.Error.Code}
		case "message":
			return &StringObject{Value: e.Error.Message}
		case "fix":
			return &StringObject{Value: e.Error.Fix}
		case "retry":
			return nativeBoolToObject(e.Error.Retry)
		case "docs":
			return &StringObject{Value: e.Error.Docs}
		case "source":
			return &StringObject{Value: e.Error.Source}
		}
	}

	// error module methods: error.new(), error.wrap(), error.is(), etc.
	if _, ok := obj.(*ErrorModuleObject); ok {
		return i.evalErrorModuleMethod(prop)
	}

	// web module methods: web.get(), web.serve(), web.json(), etc.
	if _, ok := obj.(*WebModuleObject); ok {
		return i.evalWebModuleMethod(prop)
	}

	// db module methods: db.connect(), db.find(), db.insert(), etc.
	if _, ok := obj.(*DbModuleObject); ok {
		return i.evalDbModuleMethod(prop)
	}

	// http module methods: http.get(), http.post(), etc.
	if _, ok := obj.(*HttpModuleObject); ok {
		return i.evalHttpModuleMethod(prop)
	}

	// http response fields: resp.status, resp.json(), resp.body, etc.
	if resp, ok := obj.(*HttpResponseObject); ok {
		return i.evalHttpResponseMemberAccess(resp, prop)
	}

	// llm module methods: llm.ask(), llm.chat(), llm.embed(), etc.
	if _, ok := obj.(*LlmModuleObject); ok {
		return i.evalLlmModuleMethod(prop)
	}

	// server object methods: server.close()
	if srv, ok := obj.(*ServerObject); ok {
		if prop == "close" {
			return &BuiltinFunction{
				Name: "server.close",
				Fn: func(interp *Interpreter, args ...Object) Object {
					srv.server.Shutdown(context.Background())
					return NULL_OBJ
				},
			}
		}
	}

	// Friendly errors for common other-language patterns
	if bf, ok := obj.(*BuiltinFunction); ok && bf.Name == "console" {
		return newRuntimeError(codongerror.E1004_UNDEFINED_FUNC,
			fmt.Sprintf("console.%s() is not a Codong function", prop),
			"use print() instead: print(\"your message\")")
	}

	return NULL_OBJ
}

func (i *Interpreter) evalErrorModuleMethod(method string) Object {
	return &BuiltinFunction{
		Name: "error." + method,
		Fn: func(interp *Interpreter, args ...Object) Object {
			switch method {
			case "new":
				if len(args) < 2 {
					return &ErrorObject{IsRuntime: true, Error: codongerror.New(codongerror.E1005_INVALID_ARGUMENT, "error.new requires code and message")}
				}
				code, ok1 := args[0].(*StringObject)
				msg, ok2 := args[1].(*StringObject)
				if !ok1 || !ok2 {
					return &ErrorObject{IsRuntime: true, Error: codongerror.New(codongerror.E1002_TYPE_MISMATCH, "error.new: code and message must be strings")}
				}
				opts := []codongerror.Option{}
				// Named args arrive as trailing MapObject: fix, retry, context, docs
				for _, a := range args[2:] {
					if named, ok := a.(*MapObject); ok {
						if fix, ok := named.Entries["fix"]; ok {
							if s, ok := fix.(*StringObject); ok {
								opts = append(opts, codongerror.WithFix(s.Value))
							}
						}
						if retry, ok := named.Entries["retry"]; ok {
							if b, ok := retry.(*BoolObject); ok {
								opts = append(opts, codongerror.WithRetry(b.Value))
							}
						}
						if ctx, ok := named.Entries["context"]; ok {
							if m, ok := ctx.(*MapObject); ok {
								ctxMap := map[string]any{}
								for k, v := range m.Entries {
									ctxMap[k] = v.Inspect()
								}
								opts = append(opts, codongerror.WithContext(ctxMap))
							}
						}
						if docs, ok := named.Entries["docs"]; ok {
							if s, ok := docs.(*StringObject); ok {
								opts = append(opts, codongerror.WithDocs(s.Value))
							}
						}
					}
				}
				return &ErrorObject{IsRuntime: false, Error: codongerror.New(code.Value, msg.Value, opts...)}
			case "wrap":
				if len(args) < 2 {
					return NULL_OBJ
				}
				errObj, ok1 := args[0].(*ErrorObject)
				ctx, ok2 := args[1].(*StringObject)
				if !ok1 || !ok2 {
					return NULL_OBJ
				}
				return &ErrorObject{Error: codongerror.Wrap(errObj.Error, ctx.Value)}
			case "is":
				if len(args) < 2 {
					return FALSE_OBJ
				}
				errObj, ok1 := args[0].(*ErrorObject)
				code, ok2 := args[1].(*StringObject)
				if !ok1 || !ok2 {
					return FALSE_OBJ
				}
				return nativeBoolToObject(codongerror.Is(errObj.Error, code.Value))
			case "unwrap":
				if len(args) < 1 {
					return NULL_OBJ
				}
				errObj, ok := args[0].(*ErrorObject)
				if !ok {
					return NULL_OBJ
				}
				inner := codongerror.Unwrap(errObj.Error)
				if inner == nil {
					return NULL_OBJ
				}
				return &ErrorObject{Error: inner}
			case "to_json":
				if len(args) < 1 {
					return &StringObject{Value: ""}
				}
				errObj, ok := args[0].(*ErrorObject)
				if !ok {
					return &StringObject{Value: ""}
				}
				return &StringObject{Value: codongerror.ToJSON(errObj.Error)}
			case "to_compact":
				if len(args) < 1 {
					return &StringObject{Value: ""}
				}
				errObj, ok := args[0].(*ErrorObject)
				if !ok {
					return &StringObject{Value: ""}
				}
				return &StringObject{Value: codongerror.ToCompact(errObj.Error)}
			case "set_format":
				if len(args) < 1 {
					return NULL_OBJ
				}
				fmtStr, ok := args[0].(*StringObject)
				if !ok {
					return NULL_OBJ
				}
				codongerror.SetFormat(fmtStr.Value)
				return NULL_OBJ
			case "handle":
				// error.handle(err, {E_CODE: handler_fn, _: default_fn})
				if len(args) < 2 {
					return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
						"error.handle requires (error, handlers_map)", "")
				}
				errObj, ok := args[0].(*ErrorObject)
				if !ok {
					return NULL_OBJ // not an error, nothing to handle
				}
				handlers, ok := args[1].(*MapObject)
				if !ok {
					return newRuntimeError(codongerror.E1002_TYPE_MISMATCH,
						"handlers must be a map", "")
				}
				// Try exact code match first
				if handler, exists := handlers.Entries[errObj.Error.Code]; exists {
					return interp.applyFunction(handler, []Object{errObj})
				}
				// Try default handler _
				if handler, exists := handlers.Entries["_"]; exists {
					return interp.applyFunction(handler, []Object{errObj})
				}
				return errObj // no handler matched, return error as-is
			case "retry":
				// error.retry(fn, max_attempts) or error.retry(fn, max_attempts, delay_ms)
				if len(args) < 2 {
					return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
						"error.retry requires (fn, max_attempts)", "")
				}
				retryFn := args[0]
				maxAttempts := 3
				if n, ok := args[1].(*NumberObject); ok {
					maxAttempts = int(n.Value)
				}
				var lastErr Object
				for attempt := 0; attempt < maxAttempts; attempt++ {
					result := interp.applyFunction(retryFn, []Object{})
					if errObj, ok := result.(*ErrorObject); ok {
						if errObj.Error.Retry {
							lastErr = result
							continue // retry
						}
						return result // non-retryable error
					}
					return result // success
				}
				if lastErr != nil {
					return lastErr
				}
				return NULL_OBJ
			}
			return NULL_OBJ
		},
	}
}

func (i *Interpreter) evalIndex(node *parser.IndexExpression, env *Environment) Object {
	left := i.Eval(node.Left, env)
	if isError(left) {
		return left
	}
	index := i.Eval(node.Index, env)
	if isError(index) {
		return index
	}

	switch {
	case left.Type() == "list" && index.Type() == "number":
		l := left.(*ListObject)
		idx := int(index.(*NumberObject).Value)
		if idx < 0 {
			idx = len(l.Elements) + idx
		}
		if idx < 0 || idx >= len(l.Elements) {
			return NULL_OBJ
		}
		return l.Elements[idx]
	case left.Type() == "map" && index.Type() == "string":
		m := left.(*MapObject)
		key := index.(*StringObject).Value
		if val, ok := m.Entries[key]; ok {
			return val
		}
		return NULL_OBJ
	}
	return NULL_OBJ
}

func (i *Interpreter) evalStringInterpolation(node *parser.StringInterpolation, env *Environment) Object {
	var parts []string
	for _, part := range node.Parts {
		val := i.Eval(part, env)
		if isError(val) {
			return val
		}
		parts = append(parts, val.Inspect())
	}
	return &StringObject{Value: strings.Join(parts, "")}
}

func (i *Interpreter) evalExpressions(exprs []parser.Expression, env *Environment) []Object {
	var result []Object
	for _, e := range exprs {
		val := i.Eval(e, env)
		if isError(val) {
			return []Object{val}
		}
		result = append(result, val)
	}
	return result
}

func (i *Interpreter) evalInfix(op string, left, right Object) Object {
	switch {
	case left.Type() == "number" && right.Type() == "number":
		return i.evalNumberInfix(op, left.(*NumberObject), right.(*NumberObject))
	case left.Type() == "string" && right.Type() == "string":
		return i.evalStringInfix(op, left.(*StringObject), right.(*StringObject))
	case op == "==":
		return nativeBoolToObject(objectsEqual(left, right))
	case op == "!=":
		return nativeBoolToObject(!objectsEqual(left, right))
	case op == "&&":
		return nativeBoolToObject(isTruthy(left) && isTruthy(right))
	case op == "||":
		return nativeBoolToObject(isTruthy(left) || isTruthy(right))
	}
	return &ErrorObject{IsRuntime: true, Error: codongerror.New(
		codongerror.E1002_TYPE_MISMATCH,
		fmt.Sprintf("unknown operator: %s %s %s", left.Type(), op, right.Type()),
	)}
}

func (i *Interpreter) evalNumberInfix(op string, left, right *NumberObject) Object {
	switch op {
	case "+":
		return &NumberObject{Value: left.Value + right.Value}
	case "-":
		return &NumberObject{Value: left.Value - right.Value}
	case "*":
		return &NumberObject{Value: left.Value * right.Value}
	case "/":
		if right.Value == 0 {
			return &ErrorObject{IsRuntime: true, Error: codongerror.New(codongerror.E9003_PANIC, "division by zero")}
		}
		return &NumberObject{Value: left.Value / right.Value}
	case "%":
		return &NumberObject{Value: math.Mod(left.Value, right.Value)}
	case "<":
		return nativeBoolToObject(left.Value < right.Value)
	case ">":
		return nativeBoolToObject(left.Value > right.Value)
	case "<=":
		return nativeBoolToObject(left.Value <= right.Value)
	case ">=":
		return nativeBoolToObject(left.Value >= right.Value)
	case "==":
		return nativeBoolToObject(left.Value == right.Value)
	case "!=":
		return nativeBoolToObject(left.Value != right.Value)
	}
	return NULL_OBJ
}

func (i *Interpreter) evalStringInfix(op string, left, right *StringObject) Object {
	switch op {
	case "+":
		return &StringObject{Value: left.Value + right.Value}
	case "==":
		return nativeBoolToObject(left.Value == right.Value)
	case "!=":
		return nativeBoolToObject(left.Value != right.Value)
	}
	return &ErrorObject{IsRuntime: true, Error: codongerror.New(
		codongerror.E1002_TYPE_MISMATCH,
		fmt.Sprintf("unknown operator: string %s string", op),
	)}
}

func (i *Interpreter) evalPrefix(op string, right Object) Object {
	switch op {
	case "!":
		return nativeBoolToObject(!isTruthy(right))
	case "-":
		if num, ok := right.(*NumberObject); ok {
			return &NumberObject{Value: -num.Value}
		}
		return &ErrorObject{IsRuntime: true, Error: codongerror.New(codongerror.E1002_TYPE_MISMATCH, "cannot negate non-number")}
	}
	return NULL_OBJ
}

// String/List/Map method stubs (return method wrapper for call)
func (i *Interpreter) evalStringMethod(s *StringObject, method string) Object {
	return &BuiltinFunction{
		Name: "string." + method,
		Fn: func(interp *Interpreter, args ...Object) Object {
			switch method {
			case "len":
				return &NumberObject{Value: float64(len(s.Value))}
			case "upper":
				return &StringObject{Value: strings.ToUpper(s.Value)}
			case "lower":
				return &StringObject{Value: strings.ToLower(s.Value)}
			case "trim":
				return &StringObject{Value: strings.TrimSpace(s.Value)}
			case "trim_start":
				return &StringObject{Value: strings.TrimLeft(s.Value, " \t\n\r")}
			case "trim_end":
				return &StringObject{Value: strings.TrimRight(s.Value, " \t\n\r")}
			case "contains":
				if len(args) < 1 {
					return NULL_OBJ
				}
				sub, ok := args[0].(*StringObject)
				if !ok {
					return FALSE_OBJ
				}
				return nativeBoolToObject(strings.Contains(s.Value, sub.Value))
			case "split":
				sep := ""
				if len(args) > 0 {
					if sepObj, ok := args[0].(*StringObject); ok {
						sep = sepObj.Value
					}
				}
				parts := strings.Split(s.Value, sep)
				elements := make([]Object, len(parts))
				for j, p := range parts {
					elements[j] = &StringObject{Value: p}
				}
				return &ListObject{Elements: elements}
			case "replace":
				if len(args) < 2 {
					return s
				}
				old, ok1 := args[0].(*StringObject)
				new_, ok2 := args[1].(*StringObject)
				if !ok1 || !ok2 {
					return s
				}
				return &StringObject{Value: strings.ReplaceAll(s.Value, old.Value, new_.Value)}
			case "starts_with":
				if len(args) < 1 {
					return FALSE_OBJ
				}
				pre, ok := args[0].(*StringObject)
				if !ok {
					return FALSE_OBJ
				}
				return nativeBoolToObject(strings.HasPrefix(s.Value, pre.Value))
			case "ends_with":
				if len(args) < 1 {
					return FALSE_OBJ
				}
				suf, ok := args[0].(*StringObject)
				if !ok {
					return FALSE_OBJ
				}
				return nativeBoolToObject(strings.HasSuffix(s.Value, suf.Value))
			case "index_of":
				if len(args) < 1 {
					return &NumberObject{Value: -1}
				}
				sub, ok := args[0].(*StringObject)
				if !ok {
					return &NumberObject{Value: -1}
				}
				return &NumberObject{Value: float64(strings.Index(s.Value, sub.Value))}
			case "slice":
				start := 0
				end := len(s.Value)
				if len(args) > 0 {
					if n, ok := args[0].(*NumberObject); ok {
						start = int(n.Value)
					}
				}
				if len(args) > 1 {
					if n, ok := args[1].(*NumberObject); ok {
						end = int(n.Value)
					}
				}
				if start < 0 { start = len(s.Value) + start; if start < 0 { start = 0 } }
				if end < 0 { end = len(s.Value) + end }
				if end > len(s.Value) { end = len(s.Value) }
				if start > end { return &StringObject{Value: ""} }
				return &StringObject{Value: s.Value[start:end]}
			case "repeat":
				if len(args) < 1 {
					return s
				}
				n, ok := args[0].(*NumberObject)
				if !ok {
					return s
				}
				return &StringObject{Value: strings.Repeat(s.Value, int(n.Value))}
			case "to_number":
				v, err := strconv.ParseFloat(s.Value, 64)
				if err != nil {
					return NULL_OBJ
				}
				return &NumberObject{Value: v}
			case "to_bool":
				return nativeBoolToObject(s.Value == "true" || s.Value == "1")
			case "match":
				if len(args) < 1 {
					return NULL_OBJ
				}
				pat, ok := args[0].(*StringObject)
				if !ok {
					return NULL_OBJ
				}
				re, err := regexp.Compile(pat.Value)
				if err != nil {
					return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
						fmt.Sprintf("invalid regex pattern: %s", err.Error()), "")
				}
				matches := re.FindAllString(s.Value, -1)
				var elements []Object
				for _, m := range matches {
					elements = append(elements, &StringObject{Value: m})
				}
				return &ListObject{Elements: elements}
			}
			return NULL_OBJ
		},
	}
}

func (i *Interpreter) evalListMethod(l *ListObject, method string) Object {
	return &BuiltinFunction{
		Name: "list." + method,
		Fn: func(interp *Interpreter, args ...Object) Object {
			switch method {
			case "len":
				return &NumberObject{Value: float64(len(l.Elements))}
			case "push":
				if len(args) > 0 {
					l.Elements = append(l.Elements, args[0])
				}
				return l
			case "pop":
				if len(l.Elements) == 0 {
					return NULL_OBJ
				}
				last := l.Elements[len(l.Elements)-1]
				l.Elements = l.Elements[:len(l.Elements)-1]
				return last
			case "first":
				if len(l.Elements) == 0 {
					return NULL_OBJ
				}
				return l.Elements[0]
			case "last":
				if len(l.Elements) == 0 {
					return NULL_OBJ
				}
				return l.Elements[len(l.Elements)-1]
			case "contains":
				if len(args) < 1 {
					return FALSE_OBJ
				}
				for _, el := range l.Elements {
					if objectsEqual(el, args[0]) {
						return TRUE_OBJ
					}
				}
				return FALSE_OBJ
			case "join":
				sep := ""
				if len(args) > 0 {
					if s, ok := args[0].(*StringObject); ok {
						sep = s.Value
					}
				}
				parts := make([]string, len(l.Elements))
				for j, el := range l.Elements {
					parts[j] = el.Inspect()
				}
				return &StringObject{Value: strings.Join(parts, sep)}
			case "reverse":
				for left, right := 0, len(l.Elements)-1; left < right; left, right = left+1, right-1 {
					l.Elements[left], l.Elements[right] = l.Elements[right], l.Elements[left]
				}
				return l
			case "map":
				if len(args) < 1 {
					return l
				}
				result := make([]Object, len(l.Elements))
				for j, el := range l.Elements {
					result[j] = interp.applyFunction(args[0], []Object{el})
				}
				return &ListObject{Elements: result}
			case "filter":
				if len(args) < 1 {
					return l
				}
				var result []Object
				for _, el := range l.Elements {
					val := interp.applyFunction(args[0], []Object{el})
					if isTruthy(val) {
						result = append(result, el)
					}
				}
				return &ListObject{Elements: result}
			case "shift":
				if len(l.Elements) == 0 {
					return NULL_OBJ
				}
				first := l.Elements[0]
				l.Elements = l.Elements[1:]
				return first
			case "unshift":
				if len(args) > 0 {
					l.Elements = append([]Object{args[0]}, l.Elements...)
				}
				return l
			case "sort":
				if len(args) > 0 {
					// Custom comparator: fn(a, b) returns negative/zero/positive number
					sort.Slice(l.Elements, func(a, b int) bool {
						result := interp.applyFunction(args[0], []Object{l.Elements[a], l.Elements[b]})
						if num, ok := result.(*NumberObject); ok {
							return num.Value < 0
						}
						return false
					})
				} else {
					sort.Slice(l.Elements, func(a, b int) bool {
						return compareObjects(l.Elements[a], l.Elements[b]) < 0
					})
				}
				return l
			case "slice":
				start := 0
				end := len(l.Elements)
				if len(args) > 0 {
					if n, ok := args[0].(*NumberObject); ok {
						start = int(n.Value)
					}
				}
				if len(args) > 1 {
					if n, ok := args[1].(*NumberObject); ok {
						end = int(n.Value)
					}
				}
				if start < 0 { start = len(l.Elements) + start; if start < 0 { start = 0 } }
				if end < 0 { end = len(l.Elements) + end }
				if start > len(l.Elements) { start = len(l.Elements) }
				if end > len(l.Elements) { end = len(l.Elements) }
				if start > end { return &ListObject{} }
				newElems := make([]Object, end-start)
				copy(newElems, l.Elements[start:end])
				return &ListObject{Elements: newElems}
			case "reduce":
				if len(args) < 2 {
					return NULL_OBJ
				}
				acc := args[1]
				for _, el := range l.Elements {
					acc = interp.applyFunction(args[0], []Object{acc, el})
				}
				return acc
			case "find":
				if len(args) < 1 {
					return NULL_OBJ
				}
				for _, el := range l.Elements {
					val := interp.applyFunction(args[0], []Object{el})
					if isTruthy(val) {
						return el
					}
				}
				return NULL_OBJ
			case "find_index":
				if len(args) < 1 {
					return &NumberObject{Value: -1}
				}
				for idx, el := range l.Elements {
					val := interp.applyFunction(args[0], []Object{el})
					if isTruthy(val) {
						return &NumberObject{Value: float64(idx)}
					}
				}
				return &NumberObject{Value: -1}
			case "index_of":
				if len(args) < 1 {
					return &NumberObject{Value: -1}
				}
				for idx, el := range l.Elements {
					if objectsEqual(el, args[0]) {
						return &NumberObject{Value: float64(idx)}
					}
				}
				return &NumberObject{Value: -1}
			case "flat":
				depth := 1
				if len(args) > 0 {
					if n, ok := args[0].(*NumberObject); ok {
						depth = int(n.Value)
					}
				}
				return &ListObject{Elements: flattenList(l.Elements, depth)}
			case "unique":
				var result []Object
				seen := make(map[string]bool)
				for _, el := range l.Elements {
					key := el.Inspect()
					if !seen[key] {
						seen[key] = true
						result = append(result, el)
					}
				}
				return &ListObject{Elements: result}
			}
			return NULL_OBJ
		},
	}
}

// --- Built-in functions ---

type BuiltinFunction struct {
	Name string
	Fn   func(*Interpreter, ...Object) Object
}

func (b *BuiltinFunction) Type() string    { return "builtin" }
func (b *BuiltinFunction) Inspect() string { return "builtin:" + b.Name }

var builtins = map[string]*BuiltinFunction{
	"print": {
		Name: "print",
		Fn: func(interp *Interpreter, args ...Object) Object {
			if len(args) == 0 {
				fmt.Println()
				return NULL_OBJ
			}
			// Filter out trailing named-args MapObject from builtin call
			printArgs := args
			if len(args) > 1 {
				if _, isNamed := args[len(args)-1].(*MapObject); isNamed && len(args) == 2 {
					printArgs = args[:1]
				}
			}
			if len(printArgs) > 1 {
				return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
					fmt.Sprintf("print() takes exactly 1 argument, got %d", len(printArgs)),
					"use string interpolation: print(\"{a} {b}\")")
			}
			output := printArgs[0].Inspect()
			fmt.Println(output)
			interp.output = append(interp.output, output)
			return NULL_OBJ
		},
	},
	"type_of": {
		Name: "type_of",
		Fn: func(interp *Interpreter, args ...Object) Object {
			if len(args) > 0 {
				return &StringObject{Value: args[0].Type()}
			}
			return NULL_OBJ
		},
	},
	"to_string": {
		Name: "to_string",
		Fn: func(interp *Interpreter, args ...Object) Object {
			if len(args) > 0 {
				return &StringObject{Value: args[0].Inspect()}
			}
			return NULL_OBJ
		},
	},
	"to_number": {
		Name: "to_number",
		Fn: func(interp *Interpreter, args ...Object) Object {
			if len(args) < 1 {
				return NULL_OBJ
			}
			switch v := args[0].(type) {
			case *NumberObject:
				return v
			case *StringObject:
				n, err := strconv.ParseFloat(v.Value, 64)
				if err != nil {
					return NULL_OBJ
				}
				return &NumberObject{Value: n}
			case *BoolObject:
				if v.Value {
					return &NumberObject{Value: 1}
				}
				return &NumberObject{Value: 0}
			}
			return NULL_OBJ
		},
	},
	"to_bool": {
		Name: "to_bool",
		Fn: func(interp *Interpreter, args ...Object) Object {
			if len(args) == 0 {
				return FALSE_OBJ
			}
			switch v := args[0].(type) {
			case *StringObject:
				switch v.Value {
				case "true", "1", "yes":
					return TRUE_OBJ
				default:
					return FALSE_OBJ
				}
			case *BoolObject:
				return v
			default:
				return nativeBoolToObject(isTruthy(args[0]))
			}
		},
	},
	"range": {
		Name: "range",
		Fn: func(interp *Interpreter, args ...Object) Object {
			if len(args) < 2 {
				return &ListObject{}
			}
			start, ok1 := args[0].(*NumberObject)
			end, ok2 := args[1].(*NumberObject)
			if !ok1 || !ok2 {
				return &ListObject{}
			}
			var elements []Object
			for i := int(start.Value); i < int(end.Value); i++ {
				elements = append(elements, &NumberObject{Value: float64(i)})
			}
			return &ListObject{Elements: elements}
		},
	},
	"channel": {
		Name: "channel",
		Fn: func(interp *Interpreter, args ...Object) Object {
			return newRuntimeError(codongerror.E1001_SYNTAX_ERROR,
				"channel() is not supported in eval mode",
				"use 'codong run' or 'codong build' for concurrency features")
		},
	},
	"log": {
		Name: "log",
		Fn: func(interp *Interpreter, args ...Object) Object {
			return newRuntimeError(codongerror.E1004_UNDEFINED_FUNC,
				"log() is not a Codong function",
				"use print() instead: print(\"your message\")")
		},
	},
	"console": {
		Name: "console",
		Fn: func(interp *Interpreter, args ...Object) Object {
			return newRuntimeError(codongerror.E1004_UNDEFINED_FUNC,
				"console.log() is not a Codong function",
				"use print() instead: print(\"your message\")")
		},
	},
	"len": {
		Name: "len",
		Fn: func(interp *Interpreter, args ...Object) Object {
			return newRuntimeError(codongerror.E1004_UNDEFINED_FUNC,
				"len() is not a global function in Codong",
				"use .len() method instead: items.len(), str.len()")
		},
	},
	"var": {
		Name: "var",
		Fn: func(interp *Interpreter, args ...Object) Object {
			return newRuntimeError(codongerror.E1001_SYNTAX_ERROR,
				"'var' is not a Codong keyword",
				"assign directly: x = value, or use 'const x = value' for constants")
		},
	},
	"let": {
		Name: "let",
		Fn: func(interp *Interpreter, args ...Object) Object {
			return newRuntimeError(codongerror.E1001_SYNTAX_ERROR,
				"'let' is not a Codong keyword",
				"assign directly: x = value, or use 'const x = value' for constants")
		},
	},
}

// ErrorModuleObject represents the error built-in module.
type ErrorModuleObject struct{}

func (e *ErrorModuleObject) Type() string    { return "module" }
func (e *ErrorModuleObject) Inspect() string { return "<module:error>" }


// errorModuleSingleton is the global error module object.
var errorModuleSingleton = &ErrorModuleObject{}

// --- Helpers ---

func nativeBoolToObject(value bool) *BoolObject {
	if value {
		return TRUE_OBJ
	}
	return FALSE_OBJ
}

func isTruthy(obj Object) bool {
	switch obj := obj.(type) {
	case *NullObject:
		return false
	case *BoolObject:
		return obj.Value
	default:
		return true // 0, "", [], {} are truthy per SPEC
	}
}

func isError(obj Object) bool {
	if obj != nil {
		if e, ok := obj.(*ErrorObject); ok {
			return e.IsRuntime
		}
	}
	return false
}

func isRuntimeError(obj Object) bool {
	return isError(obj)
}

func newRuntimeError(code, message, fix string) *ErrorObject {
	return &ErrorObject{
		Error:     codongerror.New(code, message, codongerror.WithFix(fix)),
		IsRuntime: true,
	}
}

func objectsEqual(a, b Object) bool {
	if a == b {
		return true // same pointer (reference equality for list/map)
	}
	if a.Type() != b.Type() {
		return false
	}
	switch a := a.(type) {
	case *NumberObject:
		return a.Value == b.(*NumberObject).Value
	case *StringObject:
		return a.Value == b.(*StringObject).Value
	case *BoolObject:
		return a.Value == b.(*BoolObject).Value
	case *NullObject:
		return true
	}
	return false
}

func extractParamNames(params []*parser.TypedIdentifier) []string {
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	return names
}

func compareObjects(a, b Object) int {
	if a.Type() == "number" && b.Type() == "number" {
		av := a.(*NumberObject).Value
		bv := b.(*NumberObject).Value
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
		return 0
	}
	if a.Type() == "string" && b.Type() == "string" {
		return strings.Compare(a.(*StringObject).Value, b.(*StringObject).Value)
	}
	return 0
}

func flattenList(elements []Object, depth int) []Object {
	if depth <= 0 {
		return elements
	}
	var result []Object
	for _, el := range elements {
		if list, ok := el.(*ListObject); ok {
			result = append(result, flattenList(list.Elements, depth-1)...)
		} else {
			result = append(result, el)
		}
	}
	return result
}

// evalMapMethod returns a builtin function for map methods, or nil if not a method.
func (i *Interpreter) evalMapMethod(m *MapObject, method string) Object {
	switch method {
	case "len", "keys", "values", "entries", "has", "get", "delete", "merge", "map_values", "filter":
		// it's a method
	default:
		return nil // not a method, treat as field access
	}
	return &BuiltinFunction{
		Name: "map." + method,
		Fn: func(interp *Interpreter, args ...Object) Object {
			switch method {
			case "len":
				return &NumberObject{Value: float64(len(m.Entries))}
			case "keys":
				keys := make([]Object, 0, len(m.Order))
				for _, k := range m.Order {
					keys = append(keys, &StringObject{Value: k})
				}
				return &ListObject{Elements: keys}
			case "values":
				vals := make([]Object, 0, len(m.Order))
				for _, k := range m.Order {
					vals = append(vals, m.Entries[k])
				}
				return &ListObject{Elements: vals}
			case "entries":
				entries := make([]Object, 0, len(m.Order))
				for _, k := range m.Order {
					entries = append(entries, &ListObject{Elements: []Object{
						&StringObject{Value: k}, m.Entries[k],
					}})
				}
				return &ListObject{Elements: entries}
			case "has":
				if len(args) < 1 {
					return FALSE_OBJ
				}
				key, ok := args[0].(*StringObject)
				if !ok {
					return FALSE_OBJ
				}
				_, exists := m.Entries[key.Value]
				return nativeBoolToObject(exists)
			case "get":
				if len(args) < 1 {
					return NULL_OBJ
				}
				key, ok := args[0].(*StringObject)
				if !ok {
					return NULL_OBJ
				}
				if val, exists := m.Entries[key.Value]; exists {
					return val
				}
				if len(args) > 1 {
					return args[1] // default value
				}
				return NULL_OBJ
			case "delete":
				if len(args) < 1 {
					return m
				}
				key, ok := args[0].(*StringObject)
				if !ok {
					return m
				}
				delete(m.Entries, key.Value)
				newOrder := make([]string, 0, len(m.Order))
				for _, k := range m.Order {
					if k != key.Value {
						newOrder = append(newOrder, k)
					}
				}
				m.Order = newOrder
				return m
			case "merge":
				if len(args) < 1 {
					return m
				}
				other, ok := args[0].(*MapObject)
				if !ok {
					return m
				}
				// Non-mutating: return new map
				newEntries := make(map[string]Object)
				newOrder := make([]string, 0)
				for _, k := range m.Order {
					newEntries[k] = m.Entries[k]
					newOrder = append(newOrder, k)
				}
				for _, k := range other.Order {
					if _, exists := newEntries[k]; !exists {
						newOrder = append(newOrder, k)
					}
					newEntries[k] = other.Entries[k]
				}
				return &MapObject{Entries: newEntries, Order: newOrder}
			case "map_values":
				if len(args) < 1 {
					return m
				}
				newEntries := make(map[string]Object)
				newOrder := make([]string, len(m.Order))
				copy(newOrder, m.Order)
				for _, k := range m.Order {
					result := interp.applyFunction(args[0], []Object{m.Entries[k], &StringObject{Value: k}})
					newEntries[k] = result
				}
				return &MapObject{Entries: newEntries, Order: newOrder}
			case "filter":
				if len(args) < 1 {
					return m
				}
				newEntries := make(map[string]Object)
				newOrder := make([]string, 0)
				for _, k := range m.Order {
					result := interp.applyFunction(args[0], []Object{m.Entries[k], &StringObject{Value: k}})
					if isTruthy(result) {
						newEntries[k] = m.Entries[k]
						newOrder = append(newOrder, k)
					}
				}
				return &MapObject{Entries: newEntries, Order: newOrder}
			}
			return NULL_OBJ
		},
	}
}
