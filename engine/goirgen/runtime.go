package goirgen

// RuntimeSource is the Codong runtime library embedded in every generated Go program.
// It provides dynamic types, operators, and built-in functions.
const RuntimeSource = `
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// --- Codong Runtime ---

// Value is the universal Codong value type.
type Value = interface{}

// CodongList wraps a slice for Codong list operations.
type CodongList struct {
	Elements []Value
}

// CodongMap wraps an ordered map for Codong map operations.
type CodongMap struct {
	Entries map[string]Value
	Order   []string
}

// CodongError represents a structured Codong error.
type CodongError struct {
	Code    string
	Message string
	Fix     string
	Retry   bool
	Docs    string
	Source  string
	Context *CodongMap
	Cause   *CodongError
	IsError bool // always true, used for type checking
}

func (e *CodongError) Error() string {
	s := fmt.Sprintf("[%s] %s", e.Code, e.Message)
	if e.Fix != "" {
		s += fmt.Sprintf("\n  fix: %s", e.Fix)
	}
	return s
}

// CodongFn wraps a Codong function value.
type CodongFn = func(args ...Value) Value

// --- Constructors ---

func cList(elems ...Value) *CodongList {
	return &CodongList{Elements: elems}
}

func cMap(kvs ...interface{}) *CodongMap {
	m := &CodongMap{Entries: map[string]Value{}, Order: []string{}}
	for i := 0; i+1 < len(kvs); i += 2 {
		key := kvs[i].(string)
		m.Entries[key] = kvs[i+1]
		m.Order = append(m.Order, key)
	}
	return m
}

func cError(code, msg string, opts ...interface{}) *CodongError {
	e := &CodongError{Code: code, Message: msg, Source: "codong", IsError: true}
	for i := 0; i+1 < len(opts); i += 2 {
		key, ok := opts[i].(string)
		if !ok { continue }
		switch key {
		case "fix":
			e.Fix = toString(opts[i+1])
		case "retry":
			e.Retry = toBool(opts[i+1])
		case "docs":
			e.Docs = toString(opts[i+1])
		case "context":
			if cm, ok := opts[i+1].(*CodongMap); ok {
				e.Context = cm
			}
		}
	}
	// Auto-generate docs URL if not explicitly set
	if e.Docs == "" && e.Code != "" {
		e.Docs = "https://codong.org/errors/" + e.Code
	}
	return e
}

// --- Type Conversion ---

func toFloat(v Value) float64 {
	if v == nil { return 0 }
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case bool:
		if n { return 1 }
		return 0
	case string:
		f, err := strconv.ParseFloat(n, 64)
		if err != nil { return 0 }
		return f
	}
	return 0
}

// toNumber converts to number, returns nil for invalid conversions (matching eval behavior)
func toNumber(v Value) Value {
	if v == nil { return nil }
	switch n := v.(type) {
	case float64: return n
	case int: return float64(n)
	case int64: return float64(n)
	case bool:
		if n { return float64(1) }
		return float64(0)
	case string:
		f, err := strconv.ParseFloat(n, 64)
		if err != nil { return nil }
		return f
	}
	return nil
}

func toString(v Value) string {
	if v == nil { return "null" }
	switch s := v.(type) {
	case string:
		return s
	case float64:
		if s == math.Trunc(s) && !math.IsInf(s, 0) {
			return strconv.FormatInt(int64(s), 10)
		}
		return strconv.FormatFloat(s, 'f', -1, 64)
	case bool:
		if s { return "true" }
		return "false"
	case *CodongList:
		return "[...]"
	case *CodongMap:
		return "{...}"
	case *CodongError:
		// Errors print as "null" when used as values (e.g., print(fs.read("missing")))
		// Access error fields via .code, .message, .fix etc.
		return "null"
	case func(...Value) Value:
		return "fn (...)"
	}
	return fmt.Sprintf("%v", v)
}

func toBool(v Value) bool {
	if v == nil { return false }
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true" || b == "1"
	}
	return true // 0, "", [], {} are all truthy in Codong
}

// toBoolV is the Codong to_bool() built-in function — returns Value for use in expressions
// For strings: only "true"/"1" are true (explicit conversion)
// For everything else: use Codong isTruthy (0, "", [], {} are truthy)
func toBoolV(v Value) Value {
	if v == nil { return false }
	switch b := v.(type) {
	case bool: return b
	case string:
		// Explicit string conversion:
		// "true"/"1" → true, "false"/"0" → false, "" → true (empty string is truthy in Codong)
		// everything else → false
		switch b {
		case "true", "1": return true
		case "false", "0": return false
		case "": return true
		default: return false
		}
	}
	// All non-nil, non-false values are truthy in Codong (0, "", [], {} included)
	return true
}

// isTruthyV wraps isTruthy to return Value
func isTruthyV(v Value) Value { return isTruthy(v) }

func isTruthy(v Value) bool {
	if v == nil { return false }
	if b, ok := v.(bool); ok { return b }
	return true
}

// cOr implements short-circuit || that returns the first truthy value (not a boolean)
func cOr(a Value, b func() Value) Value {
	if isTruthy(a) { return a }
	return b()
}

// cAnd implements short-circuit && that returns values (not a boolean)
func cAnd(a Value, b func() Value) Value {
	if !isTruthy(a) { return a }
	return b()
}

func typeOf(v Value) string {
	if v == nil { return "null" }
	switch v.(type) {
	case float64: return "number"
	case string: return "string"
	case bool: return "bool"
	case *CodongList: return "list"
	case *CodongMap: return "map"
	case *CodongError: return "error"
	case func(...Value) Value: return "fn"
	}
	return "unknown"
}

func toList(v Value) *CodongList {
	if l, ok := v.(*CodongList); ok { return l }
	// Maps iterate as list of keys
	if m, ok := v.(*CodongMap); ok {
		elems := make([]Value, len(m.Order))
		for i, k := range m.Order { elems[i] = k }
		return &CodongList{Elements: elems}
	}
	// Strings iterate as list of characters
	if s, ok := v.(string); ok {
		elems := make([]Value, len(s))
		for i, ch := range s { elems[i] = string(ch) }
		return &CodongList{Elements: elems}
	}
	return &CodongList{}
}

func toMap(v Value) *CodongMap {
	if m, ok := v.(*CodongMap); ok { return m }
	return cMap()
}

// --- Operators ---

func cAdd(a, b Value) Value {
	if sa, ok := a.(string); ok { return sa + toString(b) }
	return toFloat(a) + toFloat(b)
}

func cSub(a, b Value) Value { return toFloat(a) - toFloat(b) }
func cMul(a, b Value) Value { return toFloat(a) * toFloat(b) }
func cDiv(a, b Value) Value {
	bf := toFloat(b)
	if bf == 0 { panic(cError("E9003_PANIC", "division by zero")) }
	return toFloat(a) / bf
}
func cMod(a, b Value) Value { return math.Mod(toFloat(a), toFloat(b)) }

func cEq(a, b Value) bool {
	if a == nil && b == nil { return true }
	if a == nil || b == nil { return false }
	switch av := a.(type) {
	case float64:
		bv, ok := b.(float64); return ok && av == bv
	case string:
		bv, ok := b.(string); return ok && av == bv
	case bool:
		bv, ok := b.(bool); return ok && av == bv
	case *CodongList:
		return a == b // reference equality
	case *CodongMap:
		return a == b // reference equality
	}
	return false
}

func cLt(a, b Value) bool  { return toFloat(a) < toFloat(b) }
func cGt(a, b Value) bool  { return toFloat(a) > toFloat(b) }
func cLte(a, b Value) bool { return toFloat(a) <= toFloat(b) }
func cGte(a, b Value) bool { return toFloat(a) >= toFloat(b) }

// --- Member Access ---

func cGet(obj Value, key string) Value {
	switch o := obj.(type) {
	case *CodongMap:
		if v, ok := o.Entries[key]; ok { return v }
		// Special: server.middleware returns the middleware namespace
		if key == "middleware" {
			if t, ok := o.Entries["_type"].(string); ok && t == "server" {
				return cWebMiddlewareNS
			}
		}
		return nil
	case *CodongError:
		switch key {
		case "code": return o.Code
		case "message": return o.Message
		case "fix": return o.Fix
		case "retry": return o.Retry
		case "docs": return o.Docs
		case "source": return o.Source
		case "context":
			if o.Context != nil { return o.Context }
			return nil
		}
	}
	return nil
}

func cSet(obj Value, key string, val Value) {
	if m, ok := obj.(*CodongMap); ok {
		if _, exists := m.Entries[key]; !exists {
			m.Order = append(m.Order, key)
		}
		m.Entries[key] = val
	}
}

func cIndex(obj Value, idx Value) Value {
	switch o := obj.(type) {
	case *CodongList:
		i := int(toFloat(idx))
		if i < 0 { i = len(o.Elements) + i }
		if i >= 0 && i < len(o.Elements) { return o.Elements[i] }
		return nil
	case *CodongMap:
		if s, ok := idx.(string); ok {
			if v, exists := o.Entries[s]; exists { return v; _ = ok }
		}
	case string:
		i := int(toFloat(idx))
		if i < 0 { i = len(o) + i }
		if i >= 0 && i < len(o) { return string(o[i]) }
	}
	return nil
}

func cSetIndex(obj Value, idx Value, val Value) {
	switch o := obj.(type) {
	case *CodongList:
		i := int(toFloat(idx))
		if i < 0 { i = len(o.Elements) + i }
		if i >= 0 && i < len(o.Elements) { o.Elements[i] = val; return }
		panic(cError("E1005_INDEX_ERROR", fmt.Sprintf("list index %d out of bounds (length %d)", int(toFloat(idx)), len(o.Elements))))
	case *CodongMap:
		if s, ok := idx.(string); ok { cSet(obj, s, val) }
	}
}

// --- List Methods ---

func cListMethod(l *CodongList, method string, args ...Value) Value {
	switch method {
	case "len":
		return float64(len(l.Elements))
	case "push":
		if len(args) > 0 { l.Elements = append(l.Elements, args[0]) }
		return l
	case "pop":
		if len(l.Elements) == 0 { return nil }
		last := l.Elements[len(l.Elements)-1]
		l.Elements = l.Elements[:len(l.Elements)-1]
		return last
	case "shift":
		if len(l.Elements) == 0 { return nil }
		first := l.Elements[0]
		l.Elements = l.Elements[1:]
		return first
	case "unshift":
		if len(args) > 0 { l.Elements = append([]Value{args[0]}, l.Elements...) }
		return l
	case "first":
		if len(l.Elements) > 0 { return l.Elements[0] }
		return nil
	case "last":
		if len(l.Elements) > 0 { return l.Elements[len(l.Elements)-1] }
		return nil
	case "join":
		sep := ","
		if len(args) > 0 { sep = toString(args[0]) }
		parts := make([]string, len(l.Elements))
		for i, el := range l.Elements { parts[i] = toString(el) }
		return strings.Join(parts, sep)
	case "contains":
		if len(args) == 0 { return false }
		for _, el := range l.Elements { if cEq(el, args[0]) { return true } }
		return false
	case "sort":
		if len(args) > 0 {
			fn := args[0].(func(...Value) Value)
			sort.SliceStable(l.Elements, func(i, j int) bool {
				r := fn(l.Elements[i], l.Elements[j])
				return toFloat(r) < 0
			})
		} else {
			sort.SliceStable(l.Elements, func(i, j int) bool {
				return toString(l.Elements[i]) < toString(l.Elements[j])
			})
		}
		return l
	case "reverse":
		for i, j := 0, len(l.Elements)-1; i < j; i, j = i+1, j-1 {
			l.Elements[i], l.Elements[j] = l.Elements[j], l.Elements[i]
		}
		return l
	case "slice":
		start := 0; end := len(l.Elements)
		if len(args) > 0 { start = int(toFloat(args[0])) }
		if len(args) > 1 { end = int(toFloat(args[1])) }
		if start < 0 { start = len(l.Elements) + start; if start < 0 { start = 0 } }
		if end < 0 { end = len(l.Elements) + end }
		if end > len(l.Elements) { end = len(l.Elements) }
		if start > end { return cList() }
		cp := make([]Value, end-start)
		copy(cp, l.Elements[start:end])
		return &CodongList{Elements: cp}
	case "map":
		fn := args[0].(func(...Value) Value)
		result := make([]Value, len(l.Elements))
		for i, el := range l.Elements { result[i] = fn(el) }
		return &CodongList{Elements: result}
	case "filter":
		fn := args[0].(func(...Value) Value)
		var result []Value
		for _, el := range l.Elements { if isTruthy(fn(el)) { result = append(result, el) } }
		if result == nil { result = []Value{} }
		return &CodongList{Elements: result}
	case "reduce":
		fn := args[0].(func(...Value) Value)
		acc := args[1]
		for _, el := range l.Elements { acc = fn(acc, el) }
		return acc
	case "find":
		fn := args[0].(func(...Value) Value)
		for _, el := range l.Elements { if isTruthy(fn(el)) { return el } }
		return nil
	case "find_index":
		fn := args[0].(func(...Value) Value)
		for i, el := range l.Elements { if isTruthy(fn(el)) { return float64(i) } }
		return float64(-1)
	case "index_of":
		for i, el := range l.Elements { if cEq(el, args[0]) { return float64(i) } }
		return float64(-1)
	case "unique":
		seen := map[string]bool{}
		var result []Value
		for _, el := range l.Elements {
			key := toString(el)
			if !seen[key] { seen[key] = true; result = append(result, el) }
		}
		return &CodongList{Elements: result}
	case "flat":
		depth := 1
		if len(args) > 0 { depth = int(toFloat(args[0])) }
		return &CodongList{Elements: flattenList(l.Elements, depth)}
	case "delete":
		if len(args) > 0 {
			idx := int(toFloat(args[0]))
			if idx >= 0 && idx < len(l.Elements) {
				l.Elements = append(l.Elements[:idx], l.Elements[idx+1:]...)
			}
		}
		return l
	}
	return nil
}

func flattenList(elements []Value, depth int) []Value {
	if depth <= 0 { return elements }
	var result []Value
	for _, el := range elements {
		if sub, ok := el.(*CodongList); ok {
			result = append(result, flattenList(sub.Elements, depth-1)...)
		} else {
			result = append(result, el)
		}
	}
	return result
}

// --- String Methods ---

func cStrMethod(s string, method string, args ...Value) Value {
	switch method {
	case "len": return float64(len(s))
	case "upper": return strings.ToUpper(s)
	case "lower": return strings.ToLower(s)
	case "trim": return strings.TrimSpace(s)
	case "trim_start": return strings.TrimLeft(s, " \t\n\r")
	case "trim_end": return strings.TrimRight(s, " \t\n\r")
	case "split":
		sep := ""; if len(args) > 0 { sep = toString(args[0]) }
		var parts []string
		if sep == "" { for _, ch := range s { parts = append(parts, string(ch)) }
		} else { parts = strings.Split(s, sep) }
		elems := make([]Value, len(parts))
		for i, p := range parts { elems[i] = p }
		return &CodongList{Elements: elems}
	case "contains": return strings.Contains(s, toString(args[0]))
	case "starts_with": return strings.HasPrefix(s, toString(args[0]))
	case "ends_with": return strings.HasSuffix(s, toString(args[0]))
	case "replace": return strings.ReplaceAll(s, toString(args[0]), toString(args[1]))
	case "index_of":
		idx := strings.Index(s, toString(args[0]))
		return float64(idx)
	case "slice":
		start := 0; end := len(s)
		if len(args) > 0 { start = int(toFloat(args[0])) }
		if len(args) > 1 { end = int(toFloat(args[1])) }
		if start < 0 { start = len(s) + start; if start < 0 { start = 0 } }
		if end < 0 { end = len(s) + end }
		if end > len(s) { end = len(s) }
		if start > end { return "" }
		return s[start:end]
	case "repeat":
		n := int(toFloat(args[0]))
		return strings.Repeat(s, n)
	case "to_number":
		f, err := strconv.ParseFloat(s, 64)
		if err != nil { return nil }
		return f
	case "to_bool":
		return s == "true" || s == "1"
	case "match":
		re := regexp.MustCompile(toString(args[0]))
		matches := re.FindAllString(s, -1)
		if matches == nil { return cList() }
		elems := make([]Value, len(matches))
		for i, m := range matches { elems[i] = m }
		return &CodongList{Elements: elems}
	}
	return nil
}

// --- Map Methods ---

func cMapMethod(m *CodongMap, method string, args ...Value) Value {
	switch method {
	case "len": return float64(len(m.Entries))
	case "keys":
		elems := make([]Value, len(m.Order))
		for i, k := range m.Order { elems[i] = k }
		return &CodongList{Elements: elems}
	case "values":
		elems := make([]Value, len(m.Order))
		for i, k := range m.Order { elems[i] = m.Entries[k] }
		return &CodongList{Elements: elems}
	case "entries":
		elems := make([]Value, len(m.Order))
		for i, k := range m.Order {
			elems[i] = &CodongList{Elements: []Value{k, m.Entries[k]}}
		}
		return &CodongList{Elements: elems}
	case "has":
		_, ok := m.Entries[toString(args[0])]
		return ok
	case "get":
		k := toString(args[0])
		if v, ok := m.Entries[k]; ok { return v }
		if len(args) > 1 { return args[1] }
		return nil
	case "delete":
		k := toString(args[0])
		delete(m.Entries, k)
		newOrder := make([]string, 0, len(m.Order))
		for _, o := range m.Order { if o != k { newOrder = append(newOrder, o) } }
		m.Order = newOrder
		return m
	case "merge":
		other := args[0].(*CodongMap)
		nm := &CodongMap{Entries: map[string]Value{}, Order: make([]string, len(m.Order))}
		copy(nm.Order, m.Order)
		for k, v := range m.Entries { nm.Entries[k] = v }
		for _, k := range other.Order {
			if _, exists := nm.Entries[k]; !exists { nm.Order = append(nm.Order, k) }
			nm.Entries[k] = other.Entries[k]
		}
		return nm
	case "map_values":
		fn := args[0].(func(...Value) Value)
		nm := &CodongMap{Entries: map[string]Value{}, Order: make([]string, len(m.Order))}
		copy(nm.Order, m.Order)
		for _, k := range m.Order { nm.Entries[k] = fn(m.Entries[k], k) }
		return nm
	case "filter":
		fn := args[0].(func(...Value) Value)
		nm := &CodongMap{Entries: map[string]Value{}, Order: []string{}}
		for _, k := range m.Order {
			if isTruthy(fn(m.Entries[k], k)) { nm.Entries[k] = m.Entries[k]; nm.Order = append(nm.Order, k) }
		}
		return nm
	}
	return nil
}

// --- Method Dispatch ---

func cCall(obj Value, method string, args ...Value) Value {
	switch o := obj.(type) {
	case *CodongList: return cListMethod(o, method, args...)
	case *CodongMap:
		// Server/group objects — route registration
		if t, ok := o.Entries["_type"].(string); ok {
			switch t {
			case "server":
				switch method {
				case "get", "post", "put", "delete", "patch":
					return cWebRoute(strings.ToUpper(method), args[0], args[1])
				case "group":
					if len(args) > 0 { return cMap("_type", "group", "prefix", args[0]) }
				case "use":
					if len(args) > 0 {
						// Handle server.use("/prefix", middleware)
						if len(args) >= 2 {
							if prefix, ok := args[0].(string); ok {
								if mw, ok := args[1].(*CodongMap); ok {
									cSet(mw, "prefix", prefix)
								}
								cWebMiddlewares = append(cWebMiddlewares, args[1])
								return nil
							}
						}
						cWebMiddlewares = append(cWebMiddlewares, args[0])
					}
					return nil
				case "ws":
					if len(args) >= 2 { return cWebWsRoute(args[0], args[1]) }
					return nil
				case "ws_broadcast":
					if len(args) >= 2 {
						msg := toString(args[1])
						cWsHub.mu.RLock()
						for _, ch := range cWsHub.conns { select { case ch <- msg: default: } }
						cWsHub.mu.RUnlock()
					}
					return nil
				case "close":
					return nil
				}
			case "web_middleware_ns":
				switch method {
				case "cors":
					return cMap("_mw_type", "cors")
				case "logger":
					return cMap("_mw_type", "logger")
				case "recover":
					return cMap("_mw_type", "recover")
				case "auth_bearer":
					var validator Value
					if len(args) > 0 { validator = args[0] }
					return cMap("_mw_type", "auth_bearer", "validator", validator)
				case "multipart":
					m := cMap("_mw_type", "multipart")
					if len(args) > 0 {
						if opts, ok := args[0].(*CodongMap); ok {
							if v, exists := opts.Entries["max_file_size"]; exists { cSet(m, "max_file_size", v) }
							if v, exists := opts.Entries["allowed_types"]; exists { cSet(m, "allowed_types", v) }
						}
					}
					return m
				case "session":
					m := cMap("_mw_type", "session")
					if len(args) > 0 {
						if opts, ok := args[0].(*CodongMap); ok {
							for k, v := range opts.Entries { cSet(m, k, v) }
						}
					}
					return m
				}
			case "group":
				prefix := toString(o.Entries["prefix"])
				switch method {
				case "get", "post", "put", "delete", "patch":
					fullPath := prefix + toString(args[0])
					return cWebRoute(strings.ToUpper(method), fullPath, args[1])
				}
			}
		}
		// User-defined function fields take priority
		if fn, ok := o.Entries[method]; ok {
			if f, ok := fn.(func(...Value) Value); ok { return f(args...) }
			// If the value is a map and we're calling it with args, do map lookup
			if subMap, ok := fn.(*CodongMap); ok && len(args) > 0 {
				key := toString(args[0])
				if v, exists := subMap.Entries[key]; exists { return v }
				if len(args) > 1 { return args[1] } // default value
				return nil
			}
			// If called with no args, return the value itself
			if len(args) == 0 { return fn }
		}
		return cMapMethod(o, method, args...)
	case string: return cStrMethod(o, method, args...)
	case *CodongError:
		v := cGet(obj, method)
		if v != nil { return v }
	}
	return nil
}

// --- Built-in Functions ---

func cPrint(v Value) {
	fmt.Println(toString(v))
}

func cPrintV(v Value) Value {
	fmt.Println(toString(v))
	return nil
}

func cPrintError(code, msg string, opts ...string) {
	e := cError(code, msg)
	if len(opts) > 0 && opts[0] != "" {
		e.Fix = opts[0]
	}
	cPanicExit(e)
}

func cPrintMultiErr(count int) Value {
	e := cError("E1005_ARG_ERROR", fmt.Sprintf("print() takes exactly 1 argument (%d given)", count), "fix", "use string interpolation: print(\"${a} ${b}\")")
	cPanicExit(e)
	return nil
}

func cDiscard(_ Value) {}

func cPanicExit(ce *CodongError) {
	fmt.Printf("[%s] %s\n", ce.Code, ce.Message)
	if ce.Fix != "" {
		fmt.Printf("  fix: %s\n", ce.Fix)
	}
	os.Exit(1)
}

func cRange(start, end float64) *CodongList {
	var elems []Value
	for i := start; i < end; i++ { elems = append(elems, i) }
	return &CodongList{Elements: elems}
}

// --- Error Propagation (?) ---

type cReturnSignal struct{ Value Value }

func cPropagate(v Value) Value {
	// In assignment context (err = expr?), return the error for inspection
	if _, ok := v.(*CodongError); ok {
		return v
	}
	if m, ok := v.(*CodongMap); ok {
		if errVal, ok := m.Entries["error"]; ok {
			if _, ok := errVal.(*CodongError); ok {
				return errVal
			}
		}
	}
	return v
}

func cPropagateStmt(v Value) {
	// In standalone context (expr?), panic to propagate error up call stack
	if e, ok := v.(*CodongError); ok {
		panic(&cReturnSignal{Value: e})
	}
}

// --- Web Module ---

var cWebRoutes []struct{ method, pattern string; handler func(...Value) Value }
var cWebServers []*struct{ port int }
var cWebMiddlewares []Value
var cWebMiddlewareNS = &CodongMap{Entries: map[string]interface{}{"_type": "web_middleware_ns"}, Order: []string{"_type"}}
var cWebAuthContext Value // stores auth context from middleware for current request

// Session support
var cSessionStore = make(map[string]*CodongMap)
var cSessionMu sync.Mutex
type cSessionContext struct {
	id     string
	data   *CodongMap
	name   string
	maxAge int
}
var cCurrentSession *cSessionContext

func cWebRoute(method string, pattern Value, handler Value) Value {
	p := toString(pattern)
	// Convert :param to {param}
	parts := strings.Split(p, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") { parts[i] = "{" + part[1:] + "}" }
	}
	p = strings.Join(parts, "/")
	fn := handler.(func(...Value) Value)
	cWebRoutes = append(cWebRoutes, struct{ method, pattern string; handler func(...Value) Value }{method, p, fn})
	return nil
}

func cWebMakeServer(port int) Value {
	cWebServers = append(cWebServers, &struct{ port int }{port})
	return cMap("_type", "server", "port", float64(port))
}

func cWebServeAll() {
	if len(cWebServers) == 0 { return }
	srv := cWebServers[0]
	cWebServe(srv.port)
}

func cWebGet(pattern string, handler func(...Value) Value) {
	cWebRoutes = append(cWebRoutes, struct{ method, pattern string; handler func(...Value) Value }{"GET", pattern, handler})
}
func cWebPost(pattern string, handler func(...Value) Value) {
	cWebRoutes = append(cWebRoutes, struct{ method, pattern string; handler func(...Value) Value }{"POST", pattern, handler})
}

func cWebServe(port int) Value {
	mux := http.NewServeMux()
	for _, r := range cWebRoutes {
		route := r
		pattern := fmt.Sprintf("%s %s", route.method, route.pattern)
		mux.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
			reqMap := cMap(
				"method", req.Method,
				"path", req.URL.Path,
				"url", req.URL.String(),
			)
			// Parse query
			qm := cMap()
			for k, v := range req.URL.Query() { cSet(qm, k, v[0]) }
			cSet(reqMap, "query", qm)
			// Parse params
			pm := cMap()
			parts := strings.Split(route.pattern, "/")
			for _, p := range parts {
				if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
					name := p[1:len(p)-1]
					cSet(pm, name, req.PathValue(name))
				}
			}
			cSet(reqMap, "param", pm)
			// Parse headers (store both original and lowercase for case-insensitive lookup)
			hm := cMap()
			for k, v := range req.Header {
				cSet(hm, k, v[0])
				cSet(hm, strings.ToLower(k), v[0])
			}
			cSet(reqMap, "headers", hm)
			cSet(reqMap, "header", hm) // alias
			// Cookies
			cm := cMap()
			for _, c := range req.Cookies() { cSet(cm, c.Name, c.Value) }
			cSet(reqMap, "cookies", cm)
			cSet(reqMap, "cookie", func(args ...Value) Value {
				if len(args) > 0 { if v, ok := cm.Entries[toString(args[0])]; ok { return v } }
				return nil
			})
			// Client IP
			ip := req.RemoteAddr
			if fwd := req.Header.Get("X-Forwarded-For"); fwd != "" { ip = fwd }
			cSet(reqMap, "ip", ip)
			// Build context from auth middleware
			ctxMap := cMap()
			if cWebAuthContext != nil {
				if authMap, ok := cWebAuthContext.(*CodongMap); ok {
					for _, k := range authMap.Order {
						cSet(ctxMap, k, authMap.Entries[k])
					}
				}
			}
			// Also check headers for backward compat
			for k, v := range req.Header {
				if strings.HasPrefix(k, "X-Codong-Auth-") {
					ctxKey := strings.ToLower(k[len("X-Codong-Auth-"):])
					if _, exists := ctxMap.Entries[ctxKey]; !exists {
						cSet(ctxMap, ctxKey, v[0])
					}
				}
			}
			cSet(reqMap, "context", ctxMap)
			// Session data (from session middleware)
			if cCurrentSession != nil {
				sess := cCurrentSession.data
				cSet(reqMap, "session", sess)
			}
			// query_all() returns the full query map
			cSet(reqMap, "query_all", func(args ...Value) Value { return qm })
			// Parse body (with multipart support)
			contentType := req.Header.Get("Content-Type")
			if strings.HasPrefix(contentType, "multipart/form-data") {
				cParseMultipartBody(req, reqMap)
			} else if req.Body != nil {
				bodyBytes, _ := io.ReadAll(req.Body)
				if len(bodyBytes) > 0 {
					var jdata interface{}
					if json.Unmarshal(bodyBytes, &jdata) == nil {
						cSet(reqMap, "body", goToValue(jdata))
					} else {
						cSet(reqMap, "body", string(bodyBytes))
					}
				}
			}
			// Call handler
			result := route.handler(reqMap)
			writeResponse(w, req, result)
		})
	}
	// Register WebSocket routes
	for _, ws := range cWebWSRoutes {
		wsHandler := ws.handler
		mux.HandleFunc(ws.pattern, cWebWSUpgradeHandler(wsHandler))
	}
	addr := fmt.Sprintf(":%d", port)
	// Apply middlewares
	var finalHandler http.Handler = mux
	for idx := len(cWebMiddlewares) - 1; idx >= 0; idx-- {
		mw := cWebMiddlewares[idx]
		if m, ok := mw.(*CodongMap); ok {
			mwType := toString(m.Entries["_mw_type"])
			prev := finalHandler
			switch mwType {
			case "cors":
				finalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Access-Control-Allow-Origin", "*")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
					if r.Method == "OPTIONS" { w.WriteHeader(204); return }
					prev.ServeHTTP(w, r)
				})
			case "static":
				finalHandler = cWebApplyStatic(m, finalHandler)
			case "logger":
				finalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					fmt.Fprintf(os.Stderr, "%s %s\n", r.Method, r.URL.Path)
					prev.ServeHTTP(w, r)
				})
			case "recover":
				finalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer func() {
						if err := recover(); err != nil {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(500)
							fmt.Fprint(w, "{\"error\":\"internal server error\"}")
						}
					}()
					prev.ServeHTTP(w, r)
				})
			case "session":
				sessionName := "sid"
				sessionMaxAge := 3600
				if sn, ok := m.Entries["name"].(string); ok && sn != "" && sn != "null" { sessionName = sn }
				if ma, ok := m.Entries["max_age"]; ok { sessionMaxAge = int(toFloat(ma)) }
				sName := sessionName
				sMaxAge := sessionMaxAge
				prevS := finalHandler
				finalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Read session ID from cookie
					var sessID string
					if c, err := r.Cookie(sName); err == nil { sessID = c.Value }
					if sessID == "" {
						sessID = fmt.Sprintf("sess-%d-%d", time.Now().UnixNano(), cWsIDCounter)
						cWsIDMu.Lock(); cWsIDCounter++; cWsIDMu.Unlock()
					}
					// Get or create session data
					cSessionMu.Lock()
					sess, ok := cSessionStore[sessID]
					if !ok { sess = cMap(); cSessionStore[sessID] = sess }
					cSessionMu.Unlock()
					// Store session in context for request handler
					cCurrentSession = &cSessionContext{id: sessID, data: sess, name: sName, maxAge: sMaxAge}
					// Set session cookie BEFORE response is written
					http.SetCookie(w, &http.Cookie{Name: sName, Value: sessID, Path: "/", MaxAge: sMaxAge, HttpOnly: true})
					prevS.ServeHTTP(w, r)
					cCurrentSession = nil
				})
			case "multipart":
				// multipart middleware is handled at parse time, not as HTTP middleware
			case "auth_bearer":
				validator := m.Entries["validator"]
				finalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					auth := r.Header.Get("Authorization")
					if !strings.HasPrefix(auth, "Bearer ") {
						w.WriteHeader(401)
						fmt.Fprint(w, "{\"error\":\"unauthorized\"}")
						return
					}
					token := auth[7:]
					result := cCallFn(validator, token)
					if result == nil || result == false {
						w.WriteHeader(401)
						fmt.Fprint(w, "{\"error\":\"unauthorized\"}")
						return
					}
					// Store auth context for handler access
					cWebAuthContext = result
					// Also store in headers for backward compat
					if rm, ok := result.(*CodongMap); ok {
						for k, v := range rm.Entries { r.Header.Set("X-Codong-Auth-"+k, toString(v)) }
					}
					prev.ServeHTTP(w, r)
					cWebAuthContext = nil
				})
			}
		}
	}
	server := &http.Server{Addr: addr, Handler: finalHandler}
	fmt.Fprintf(os.Stderr, "Codong server listening on http://localhost%s\n", addr)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()
	server.ListenAndServe()
	return nil
}

func writeResponse(w http.ResponseWriter, req *http.Request, result Value) {
	if m, ok := result.(*CodongMap); ok {
		rt := ""; if t, ok := m.Entries["_type"].(string); ok { rt = t }
		status := 200; if s, ok := m.Entries["status"].(float64); ok { status = int(s) }
		// Apply custom headers before status/body
		if hdrs, ok := m.Entries["headers"].(*CodongMap); ok {
			for k, v := range hdrs.Entries { w.Header().Set(k, toString(v)) }
		}
		switch rt {
		case "json":
			if w.Header().Get("Content-Type") == "" { w.Header().Set("Content-Type", "application/json") }
			w.WriteHeader(status)
			jb, _ := json.Marshal(valueToGo(m.Entries["data"]))
			w.Write(jb)
		case "text":
			if w.Header().Get("Content-Type") == "" { w.Header().Set("Content-Type", "text/plain") }
			w.WriteHeader(status)
			fmt.Fprint(w, toString(m.Entries["body"]))
		case "html":
			if w.Header().Get("Content-Type") == "" { w.Header().Set("Content-Type", "text/html") }
			w.WriteHeader(status)
			fmt.Fprint(w, toString(m.Entries["body"]))
		case "redirect":
			url := toString(m.Entries["url"])
			http.Redirect(w, req, url, status)
			return
		case "sse":
			if handler, ok := m.Entries["_handler"]; ok {
				writeSSEResponse(w, req, handler)
				return
			}
		default:
			if w.Header().Get("Content-Type") == "" { w.Header().Set("Content-Type", "application/json") }
			w.WriteHeader(200)
			jb, _ := json.Marshal(valueToGo(result))
			w.Write(jb)
		}
	} else if s, ok := result.(string); ok {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, s)
	} else {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, toString(result))
	}
}

func cWebJson(args ...Value) *CodongMap {
	var data Value
	status := float64(200)
	if len(args) > 0 { data = args[0] }
	if len(args) > 1 { status = toFloat(args[1]) }
	m := cMap("_type", "json", "data", data, "status", status)
	if len(args) > 2 {
		if h, ok := args[2].(*CodongMap); ok { cSet(m, "headers", h) }
	}
	return m
}
func cWebText(body Value) *CodongMap {
	return cMap("_type", "text", "body", body, "status", float64(200))
}
func cWebHtml(body Value) *CodongMap {
	return cMap("_type", "html", "body", body, "status", float64(200))
}
func cWebResponse(args ...Value) *CodongMap {
	var data Value; status := float64(200)
	if len(args) > 0 { data = args[0] }
	if len(args) > 1 { status = toFloat(args[1]) }
	m := cMap("_type", "json", "data", data, "status", status)
	if len(args) > 2 {
		if h, ok := args[2].(*CodongMap); ok { cSet(m, "headers", h) }
	}
	return m
}

// --- SSE (Server-Sent Events) ---

// CodongSSEStream provides send/close methods for SSE streaming
type CodongSSEStream struct {
	w       http.ResponseWriter
	flusher http.Flusher
	closed  bool
}

func cWebSSE(handler Value) *CodongMap {
	return cMap("_type", "sse", "_handler", handler)
}

func writeSSEResponse(w http.ResponseWriter, req *http.Request, handler Value) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)
	flusher.Flush()

	stream := &CodongSSEStream{w: w, flusher: flusher}

	// Create a stream object with send/close methods
	streamObj := cMap()
	streamMap := streamObj
	sendFn := func(args ...Value) Value {
		if stream.closed { return nil }
		event := "message"
		var data Value
		if len(args) > 0 { event = toString(args[0]) }
		if len(args) > 1 { data = args[1] }
		jb, _ := json.Marshal(valueToGo(data))
		fmt.Fprintf(stream.w, "event: %s\ndata: %s\n\n", event, string(jb))
		stream.flusher.Flush()
		return nil
	}
	closeFn := func(args ...Value) Value {
		stream.closed = true
		return nil
	}
	streamMap.Entries["send"] = CodongFn(sendFn)
	streamMap.Entries["close"] = CodongFn(closeFn)
	if _, ok := streamMap.Entries["send"]; !ok {
		streamMap.Order = append(streamMap.Order, "send")
	}
	if _, ok := streamMap.Entries["close"]; !ok {
		streamMap.Order = append(streamMap.Order, "close")
	}

	// Call the handler with the stream object
	if fn, ok := handler.(CodongFn); ok {
		fn(streamObj)
	}
}

// --- web.static() ---

var cWebStaticMiddlewares []Value

var cDefaultMIMETypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "application/javascript; charset=utf-8",
	".mjs":   "application/javascript; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".xml":   "application/xml; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
	".md":    "text/markdown; charset=utf-8",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".svg":   "image/svg+xml",
	".ico":   "image/x-icon",
	".webp":  "image/webp",
	".avif":  "image/avif",
	".mp4":   "video/mp4",
	".webm":  "video/webm",
	".mp3":   "audio/mpeg",
	".wav":   "audio/wav",
	".ogg":   "audio/ogg",
	".pdf":   "application/pdf",
	".zip":   "application/zip",
	".gz":    "application/gzip",
	".tar":   "application/x-tar",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".eot":   "application/vnd.ms-fontobject",
	".wasm":  "application/wasm",
	".map":   "application/json",
}

func cWebStatic(args ...Value) Value {
	if len(args) < 1 { return nil }
	rootDir := toString(args[0])
	m := cMap("_mw_type", "static", "root_dir", rootDir, "spa", false, "index", "index.html", "max_age", float64(0), "dotfiles", "deny", "etag", true)
	if len(args) > 1 {
		if opts, ok := args[1].(*CodongMap); ok {
			if v, exists := opts.Entries["spa"]; exists { cSet(m, "spa", v) }
			if v, exists := opts.Entries["index"]; exists { cSet(m, "index", v) }
			if v, exists := opts.Entries["max_age"]; exists { cSet(m, "max_age", v) }
			if v, exists := opts.Entries["dotfiles"]; exists { cSet(m, "dotfiles", v) }
			if v, exists := opts.Entries["etag"]; exists { cSet(m, "etag", v) }
		}
	}
	return m
}

func cWebApplyStatic(m *CodongMap, next http.Handler) http.Handler {
	rootDir := toString(m.Entries["root_dir"])
	prefix := toString(m.Entries["prefix"])
	if prefix == "null" { prefix = "" }
	spa := toBool(m.Entries["spa"])
	indexFile := toString(m.Entries["index"])
	if indexFile == "null" { indexFile = "index.html" }
	maxAge := int(toFloat(m.Entries["max_age"]))
	dotfiles := toString(m.Entries["dotfiles"])
	if dotfiles == "null" { dotfiles = "deny" }
	useETag := toBool(m.Entries["etag"])

	root := rootDir
	if !filepath.IsAbs(root) {
		wd, _ := os.Getwd()
		root = filepath.Join(wd, root)
	}
	root = filepath.Clean(root)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		urlPath := r.URL.Path
		if prefix != "" {
			if !strings.HasPrefix(urlPath, prefix) { next.ServeHTTP(w, r); return }
			urlPath = strings.TrimPrefix(urlPath, prefix)
			if urlPath == "" { urlPath = "/" }
		}
		fsPath := filepath.Clean(filepath.Join(root, filepath.FromSlash(urlPath)))
		if !strings.HasPrefix(fsPath, root) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if dotfiles != "allow" {
			base := filepath.Base(fsPath)
			if strings.HasPrefix(base, ".") && base != "." {
				if dotfiles == "deny" { http.Error(w, "forbidden", http.StatusForbidden) } else { next.ServeHTTP(w, r) }
				return
			}
		}
		info, err := os.Stat(fsPath)
		if err != nil {
			if os.IsNotExist(err) {
				// Try API routes first (next handler), then SPA fallback
				if spa {
					// Use a response recorder to check if next handler has a real response
					rec := &responseRecorder{ResponseWriter: w, statusCode: 0}
					next.ServeHTTP(rec, r)
					if rec.statusCode != 0 && rec.statusCode != 404 {
						return // API route handled it
					}
					// API returned 404 or didn't handle — serve SPA index
					indexPath := filepath.Join(root, indexFile)
					if ii, e := os.Stat(indexPath); e == nil && !ii.IsDir() {
						cServeStaticFile(w, r, indexPath, ii, maxAge, useETag)
						return
					}
				}
				next.ServeHTTP(w, r); return
			}
			http.Error(w, "internal server error", 500); return
		}
		if info.IsDir() {
			ip := filepath.Join(fsPath, indexFile)
			if ii, e := os.Stat(ip); e == nil && !ii.IsDir() { cServeStaticFile(w, r, ip, ii, maxAge, useETag); return }
			next.ServeHTTP(w, r); return
		}
		cServeStaticFile(w, r, fsPath, info, maxAge, useETag)
	})
}

// responseRecorder captures the status code to check if a handler handled the request
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	written    bool
}
func (rr *responseRecorder) WriteHeader(code int) {
	rr.statusCode = code
	if code != 404 { rr.ResponseWriter.WriteHeader(code) }
	rr.written = true
}
func (rr *responseRecorder) Write(b []byte) (int, error) {
	if !rr.written { rr.statusCode = 200; rr.written = true }
	if rr.statusCode == 404 { return len(b), nil } // swallow 404 body
	return rr.ResponseWriter.Write(b)
}

func cServeStaticFile(w http.ResponseWriter, r *http.Request, path string, info os.FileInfo, maxAge int, useETag bool) {
	ext := strings.ToLower(filepath.Ext(path))
	ct := "application/octet-stream"
	if mime, ok := cDefaultMIMETypes[ext]; ok { ct = mime }
	w.Header().Set("Content-Type", ct)
	if maxAge > 0 { w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", maxAge)) } else { w.Header().Set("Cache-Control", "no-cache") }
	if useETag {
		etag := fmt.Sprintf("%c%x-%x%c", '"', info.ModTime().UnixNano(), info.Size(), '"')
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag { w.WriteHeader(304); return }
	}
	// Serve file directly (avoid http.ServeFile which adds redirect logic)
	f, err := os.Open(path)
	if err != nil { http.Error(w, "internal server error", 500); return }
	defer f.Close()
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

// --- web.set_cookie / web.delete_cookie ---

func cWebSetCookie(args ...Value) Value {
	if len(args) < 2 { return nil }
	name := toString(args[0])
	value := toString(args[1])
	parts := []string{fmt.Sprintf("%s=%s", name, value)}
	path := "/"; httpOnly := true; secure := false; sameSite := "Lax"; cookieMaxAge := 0
	if len(args) > 2 {
		if opts, ok := args[2].(*CodongMap); ok {
			if v, exists := opts.Entries["path"]; exists { path = toString(v) }
			if v, exists := opts.Entries["domain"]; exists { d := toString(v); if d != "" && d != "null" { parts = append(parts, "Domain="+d) } }
			if v, exists := opts.Entries["max_age"]; exists { cookieMaxAge = int(toFloat(v)) }
			if v, exists := opts.Entries["http_only"]; exists { httpOnly = toBool(v) }
			if v, exists := opts.Entries["secure"]; exists { secure = toBool(v) }
			if v, exists := opts.Entries["same_site"]; exists { sameSite = toString(v) }
		}
	}
	parts = append(parts, "Path="+path)
	if cookieMaxAge > 0 { parts = append(parts, fmt.Sprintf("Max-Age=%d", cookieMaxAge)) }
	if httpOnly { parts = append(parts, "HttpOnly") }
	if secure { parts = append(parts, "Secure") }
	switch strings.ToLower(sameSite) {
	case "strict": parts = append(parts, "SameSite=Strict")
	case "none": parts = append(parts, "SameSite=None")
	default: parts = append(parts, "SameSite=Lax")
	}
	headerVal := strings.Join(parts, "; ")
	result := cMap()
	if len(args) > 3 {
		if existing, ok := args[3].(*CodongMap); ok {
			for _, k := range existing.Order { cSet(result, k, existing.Entries[k]) }
		}
	}
	cSet(result, "Set-Cookie", headerVal)
	return result
}

func cWebDeleteCookie(args ...Value) Value {
	if len(args) < 1 { return nil }
	name := toString(args[0])
	hdr := fmt.Sprintf("%s=; Path=/; Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:00 GMT", name)
	return cMap("Set-Cookie", hdr)
}

// --- Multipart parsing for request body ---

func cParseMultipartBody(req *http.Request, reqMap *CodongMap) {
	// Get multipart config from middleware
	var maxFileSize int64 = 10 * 1024 * 1024 // default 10MB
	var allowedTypes []string
	for _, mw := range cWebMiddlewares {
		if m, ok := mw.(*CodongMap); ok {
			if toString(m.Entries["_mw_type"]) == "multipart" {
				if mfs, ok := m.Entries["max_file_size"]; ok {
					maxFileSize = cParseSize(toString(mfs))
				}
				if at, ok := m.Entries["allowed_types"].(*CodongList); ok {
					for _, t := range at.Elements { allowedTypes = append(allowedTypes, toString(t)) }
				}
			}
		}
	}

	mr, err := req.MultipartReader()
	if err != nil { cSet(reqMap, "body", nil); cSet(reqMap, "files", cMap()); return }
	filesMap := cMap()
	bodyMap := cMap()
	// Track multiple files per field name
	filesListMap := make(map[string][]Value)
	var allFiles []Value
	var tmpFiles []string
	for {
		part, err := mr.NextPart()
		if err != nil { break }
		fieldName := part.FormName()
		filename := part.FileName()
		if filename == "" {
			data, _ := io.ReadAll(io.LimitReader(part, 1<<20))
			cSet(bodyMap, fieldName, string(data))
			part.Close()
			continue
		}
		tmpFile, err := os.CreateTemp("", "codong-upload-*")
		if err != nil { part.Close(); continue }
		written, _ := io.Copy(tmpFile, io.LimitReader(part, maxFileSize+1))
		tmpFile.Close()
		part.Close()
		if written > maxFileSize { os.Remove(tmpFile.Name()); continue }
		ct := part.Header.Get("Content-Type")
		if ct == "" { ct = "application/octet-stream" }
		// Check allowed types
		if len(allowedTypes) > 0 {
			allowed := false
			for _, t := range allowedTypes { if t == ct { allowed = true; break } }
			if !allowed { os.Remove(tmpFile.Name()); continue }
		}
		tmpFiles = append(tmpFiles, tmpFile.Name())
		ext := filepath.Ext(filename)
		fileObj := cMap("field_name", fieldName, "filename", filepath.Base(filename), "tmp_path", tmpFile.Name(), "size", float64(written), "content_type", ct, "mime", ct, "extension", ext)
		cSet(filesMap, fieldName, fileObj)
		filesListMap[fieldName] = append(filesListMap[fieldName], fileObj)
		allFiles = append(allFiles, fileObj)
	}
	// Add get() to files
	fsCopy := filesMap
	cSet(filesMap, "get", func(args ...Value) Value {
		if len(args) > 0 { if v, ok := fsCopy.Entries[toString(args[0])]; ok { return v } }
		return nil
	})
	if len(bodyMap.Order) > 0 { cSet(reqMap, "body", bodyMap) } else { cSet(reqMap, "body", nil) }
	cSet(reqMap, "files", filesMap)
	// files_list returns files for a specific field name
	cSet(reqMap, "files_list", func(args ...Value) Value {
		if len(args) > 0 {
			if files, ok := filesListMap[toString(args[0])]; ok {
				return &CodongList{Elements: files}
			}
		}
		return cList()
	})
	// files_all returns all uploaded files
	cSet(reqMap, "files_all", func(args ...Value) Value {
		return &CodongList{Elements: allFiles}
	})
}

func cParseSize(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasSuffix(s, "mb") { n, _ := strconv.ParseFloat(strings.TrimSuffix(s, "mb"), 64); return int64(n * 1024 * 1024) }
	if strings.HasSuffix(s, "kb") { n, _ := strconv.ParseFloat(strings.TrimSuffix(s, "kb"), 64); return int64(n * 1024) }
	if strings.HasSuffix(s, "b") { n, _ := strconv.ParseFloat(strings.TrimSuffix(s, "b"), 64); return int64(n) }
	n, _ := strconv.ParseFloat(s, 64)
	return int64(n)
}

// --- WebSocket support ---

const cWsMagicGUID = "258EAFA5-E914-47DA-95CA-5AB5DC11885E"
var cWsIDCounter int64
var cWsIDMu sync.Mutex
var cWsHub = struct{
	mu sync.RWMutex
	conns map[string]chan string
}{conns: make(map[string]chan string)}

func cNextWSID() string {
	cWsIDMu.Lock(); defer cWsIDMu.Unlock()
	cWsIDCounter++
	return fmt.Sprintf("ws-%d-%d", time.Now().UnixMilli(), cWsIDCounter)
}

var cWebWSRoutes []struct{ pattern string; handler func(...Value) Value }

func cWebWsRoute(pattern Value, handler Value) Value {
	p := toString(pattern)
	fn := handler.(func(...Value) Value)
	cWebWSRoutes = append(cWebWSRoutes, struct{ pattern string; handler func(...Value) Value }{p, fn})
	return nil
}

func cWebWSUpgradeHandler(handler func(...Value) Value) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.ToLower(r.Header.Get("Upgrade")) != "websocket" {
			http.Error(w, "expected websocket upgrade", 400); return
		}
		key := r.Header.Get("Sec-WebSocket-Key")
		if key == "" { http.Error(w, "missing Sec-WebSocket-Key", 400); return }
		h := sha1.New(); h.Write([]byte(key + cWsMagicGUID))
		acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))
		hj, ok := w.(http.Hijacker)
		if !ok { http.Error(w, "websocket not supported", 500); return }
		netConn, rw, err := hj.Hijack()
		if err != nil { http.Error(w, err.Error(), 500); return }
		resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + acceptKey + "\r\n\r\n"
		netConn.Write([]byte(resp))

		connID := cNextWSID()
		sendCh := make(chan string, 64)
		done := make(chan struct{})
		handlers := cMap()
		ctxMap := cMap()
		connObj := cMap("id", connID, "context", ctxMap)
		cSet(connObj, "send", func(args ...Value) Value {
			if len(args) > 0 { select { case sendCh <- toString(args[0]): default: } }
			return nil
		})
		cSet(connObj, "close", func(args ...Value) Value { select { case <-done: default: close(done) }; return nil })
		cSet(connObj, "on", func(args ...Value) Value {
			if len(args) >= 2 { cSet(handlers, toString(args[0]), args[1]) }; return nil
		})
		cSet(connObj, "query", func(args ...Value) Value {
			if len(args) > 0 { v := r.URL.Query().Get(toString(args[0])); if v != "" { return v } }; return nil
		})
		cSet(connObj, "header", func(args ...Value) Value {
			if len(args) > 0 { v := r.Header.Get(toString(args[0])); if v != "" { return v } }; return nil
		})
		cWsHub.mu.Lock(); cWsHub.conns[connID] = sendCh; cWsHub.mu.Unlock()
		handler(connObj)
		// Writer goroutine
		go func() {
			for { select { case msg := <-sendCh: cWsWriteTextFrame(netConn, msg); case <-done: return } }
		}()
		// Reader loop
		reader := bufio.NewReader(rw)
		for {
			msg, opcode, err := cWsReadFrame(reader)
			if err != nil { break }
			switch opcode {
			case 0x1:
				if fn, ok := handlers.Entries["message"]; ok { if f, ok := fn.(func(...Value) Value); ok { f(msg) } }
			case 0x8: cWsWriteFrame(netConn, 0x8, []byte{}); goto cleanup
			case 0x9: cWsWriteFrame(netConn, 0xA, []byte(msg))
			}
		}
	cleanup:
		if fn, ok := handlers.Entries["close"]; ok { if f, ok := fn.(func(...Value) Value); ok { f() } }
		cWsHub.mu.Lock(); delete(cWsHub.conns, connID); cWsHub.mu.Unlock()
		select { case <-done: default: close(done) }
		netConn.Close()
	}
}

func cWsReadFrame(reader *bufio.Reader) (string, byte, error) {
	b1, err := reader.ReadByte(); if err != nil { return "", 0, err }
	b2, err := reader.ReadByte(); if err != nil { return "", 0, err }
	opcode := b1 & 0x0F; masked := (b2 & 0x80) != 0; length := int64(b2 & 0x7F)
	if length == 126 { var lb [2]byte; io.ReadFull(reader, lb[:]); length = int64(binary.BigEndian.Uint16(lb[:])) }
	if length == 127 { var lb [8]byte; io.ReadFull(reader, lb[:]); length = int64(binary.BigEndian.Uint64(lb[:])) }
	var mask [4]byte
	if masked { io.ReadFull(reader, mask[:]) }
	payload := make([]byte, length)
	io.ReadFull(reader, payload)
	if masked { for i := range payload { payload[i] ^= mask[i%4] } }
	return string(payload), opcode, nil
}

func cWsWriteTextFrame(conn interface{ Write([]byte) (int, error) }, msg string) { cWsWriteFrame(conn, 0x1, []byte(msg)) }

func cWsWriteFrame(conn interface{ Write([]byte) (int, error) }, opcode byte, payload []byte) {
	frame := []byte{0x80 | opcode}
	l := len(payload)
	if l < 126 { frame = append(frame, byte(l))
	} else if l < 65536 { frame = append(frame, 126); lb := make([]byte, 2); binary.BigEndian.PutUint16(lb, uint16(l)); frame = append(frame, lb...)
	} else { frame = append(frame, 127); lb := make([]byte, 8); binary.BigEndian.PutUint64(lb, uint64(l)); frame = append(frame, lb...) }
	frame = append(frame, payload...)
	conn.Write(frame)
}

// --- DB Module ---

var cDB *sql.DB
var cDbTempFile string

func cDbConnect(dsn string) Value {
	// Strip SQLite URL prefix if present
	cleanDSN := dsn
	if strings.HasPrefix(dsn, "sqlite:///") { cleanDSN = dsn[len("sqlite:///"):]
	} else if strings.HasPrefix(dsn, "sqlite://") { cleanDSN = dsn[len("sqlite://"):] }
	// For in-memory databases, create a real temp file
	// modernc.org/sqlite + database/sql pool doesn't work with :memory:
	if cleanDSN == ":memory:" {
		f, err := os.CreateTemp("", "codong-mem-*.db")
		if err != nil { return cError("E2003", "cannot create temp db: " + err.Error()) }
		cleanDSN = f.Name()
		f.Close()
		// Register cleanup on exit
		cDbTempFile = cleanDSN
	}
	var err error
	cDB, err = sql.Open("sqlite", cleanDSN)
	if err != nil { return cError("E2003", "db connect failed: " + err.Error()) }
	cDB.SetMaxOpenConns(1)
	if err := cDB.Ping(); err != nil { return cError("E2003", "connection failed: " + err.Error()) }
	// WAL mode for file-based only
	if cleanDSN != "" && !strings.Contains(cleanDSN, "codong-mem") {
		cDB.Exec("PRAGMA journal_mode=WAL")
	}
	return cMap("_type", "db_connection", "status", "connected", "dsn", dsn)
}

func cDbPing() Value {
	if cDB == nil { return false }
	if err := cDB.Ping(); err != nil { return false }
	return true
}

func cDbStats() Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	stats := cDB.Stats()
	return cMap("open_connections", float64(stats.OpenConnections), "in_use", float64(stats.InUse), "idle", float64(stats.Idle), "total", float64(stats.OpenConnections), "max_open", float64(stats.MaxOpenConnections), "wait_count", float64(stats.WaitCount))
}

func cDbDisconnectRT() Value {
	if cDB != nil { cDB.Close(); cDB = nil }
	if cDbTempFile != "" { os.Remove(cDbTempFile); cDbTempFile = "" }
	return nil
}

func cDbDisconnect() {
	if cDB != nil { cDB.Close(); cDB = nil }
}

// cDbError classifies a database error and returns the appropriate CodongError.
func cDbError(err error) *CodongError {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed"):
		return cError("E2002_DUPLICATE_KEY", msg)
	case strings.Contains(msg, "syntax error") ||
		strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "no such column") ||
		strings.Contains(msg, "near \""):
		return cError("E2004_QUERY_FAILED", msg)
	default:
		return cError("E2003", msg)
	}
}

func cDbQuery(sqlStr string, params ...Value) Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	args := make([]interface{}, len(params))
	for i, p := range params { args[i] = valueToGo(p) }
	trimmed := strings.TrimSpace(strings.ToUpper(sqlStr))
	if strings.HasPrefix(trimmed, "SELECT") {
		rows, err := cDbQueryRows(sqlStr, args...)
		if err != nil { return cDbError(err) }
		defer rows.Close()
		return rowsToList(rows)
	}
	result, err := cDbExec(sqlStr, args...)
	if err != nil { return cDbError(err) }
	aff, _ := result.RowsAffected()
	lid, _ := result.LastInsertId()
	return cMap("affected", float64(aff), "id", float64(lid))
}

func cDbCount(table string, filterVal Value) Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	var filter *CodongMap
	if filterVal != nil { filter, _ = filterVal.(*CodongMap) }
	where, args := filterSQL(filter)
	q := "SELECT COUNT(*) FROM " + table
	if where != "" { q += " WHERE " + where }
	var count int64
	if err := cDbQueryRowOne(q, args...).Scan(&count); err != nil { return cDbError(err) }
	return float64(count)
}

func cDbExists(table string, filterVal Value) Value {
	count := cDbCount(table, filterVal)
	if f, ok := count.(float64); ok { return f > 0 }
	return false
}

func cDbInsert(table string, dataVal Value) Value {
	data := dataVal.(*CodongMap)
	var cols, phs []string
	var vals []interface{}
	for _, k := range data.Order {
		cols = append(cols, k)
		phs = append(phs, "?")
		vals = append(vals, valueToGo(data.Entries[k]))
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ","), strings.Join(phs, ","))
	r, err := cDbExec(q, vals...)
	if err != nil { return cDbError(err) }
	id, _ := r.LastInsertId()
	// Return the inserted row (include original data + id)
	result := cMap()
	cSet(result, "id", float64(id))
	for _, k := range data.Order {
		cSet(result, k, data.Entries[k])
	}
	return result
}

func cDbInsertBatch(table string, listVal Value) Value {
	list, ok := listVal.(*CodongList)
	if !ok { return cError("E2003", "insert_batch requires a list") }
	results := make([]Value, 0, len(list.Elements))
	for _, item := range list.Elements {
		r := cDbInsert(table, item)
		if _, ok := r.(*CodongError); ok { return r }
		results = append(results, r)
	}
	return &CodongList{Elements: results}
}

func cDbCreateIndex(table string, colsVal Value) Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	cols := toString(colsVal)
	name := "idx_" + table + "_" + strings.ReplaceAll(cols, ",", "_")
	q := fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", name, table, cols)
	_, err := cDbExec(q)
	if err != nil { return cDbError(err) }
	return true
}

func cDbFind(table string, filterVal Value) *CodongList {
	if cDB == nil { cPrint(cError("E2002", "no database connection")); return &CodongList{} }
	var filter *CodongMap
	if filterVal != nil { filter, _ = filterVal.(*CodongMap) }
	where, args := filterSQL(filter)
	q := "SELECT * FROM " + table
	if where != "" { q += " WHERE " + where }
	rows, err := cDbQueryRows(q, args...)
	if err != nil { cPrint(cDbError(err)); return &CodongList{} }
	defer rows.Close()
	return rowsToList(rows)
}

func cDbFindOpts(table string, filterVal Value, optsVal Value) Value {
	if cDB == nil { cPrint(cError("E2002", "no database connection")); return &CodongList{} }
	var filter *CodongMap
	if filterVal != nil { filter, _ = filterVal.(*CodongMap) }
	where, args := filterSQL(filter)
	q := "SELECT * FROM " + table
	if where != "" { q += " WHERE " + where }
	if opts, ok := optsVal.(*CodongMap); ok {
		if sortVal, ok := opts.Entries["sort"].(*CodongMap); ok {
			var sortClauses []string
			for _, k := range sortVal.Order {
				dir := "ASC"
				if toFloat(sortVal.Entries[k]) < 0 { dir = "DESC" }
				sortClauses = append(sortClauses, k+" "+dir)
			}
			if len(sortClauses) > 0 { q += " ORDER BY " + strings.Join(sortClauses, ", ") }
		}
		if lim, ok := opts.Entries["limit"]; ok {
			q += fmt.Sprintf(" LIMIT %d", int(toFloat(lim)))
		}
		if off, ok := opts.Entries["offset"]; ok {
			q += fmt.Sprintf(" OFFSET %d", int(toFloat(off)))
		}
	}
	rows, err := cDbQueryRows(q, args...)
	if err != nil { cPrint(cDbError(err)); return &CodongList{} }
	defer rows.Close()
	return rowsToList(rows)
}

func cDbFindOne(table string, filterVal Value) Value {
	var filter *CodongMap
	if filterVal != nil { filter, _ = filterVal.(*CodongMap) }
	where, args := filterSQL(filter)
	q := "SELECT * FROM " + table
	if where != "" { q += " WHERE " + where }
	q += " LIMIT 1"
	rows, err := cDbQueryRows(q, args...)
	if err != nil { return nil }
	defer rows.Close()
	list := rowsToList(rows)
	if len(list.Elements) == 0 { return nil }
	return list.Elements[0]
}

func cDbUpdate(table string, filterVal Value, dataVal Value) Value {
	filter := filterVal.(*CodongMap)
	data := dataVal.(*CodongMap)
	var setClauses []string; var setVals []interface{}
	for _, k := range data.Order {
		setClauses = append(setClauses, k+" = ?")
		setVals = append(setVals, valueToGo(data.Entries[k]))
	}
	where, wArgs := filterSQL(filter)
	allArgs := append(setVals, wArgs...)
	q := fmt.Sprintf("UPDATE %s SET %s", table, strings.Join(setClauses, ","))
	if where != "" { q += " WHERE " + where }
	r, err := cDbExec(q, allArgs...)
	if err != nil { return cDbError(err) }
	aff, _ := r.RowsAffected()
	return float64(aff)
}

func cDbDelete(table string, filterVal Value) Value {
	var filter *CodongMap
	if filterVal != nil { filter, _ = filterVal.(*CodongMap) }
	where, args := filterSQL(filter)
	q := "DELETE FROM " + table
	if where != "" { q += " WHERE " + where }
	r, err := cDbExec(q, args...)
	if err != nil { return cDbError(err) }
	aff, _ := r.RowsAffected()
	return float64(aff)
}

func cDbCreateTable(table string, schema Value) Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	m, ok := schema.(*CodongMap)
	if !ok { return cError("E2003", "schema must be a map") }
	var cols []string
	for _, k := range m.Order {
		colType := toString(m.Entries[k])
		// If the value contains SQL keywords like "primary key", "unique", etc.,
		// pass through as raw SQL column definition
		lower := strings.ToLower(colType)
		if strings.Contains(lower, "primary key") || strings.Contains(lower, "unique") ||
			strings.Contains(lower, "not null") || strings.Contains(lower, "default") ||
			strings.Contains(lower, "autoincrement") || strings.Contains(lower, "references") {
			cols = append(cols, k+" "+colType)
		} else {
			sqlType := "TEXT"
			switch lower {
			case "number", "int", "integer": sqlType = "INTEGER"
			case "float", "real": sqlType = "REAL"
			case "bool", "boolean": sqlType = "INTEGER"
			case "text", "string": sqlType = "TEXT"
			}
			if strings.ToLower(k) == "id" {
				cols = append(cols, k+" INTEGER PRIMARY KEY AUTOINCREMENT")
			} else {
				cols = append(cols, k+" "+sqlType)
			}
		}
	}
	q := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", table, strings.Join(cols, ", "))
	_, err := cDbExec(q)
	if err != nil { return cDbError(err) }
	return true
}

func cDbUpsert(table string, filterVal Value, dataVal Value) Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	// Try update first
	filter := filterVal.(*CodongMap)
	data := dataVal.(*CodongMap)
	where, wArgs := filterSQL(filter)
	if where != "" {
		var count int64
		cDbQueryRowOne("SELECT COUNT(*) FROM "+table+" WHERE "+where, wArgs...).Scan(&count)
		if count > 0 {
			return cDbUpdate(table, filterVal, dataVal)
		}
	}
	// Merge filter and data for insert
	merged := cMap()
	for _, k := range filter.Order { cSet(merged, k, filter.Entries[k]) }
	for _, k := range data.Order { cSet(merged, k, data.Entries[k]) }
	return cDbInsert(table, merged)
}

func cDbQueryOne(sqlStr string, params ...Value) Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	args := make([]interface{}, len(params))
	for i, p := range params { args[i] = valueToGo(p) }
	rows, err := cDbQueryRows(sqlStr+" LIMIT 1", args...)
	if err != nil { return cDbError(err) }
	defer rows.Close()
	list := rowsToList(rows)
	if len(list.Elements) == 0 { return nil }
	return list.Elements[0]
}

var cDbTx *sql.Tx // active transaction, if any

func cDbTransaction(fn Value) Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	tx, err := cDB.Begin()
	if err != nil { return cDbError(err) }
	cDbTx = tx
	// Create a tx object with methods that work within the transaction
	txObj := cMap("_type", "db_tx")
	cSet(txObj, "update", func(args ...Value) Value {
		return cDbUpdate(toString(args[0]), args[1], args[2])
	})
	cSet(txObj, "insert", func(args ...Value) Value {
		return cDbInsert(toString(args[0]), args[1])
	})
	cSet(txObj, "delete", func(args ...Value) Value {
		return cDbDelete(toString(args[0]), args[1])
	})
	cSet(txObj, "query", func(args ...Value) Value {
		if len(args) > 1 {
			return cDbQuery(toString(args[0]), toList(args[1]).Elements...)
		}
		return cDbQuery(toString(args[0]))
	})
	// Call fn with tx object, and also catch panics from ? operator
	var result Value
	func() {
		defer func() {
			if r := recover(); r != nil {
				if rs, ok := r.(*cReturnSignal); ok {
					if ce, ok := rs.Value.(*CodongError); ok {
						result = ce
						return
					}
				}
				if ce, ok := r.(*CodongError); ok {
					result = ce
					return
				}
				panic(r) // re-panic non-error panics
			}
		}()
		result = cCallFn(fn, txObj)
	}()
	cDbTx = nil
	if e, ok := result.(*CodongError); ok {
		tx.Rollback()
		// Re-panic so try/catch can handle it
		panic(&cReturnSignal{Value: e})
	}
	if err := tx.Commit(); err != nil {
		return cError("E2003", "commit failed: " + err.Error())
	}
	return result
}

// cDbExec executes SQL using the transaction if active, otherwise the main connection.
func cDbExec(q string, args ...interface{}) (sql.Result, error) {
	if cDbTx != nil { return cDbTx.Exec(q, args...) }
	return cDB.Exec(q, args...)
}

func cDbQueryRows(q string, args ...interface{}) (*sql.Rows, error) {
	if cDbTx != nil { return cDbTx.Query(q, args...) }
	return cDB.Query(q, args...)
}

func cDbQueryRowOne(q string, args ...interface{}) *sql.Row {
	if cDbTx != nil { return cDbTx.QueryRow(q, args...) }
	return cDB.QueryRow(q, args...)
}

func cDbSort(table string, filterVal Value, order string) Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	var filter *CodongMap
	if filterVal != nil { filter, _ = filterVal.(*CodongMap) }
	// filterVal might be the sort field name as string
	sortField := ""
	if s, ok := filterVal.(string); ok {
		sortField = s
		filter = nil
	}
	where, args := filterSQL(filter)
	q := "SELECT * FROM " + table
	if where != "" { q += " WHERE " + where }
	if sortField != "" {
		dir := "ASC"
		if strings.ToLower(order) == "desc" { dir = "DESC" }
		q += " ORDER BY " + sortField + " " + dir
	}
	rows, err := cDbQueryRows(q, args...)
	if err != nil { return cDbError(err) }
	defer rows.Close()
	return rowsToList(rows)
}

func filterSQL(filter *CodongMap) (string, []interface{}) {
	if filter == nil || len(filter.Entries) == 0 { return "", nil }
	var clauses []string; var args []interface{}
	for _, k := range filter.Order {
		v := filter.Entries[k]
		// Handle $and operator
		if k == "$and" {
			if andList, ok := v.(*CodongList); ok {
				for _, item := range andList.Elements {
					if m, ok := item.(*CodongMap); ok {
						subWhere, subArgs := filterSQL(m)
						if subWhere != "" {
							clauses = append(clauses, "("+subWhere+")")
							args = append(args, subArgs...)
						}
					}
				}
			}
			continue
		}
		// Handle operator maps like {$gt: 5, $lt: 10}
		if opMap, ok := v.(*CodongMap); ok {
			for _, op := range opMap.Order {
				val := opMap.Entries[op]
				switch op {
				case "$gt":
					clauses = append(clauses, k+" > ?")
					args = append(args, valueToGo(val))
				case "$gte":
					clauses = append(clauses, k+" >= ?")
					args = append(args, valueToGo(val))
				case "$lt":
					clauses = append(clauses, k+" < ?")
					args = append(args, valueToGo(val))
				case "$lte":
					clauses = append(clauses, k+" <= ?")
					args = append(args, valueToGo(val))
				case "$ne":
					clauses = append(clauses, k+" != ?")
					args = append(args, valueToGo(val))
				case "$like":
					clauses = append(clauses, k+" LIKE ?")
					args = append(args, valueToGo(val))
				case "$in":
					if list, ok := val.(*CodongList); ok {
						phs := make([]string, len(list.Elements))
						for i, el := range list.Elements {
							phs[i] = "?"
							args = append(args, valueToGo(el))
						}
						clauses = append(clauses, k+" IN ("+strings.Join(phs, ",")+")")
					}
				}
			}
			continue
		}
		clauses = append(clauses, k+" = ?")
		args = append(args, valueToGo(v))
	}
	return strings.Join(clauses, " AND "), args
}

func rowsToList(rows *sql.Rows) *CodongList {
	cols, _ := rows.Columns()
	var results []Value
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals { ptrs[i] = &vals[i] }
		rows.Scan(ptrs...)
		m := cMap()
		for i, col := range cols { cSet(m, col, goToValue(vals[i])) }
		results = append(results, m)
	}
	if results == nil { results = []Value{} }
	return &CodongList{Elements: results}
}

// --- HTTP Client ---

func cHttpGet(url string, opts ...Value) *CodongMap {
	return cHttpDo("GET", url, nil, opts...)
}
func cHttpPost(url string, body Value, opts ...Value) *CodongMap {
	return cHttpDo("POST", url, body, opts...)
}
func cHttpDo(method, url string, body Value, opts ...Value) *CodongMap {
	var bodyReader io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			bodyReader = strings.NewReader(s)
		} else {
			jb, _ := json.Marshal(valueToGo(body))
			bodyReader = bytes.NewReader(jb)
		}
	}
	req, reqErr := http.NewRequest(method, url, bodyReader)
	if reqErr != nil {
		e := cError("E3005_CONN_FAILED", "connection failed: " + reqErr.Error())
		return cMap("status", float64(0), "ok", false, "body", e.Error(), "error", e)
	}
	req.Header.Set("User-Agent", "Codong/0.1")
	if body != nil { req.Header.Set("Content-Type", "application/json") }
	// Apply custom headers and params from opts
	for _, opt := range opts {
		if m, ok := opt.(*CodongMap); ok {
			if h, ok := m.Entries["headers"].(*CodongMap); ok {
				for k, v := range h.Entries {
					req.Header.Set(k, toString(v))
				}
			}
			if p, ok := m.Entries["params"].(*CodongMap); ok {
				q := req.URL.Query()
				for _, k := range p.Order {
					q.Set(k, toString(p.Entries[k]))
				}
				req.URL.RawQuery = q.Encode()
			}
		}
	}
	// Apply timeout
	timeout := 30 * time.Second
	for _, opt := range opts {
		if m, ok := opt.(*CodongMap); ok {
			if t, ok := m.Entries["timeout"]; ok {
				ds := toString(t)
				if d, err := time.ParseDuration(ds); err == nil { timeout = d }
			}
		}
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") || strings.Contains(errStr, "context deadline") {
			e := cError("E3001_TIMEOUT", "request timed out: " + errStr, "retry", true)
			return cMap("status", float64(0), "ok", false, "body", errStr, "error", e)
		}
		e := cError("E3005_CONN_FAILED", "connection failed: " + errStr)
		return cMap("status", float64(0), "ok", false, "body", errStr, "error", e)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	rawBody := string(respBody)
	// Build headers map
	hm := cMap()
	for k, v := range resp.Header {
		cSet(hm, strings.ToLower(k), v[0])
	}
	// Check for HTTP error status codes
	var httpErr *CodongError
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		httpErr = cError("E3003_HTTP_4XX", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
	} else if resp.StatusCode >= 500 {
		httpErr = cError("E3004_HTTP_5XX", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
	}
	// Build response with callable json() and text()
	m := cMap(
		"status", float64(resp.StatusCode),
		"ok", resp.StatusCode >= 200 && resp.StatusCode < 300,
		"body", rawBody,
		"headers", hm,
	)
	if httpErr != nil {
		cSet(m, "error", httpErr)
	}
	cSet(m, "json", func(args ...Value) Value {
		var data interface{}
		if json.Unmarshal([]byte(rawBody), &data) != nil { return nil }
		return goToValue(data)
	})
	cSet(m, "text", func(args ...Value) Value {
		return rawBody
	})
	return m
}

func cHttpRequest(optsVal Value) *CodongMap {
	m, ok := optsVal.(*CodongMap)
	if !ok { return cMap("status", float64(0), "ok", false, "body", "invalid options") }
	method := "GET"
	url := ""
	var body Value
	if v, ok := m.Entries["method"].(string); ok { method = strings.ToUpper(v) }
	if v, ok := m.Entries["url"].(string); ok { url = v }
	if v, ok := m.Entries["body"]; ok { body = v }
	opts := []Value{}
	// Pass headers/params/timeout as opts map
	optsMap := cMap()
	if h, ok := m.Entries["headers"]; ok { cSet(optsMap, "headers", h) }
	if p, ok := m.Entries["params"]; ok { cSet(optsMap, "params", p) }
	if t, ok := m.Entries["timeout"]; ok { cSet(optsMap, "timeout", t) }
	if len(optsMap.Entries) > 0 { opts = append(opts, optsMap) }
	return cHttpDo(method, url, body, opts...)
}

// --- HTTP Response Methods (in cCall for CodongMap) ---
// These are handled in cCall when the map has a "_http" marker

func init() {
	// Patch cCall to handle http response methods
}

// --- Value Conversion ---

func valueToGo(v Value) interface{} {
	switch o := v.(type) {
	case *CodongList:
		r := make([]interface{}, len(o.Elements))
		for i, el := range o.Elements { r[i] = valueToGo(el) }
		return r
	case *CodongMap:
		r := map[string]interface{}{}
		for k, val := range o.Entries { r[k] = valueToGo(val) }
		return r
	}
	return v
}

func goToValue(v interface{}) Value {
	if v == nil { return nil }
	switch val := v.(type) {
	case float64: return val
	case int64: return float64(val)
	case int: return float64(val)
	case string: return val
	case bool: return val
	case []byte: return string(val)
	case []interface{}:
		elems := make([]Value, len(val))
		for i, el := range val { elems[i] = goToValue(el) }
		return &CodongList{Elements: elems}
	case map[string]interface{}:
		m := cMap()
		for k, v := range val { cSet(m, k, goToValue(v)) }
		return m
	}
	return fmt.Sprintf("%v", v)
}

// --- Error Extended API ---

func cErrorToJson(err Value) Value {
	e, ok := err.(*CodongError)
	if !ok { return nil }
	data := map[string]interface{}{
		"code": e.Code, "message": e.Message, "fix": e.Fix,
		"retry": e.Retry, "docs": e.Docs, "source": e.Source,
	}
	if e.Context != nil {
		data["context"] = valueToGo(e.Context)
	}
	jb, _ := json.Marshal(data)
	return string(jb)
}

func cErrorToCompact(err Value) Value {
	e, ok := err.(*CodongError)
	if !ok { return nil }
	return fmt.Sprintf("err_code:%s|src:%s|msg:%s|fix:%s|retry:%v", e.Code, e.Source, e.Message, e.Fix, e.Retry)
}

func cErrorFromJson(jsonStr Value) Value {
	s := toString(jsonStr)
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(s), &data); err != nil {
		return cError("E1001", "invalid JSON: " + err.Error())
	}
	code := ""; if c, ok := data["code"].(string); ok { code = c }
	msg := ""; if m, ok := data["message"].(string); ok { msg = m }
	e := cError(code, msg)
	if f, ok := data["fix"].(string); ok { e.Fix = f }
	if r, ok := data["retry"].(bool); ok { e.Retry = r }
	if d, ok := data["docs"].(string); ok { e.Docs = d }
	if src, ok := data["source"].(string); ok { e.Source = src }
	return e
}

func cErrorFromCompact(compactStr Value) Value {
	s := toString(compactStr)
	e := &CodongError{Source: "codong", IsError: true}
	parts := strings.Split(s, "|")
	for _, part := range parts {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 { continue }
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch key {
		case "err_code": e.Code = val
		case "src": e.Source = val
		case "msg": e.Message = val
		case "fix": e.Fix = val
		case "retry": e.Retry = val == "true"
		}
	}
	if e.Docs == "" && e.Code != "" {
		e.Docs = "https://codong.org/errors/" + e.Code
	}
	return e
}

func cErrorSetFormat(f Value) Value {
	// no-op in compiled mode
	return nil
}

func cErrorHandle(err Value, handlers Value) Value {
	e, ok := err.(*CodongError)
	if !ok { return err }
	hm, ok := handlers.(*CodongMap)
	if !ok { return err }
	if fn, ok := hm.Entries[e.Code]; ok {
		return cCallFn(fn, err)
	}
	if fn, ok := hm.Entries["_"]; ok {
		return cCallFn(fn, err)
	}
	return err
}

func cErrorRetry(fn Value, opts Value) Value {
	max := 3
	var delayDur time.Duration
	if m, ok := opts.(*CodongMap); ok {
		if v, ok := m.Entries["max"]; ok { max = int(toFloat(v)) }
		if v, ok := m.Entries["delay"]; ok {
			ds := toString(v)
			if d, err := time.ParseDuration(ds); err == nil { delayDur = d }
		}
	} else {
		max = int(toFloat(opts))
	}
	var lastErr Value
	for i := 0; i < max; i++ {
		result := cCallFn(fn)
		if e, ok := result.(*CodongError); ok {
			if !e.Retry {
				return result // non-retryable error, stop immediately
			}
			lastErr = result
			if delayDur > 0 && i < max-1 { time.Sleep(delayDur) }
			continue
		}
		return result
	}
	if lastErr != nil { return lastErr }
	return nil
}

// --- LLM Module ---

func cLlmAsk(args ...Value) Value {
	// Minimal LLM caller — uses OpenAI-compatible API
	prompt := ""; model := "gpt-4o"; apiKey := os.Getenv("OPENAI_API_KEY")
	for _, a := range args {
		if s, ok := a.(string); ok && prompt == "" { prompt = s }
		if m, ok := a.(*CodongMap); ok {
			if v, ok := m.Entries["model"].(string); ok { model = v }
			if v, ok := m.Entries["api_key"].(string); ok { apiKey = v }
			if v, ok := m.Entries["prompt"].(string); ok { prompt = v }
		}
	}
	if apiKey == "" {
		if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" { apiKey = k; model = "claude-3-5-sonnet-20241022" }
	}
	if apiKey == "" { return cError("E4005", "no API key", "fix", "export OPENAI_API_KEY") }
	if prompt == "" { return cError("E1005", "no prompt provided") }

	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0.7,
		"max_tokens": 4096,
	}
	jb, _ := json.Marshal(body)
	url := "https://api.openai.com/v1/chat/completions"
	if strings.HasPrefix(model, "claude") {
		url = "https://api.anthropic.com/v1/messages"
		body = map[string]interface{}{
			"model": model, "max_tokens": 4096,
			"messages": []map[string]string{{"role": "user", "content": prompt}},
		}
		jb, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest("POST", url, bytes.NewReader(jb))
	req.Header.Set("Content-Type", "application/json")
	if strings.HasPrefix(model, "claude") {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return cError("E4001", err.Error()) }
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 { return cError("E4001", string(respBody)) }
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	// OpenAI format
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if c, ok := choices[0].(map[string]interface{}); ok {
			if m, ok := c["message"].(map[string]interface{}); ok {
				if t, ok := m["content"].(string); ok { return t }
			}
		}
	}
	// Anthropic format
	if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
		if b, ok := content[0].(map[string]interface{}); ok {
			if t, ok := b["text"].(string); ok { return t }
		}
	}
	return string(respBody)
}

func cLlmCountTokens(text string) Value {
	return float64(len(text)) / 4.0
}

// --- Dynamic Function Call Helper ---

var cCallDepth int

func cCallFn(fn Value, args ...Value) Value {
	if f, ok := fn.(func(...Value) Value); ok {
		cCallDepth++
		if cCallDepth > 10000 {
			cCallDepth = 0
			panic(cError("E9002_STACK_OVERFLOW", "maximum call stack exceeded"))
		}
		result := f(args...)
		cCallDepth--
		return result
	}
	// Try to call as a CodongMap with callable entries (for req.param("id") pattern)
	if m, ok := fn.(*CodongMap); ok && len(args) > 0 {
		key := toString(args[0])
		if v, exists := m.Entries[key]; exists { return v }
	}
	// Non-function call — panic with E1004
	if fn != nil {
		panic(cError("E1004_UNDEFINED_FUNC", fmt.Sprintf("attempted to call a non-function value of type %s", typeOf(fn))))
	}
	return nil
}

// ============================================================
// fs module runtime functions
// ============================================================

var cFsWorkDir string

func init() {
	cFsWorkDir, _ = os.Getwd()
}

func cFsResolve(path string) string {
	p := toString(path)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(cFsWorkDir, p))
}

func cFsRead(args ...Value) Value {
	if len(args) < 1 { return cError("E5001_FILE_NOT_FOUND", "fs.read requires a path argument", "fix", "fs.read(\"./file.txt\")") }
	p := cFsResolve(toString(args[0]))
	info, statErr := os.Stat(p)
	if statErr == nil && info.IsDir() {
		return cError("E5004_IS_DIRECTORY", fmt.Sprintf("path is a directory: %s", toString(args[0])), "fix", fmt.Sprintf("use fs.list(\"%s\") to read directory contents", toString(args[0])))
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cError("E5001_FILE_NOT_FOUND", fmt.Sprintf("file not found: %s", toString(args[0])), "fix", fmt.Sprintf("check path: %s", p))
		}
		if os.IsPermission(err) {
			return cError("E5002_PERMISSION_DENIED", fmt.Sprintf("permission denied: %s", toString(args[0])), "fix", "check file permissions")
		}
		return cError("E5008_IO_ERROR", err.Error(), "fix", "check disk space")
	}
	return string(data)
}

func cFsWrite(args ...Value) Value {
	if len(args) < 2 { return cError("E5008_IO_ERROR", "fs.write requires path and content", "fix", "fs.write(path, content)") }
	p := cFsResolve(toString(args[0]))
	os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, []byte(toString(args[1])), 0644); err != nil {
		if os.IsPermission(err) {
			return cError("E5002_PERMISSION_DENIED", err.Error(), "fix", "check file permissions")
		}
		return cError("E5008_IO_ERROR", err.Error(), "fix", "check disk space")
	}
	return true
}

func cFsAppend(args ...Value) Value {
	if len(args) < 2 { return cError("E5008_IO_ERROR", "fs.append requires path and content", "fix", "fs.append(path, content)") }
	p := cFsResolve(toString(args[0]))
	os.MkdirAll(filepath.Dir(p), 0755)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return cError("E5008_IO_ERROR", err.Error(), "fix", "check permissions")
	}
	defer f.Close()
	f.WriteString(toString(args[1]))
	return true
}

func cFsExists(args ...Value) Value {
	if len(args) < 1 { return false }
	p := cFsResolve(toString(args[0]))
	_, err := os.Stat(p)
	return err == nil
}

func cFsDelete(args ...Value) Value {
	if len(args) < 1 { return cError("E5008_IO_ERROR", "fs.delete requires a path", "fix", "fs.delete(path)") }
	p := cFsResolve(toString(args[0]))
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			return true // idempotent delete
		}
		return cError("E5008_IO_ERROR", err.Error(), "fix", "check permissions")
	}
	return true
}

func cFsCopy(args ...Value) Value {
	if len(args) < 2 { return cError("E5008_IO_ERROR", "fs.copy requires src and dst", "fix", "fs.copy(src, dst)") }
	src := cFsResolve(toString(args[0]))
	dst := cFsResolve(toString(args[1]))
	srcFile, err := os.Open(src)
	if err != nil {
		return cError("E5001_FILE_NOT_FOUND", fmt.Sprintf("source not found: %s", toString(args[0])), "fix", "check source path")
	}
	defer srcFile.Close()
	os.MkdirAll(filepath.Dir(dst), 0755)
	dstFile, err := os.Create(dst)
	if err != nil {
		return cError("E5008_IO_ERROR", err.Error(), "fix", "check permissions")
	}
	defer dstFile.Close()
	io.Copy(dstFile, srcFile)
	return true
}

func cFsMove(args ...Value) Value {
	if len(args) < 2 { return cError("E5008_IO_ERROR", "fs.move requires src and dst", "fix", "fs.move(src, dst)") }
	src := cFsResolve(toString(args[0]))
	dst := cFsResolve(toString(args[1]))
	os.MkdirAll(filepath.Dir(dst), 0755)
	if err := os.Rename(src, dst); err != nil {
		return cError("E5008_IO_ERROR", err.Error(), "fix", "check paths")
	}
	return true
}

func cFsList(args ...Value) Value {
	if len(args) < 1 { return cError("E5008_IO_ERROR", "fs.list requires a directory path", "fix", "fs.list(dir)") }
	p := cFsResolve(toString(args[0]))
	entries, err := os.ReadDir(p)
	if err != nil {
		return cError("E5001_FILE_NOT_FOUND", fmt.Sprintf("directory not found: %s", toString(args[0])), "fix", "check directory path")
	}
	result := make([]Value, 0, len(entries))
	for _, entry := range entries {
		info, _ := entry.Info()
		entryType := "file"
		if entry.IsDir() { entryType = "dir" }
		var size int64
		var modified string
		if info != nil {
			size = info.Size()
			modified = info.ModTime().UTC().Format(time.RFC3339)
		}
		result = append(result, cMap("name", entry.Name(), "path", filepath.ToSlash(filepath.Join(p, entry.Name())), "type", entryType, "size", float64(size), "modified", modified))
	}
	return &CodongList{Elements: result}
}

func cFsMkdir(args ...Value) Value {
	if len(args) < 1 { return cError("E5008_IO_ERROR", "fs.mkdir requires a path", "fix", "fs.mkdir(path)") }
	p := cFsResolve(toString(args[0]))
	if err := os.MkdirAll(p, 0755); err != nil {
		return cError("E5008_IO_ERROR", err.Error(), "fix", "check permissions")
	}
	return true
}

func cFsRmdir(args ...Value) Value {
	if len(args) < 1 { return cError("E5008_IO_ERROR", "fs.rmdir requires a path", "fix", "fs.rmdir(path)") }
	p := cFsResolve(toString(args[0]))
	recursive := false
	if len(args) >= 2 { recursive = toBool(args[1]) }
	if recursive {
		os.RemoveAll(p)
	} else {
		if err := os.Remove(p); err != nil {
			return cError("E5006_DIR_NOT_EMPTY", fmt.Sprintf("directory not empty: %s", toString(args[0])), "fix", "use fs.rmdir(path, true) to delete recursively")
		}
	}
	return true
}

func cFsStat(args ...Value) Value {
	if len(args) < 1 { return nil }
	p := cFsResolve(toString(args[0]))
	info, err := os.Stat(p)
	if err != nil { return nil }
	entryType := "file"
	if info.IsDir() { entryType = "dir" }
	return cMap("name", info.Name(), "path", filepath.ToSlash(p), "type", entryType, "size", float64(info.Size()),
		"modified", info.ModTime().UTC().Format(time.RFC3339), "created", info.ModTime().UTC().Format(time.RFC3339),
		"extension", filepath.Ext(info.Name()))
}

func cFsReadJson(args ...Value) Value {
	if len(args) < 1 { return cError("E5001_FILE_NOT_FOUND", "fs.read_json requires a path", "fix", "fs.read_json(path)") }
	p := cFsResolve(toString(args[0]))
	data, err := os.ReadFile(p)
	if err != nil {
		return cError("E5001_FILE_NOT_FOUND", fmt.Sprintf("file not found: %s", toString(args[0])), "fix", "check path")
	}
	var result interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return cError("E6001_PARSE_ERROR", fmt.Sprintf("JSON parse error: %s", err.Error()), "fix", "check JSON syntax, use json.valid() first")
	}
	return goToCodong(result)
}

func cFsWriteJson(args ...Value) Value {
	if len(args) < 2 { return cError("E5008_IO_ERROR", "fs.write_json requires path and data", "fix", "fs.write_json(path, data)") }
	p := cFsResolve(toString(args[0]))
	goVal := codongToGo(args[1])
	jsonData, err := json.MarshalIndent(goVal, "", "  ")
	if err != nil {
		return cError("E6002_STRINGIFY_ERROR", fmt.Sprintf("JSON stringify error: %s", err.Error()), "fix", "remove circular references")
	}
	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, append(jsonData, '\n'), 0644)
	return true
}

func cFsReadLines(args ...Value) Value {
	if len(args) < 1 { return cError("E5001_FILE_NOT_FOUND", "fs.read_lines requires a path", "fix", "fs.read_lines(path)") }
	p := cFsResolve(toString(args[0]))
	data, err := os.ReadFile(p)
	if err != nil {
		return cError("E5001_FILE_NOT_FOUND", fmt.Sprintf("file not found: %s", toString(args[0])), "fix", "check path")
	}
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	result := make([]Value, len(lines))
	for i, line := range lines { result[i] = line }
	return &CodongList{Elements: result}
}

func cFsWriteLines(args ...Value) Value {
	if len(args) < 2 { return cError("E5008_IO_ERROR", "fs.write_lines requires path and lines", "fix", "fs.write_lines(path, lines)") }
	p := cFsResolve(toString(args[0]))
	list := toList(args[1])
	var sb strings.Builder
	for _, el := range list.Elements {
		sb.WriteString(toString(el))
		sb.WriteString("\n")
	}
	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, []byte(sb.String()), 0644)
	return true
}

func cFsJoin(args ...Value) Value {
	parts := make([]string, len(args))
	for i, a := range args { parts[i] = toString(a) }
	return filepath.ToSlash(filepath.Join(parts...))
}

func cFsCwd(args ...Value) Value {
	return filepath.ToSlash(cFsWorkDir)
}

func cFsBasename(args ...Value) Value {
	if len(args) < 1 { return "" }
	return filepath.Base(toString(args[0]))
}

func cFsDirname(args ...Value) Value {
	if len(args) < 1 { return "" }
	return filepath.ToSlash(filepath.Dir(toString(args[0])))
}

func cFsExtension(args ...Value) Value {
	if len(args) < 1 { return "" }
	return filepath.Ext(toString(args[0]))
}

func cFsSafeJoin(args ...Value) Value {
	if len(args) < 2 { return nil }
	base := cFsResolve(toString(args[0]))
	userInput := toString(args[1])
	// Null byte check
	if strings.ContainsRune(userInput, 0) || strings.Contains(userInput, "\\x00") || strings.Contains(userInput, "\x00") { return nil }
	// Reject absolute paths
	if filepath.IsAbs(userInput) || strings.HasPrefix(userInput, "/") { return nil }
	// Reject backslash paths
	if strings.Contains(userInput, "\\") { return nil }
	// URL decode (loop for double-encoding)
	decoded := userInput
	for i := 0; i < 3; i++ {
		d, err := url.PathUnescape(decoded)
		if err != nil || d == decoded { break }
		decoded = d
	}
	if strings.Contains(decoded, "..") { return nil }
	if filepath.IsAbs(decoded) || strings.HasPrefix(decoded, "/") { return nil }
	joined := filepath.Clean(filepath.Join(base, decoded))
	if !strings.HasPrefix(joined+string(filepath.Separator), base+string(filepath.Separator)) { return nil }
	return filepath.ToSlash(joined)
}

func cFsTempFile(args ...Value) Value {
	f, err := os.CreateTemp("", "codong-*.tmp")
	if err != nil {
		return cError("E5008_IO_ERROR", err.Error(), "fix", "check temp directory permissions")
	}
	tmpPath := filepath.ToSlash(f.Name())
	f.Close()
	deleteFn := func(args ...Value) Value { os.Remove(tmpPath); return nil }
	return cMap("path", tmpPath, "delete", CodongFn(deleteFn))
}

func cFsTempDir(args ...Value) Value {
	dir, err := os.MkdirTemp("", "codong-*")
	if err != nil {
		return cError("E5008_IO_ERROR", err.Error(), "fix", "check temp directory permissions")
	}
	tmpPath := filepath.ToSlash(dir)
	deleteFn := func(args ...Value) Value { os.RemoveAll(tmpPath); return nil }
	return cMap("path", tmpPath, "delete", CodongFn(deleteFn))
}

// goToCodong converts a Go value (from JSON unmarshal) to Codong runtime types.
func goToCodong(v interface{}) Value {
	if v == nil { return nil }
	switch val := v.(type) {
	case map[string]interface{}:
		m := &CodongMap{Entries: map[string]Value{}, Order: []string{}}
		for k, vv := range val {
			m.Entries[k] = goToCodong(vv)
			m.Order = append(m.Order, k)
		}
		return m
	case []interface{}:
		elems := make([]Value, len(val))
		for i, vv := range val { elems[i] = goToCodong(vv) }
		return &CodongList{Elements: elems}
	case float64:
		return val
	case string:
		return val
	case bool:
		return val
	default:
		return fmt.Sprintf("%v", val)
	}
}

// codongToGo converts a Codong runtime value to a plain Go value for JSON serialization.
func codongToGo(v Value) interface{} {
	if v == nil { return nil }
	switch val := v.(type) {
	case *CodongMap:
		result := make(map[string]interface{})
		for k, vv := range val.Entries { result[k] = codongToGo(vv) }
		return result
	case *CodongList:
		result := make([]interface{}, len(val.Elements))
		for i, vv := range val.Elements { result[i] = codongToGo(vv) }
		return result
	case *CodongError:
		return map[string]interface{}{"code": val.Code, "message": val.Message}
	default:
		return v
	}
}

// ============================================================
// json module runtime functions
// ============================================================

func cJsonParse(args ...Value) Value {
	if len(args) < 1 { return cError("E6001_PARSE_ERROR", "json.parse requires a string", "fix", "json.parse(string)") }
	s := toString(args[0])
	var result interface{}
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return cError("E6001_PARSE_ERROR", fmt.Sprintf("JSON parse error: %s", err.Error()), "fix", "check JSON syntax, use json.valid() first")
	}
	return goToCodong(result)
}

func cJsonStringify(args ...Value) Value {
	if len(args) < 1 { return "null" }
	goVal := codongToGo(args[0])
	indent := 0
	if len(args) >= 2 { indent = int(toFloat(args[1])) }
	var data []byte
	var err error
	if indent > 0 {
		data, err = json.MarshalIndent(goVal, "", strings.Repeat(" ", indent))
	} else {
		data, err = json.Marshal(goVal)
	}
	if err != nil {
		return cError("E6002_STRINGIFY_ERROR", fmt.Sprintf("JSON stringify error: %s", err.Error()), "fix", "remove circular references")
	}
	return string(data)
}

func cJsonValid(args ...Value) Value {
	if len(args) < 1 { return false }
	return json.Valid([]byte(toString(args[0])))
}

func cJsonMerge(args ...Value) Value {
	if len(args) < 2 {
		if len(args) == 1 { return args[0] }
		return cMap()
	}
	aGo := codongToGo(args[0])
	bGo := codongToGo(args[1])
	aMap, aOk := aGo.(map[string]interface{})
	bMap, bOk := bGo.(map[string]interface{})
	if !aOk || !bOk { return args[1] }
	return goToCodong(cJsonDeepMerge(aMap, bMap))
}

func cJsonDeepMerge(a, b map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range a { result[k] = v }
	for k, v := range b {
		if aVal, exists := result[k]; exists {
			aMap, aOk := aVal.(map[string]interface{})
			bMap, bOk := v.(map[string]interface{})
			if aOk && bOk {
				result[k] = cJsonDeepMerge(aMap, bMap)
				continue
			}
		}
		result[k] = v
	}
	return result
}

func cJsonGet(args ...Value) Value {
	if len(args) < 2 { return nil }
	data := codongToGo(args[0])
	path := toString(args[1])
	var defaultVal interface{}
	if len(args) >= 3 { defaultVal = args[2] }
	parts := strings.Split(path, ".")
	cur := data
	for _, part := range parts {
		m, ok := cur.(map[string]interface{})
		if !ok { return defaultVal }
		cur, ok = m[part]
		if !ok { return defaultVal }
	}
	// Return nil explicitly for null values (don't use default)
	if cur == nil { return nil }
	return goToCodong(cur)
}

func cJsonSet(args ...Value) Value {
	if len(args) < 3 { return nil }
	data := codongToGo(args[0])
	path := toString(args[1])
	value := codongToGo(args[2])
	parts := strings.Split(path, ".")
	result := cJsonSetRecursive(data, parts, value)
	return goToCodong(result)
}

func cJsonSetRecursive(data interface{}, parts []string, value interface{}) interface{} {
	if len(parts) == 0 { return value }
	m, ok := data.(map[string]interface{})
	if !ok { m = make(map[string]interface{}) }
	result := make(map[string]interface{})
	for k, v := range m { result[k] = v }
	if len(parts) == 1 {
		result[parts[0]] = value
	} else {
		existing, _ := result[parts[0]]
		result[parts[0]] = cJsonSetRecursive(existing, parts[1:], value)
	}
	return result
}

func cJsonFlatten(args ...Value) Value {
	if len(args) < 1 { return cMap() }
	data := codongToGo(args[0])
	m, ok := data.(map[string]interface{})
	if !ok { return args[0] }
	flat := make(map[string]interface{})
	cJsonFlattenHelper("", m, flat)
	return goToCodong(flat)
}

func cJsonFlattenHelper(prefix string, m map[string]interface{}, out map[string]interface{}) {
	for k, v := range m {
		key := k
		if prefix != "" { key = prefix + "." + k }
		if nested, ok := v.(map[string]interface{}); ok {
			cJsonFlattenHelper(key, nested, out)
		} else {
			out[key] = v
		}
	}
}

func cJsonUnflatten(args ...Value) Value {
	if len(args) < 1 { return cMap() }
	data := codongToGo(args[0])
	m, ok := data.(map[string]interface{})
	if !ok { return args[0] }
	result := make(map[string]interface{})
	for k, v := range m {
		parts := strings.Split(k, ".")
		cur := result
		for i, part := range parts {
			if i == len(parts)-1 {
				cur[part] = v
			} else {
				next, ok := cur[part]
				if !ok {
					next = make(map[string]interface{})
					cur[part] = next
				}
				cur = next.(map[string]interface{})
			}
		}
	}
	return goToCodong(result)
}

// ============================================================
// env module runtime functions
// ============================================================

func cEnvGet(args ...Value) Value {
	if len(args) < 1 { return nil }
	name := toString(args[0])
	val, ok := os.LookupEnv(name)
	if !ok {
		if len(args) >= 2 { return args[1] }
		return nil
	}
	return val
}

func cEnvRequire(args ...Value) Value {
	if len(args) < 1 { return cError("E7001_ENV_NOT_SET", "env.require needs a variable name", "fix", "env.require(\"VAR\")") }
	name := toString(args[0])
	val, ok := os.LookupEnv(name)
	if !ok {
		return cError("E7001_ENV_NOT_SET", fmt.Sprintf("required environment variable not set: %s", name), "fix", fmt.Sprintf("set environment variable: %s", name))
	}
	return val
}

func cEnvHas(args ...Value) Value {
	if len(args) < 1 { return false }
	_, ok := os.LookupEnv(toString(args[0]))
	return ok
}

func cEnvAll(args ...Value) Value {
	m := &CodongMap{Entries: map[string]Value{}, Order: []string{}}
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			m.Entries[parts[0]] = parts[1]
			m.Order = append(m.Order, parts[0])
		}
	}
	return m
}

func cEnvLoad(args ...Value) Value {
	if len(args) < 1 { return cError("E7002_ENV_FILE_NOT_FOUND", "env.load requires a file path", "fix", "env.load(\".env\")") }
	p := cFsResolve(toString(args[0]))
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cError("E7002_ENV_FILE_NOT_FOUND", fmt.Sprintf(".env file not found: %s", toString(args[0])), "fix", "create .env file or use env.get() with default")
		}
		return cError("E7003_ENV_PARSE_ERROR", err.Error(), "fix", "check file permissions")
	}
	defer f.Close()
	count := float64(0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") { continue }
		eqIdx := strings.Index(line, "=")
		if eqIdx < 0 { continue }
		key := strings.TrimSpace(line[:eqIdx])
		val := strings.TrimSpace(line[eqIdx+1:])
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			val = val[1 : len(val)-1]
		}
		if strings.Contains(val, "\\n") { val = strings.ReplaceAll(val, "\\n", "\n") }
		if _, ok := os.LookupEnv(key); !ok {
			os.Setenv(key, val)
			count++
		}
	}
	return count
}

// ============================================================
// time module runtime functions
// ============================================================

func cTimeSleep(args ...Value) Value {
	if len(args) < 1 { return nil }
	d, err := time.ParseDuration(toString(args[0]))
	if err != nil {
		return cError("E1005_INVALID_ARGUMENT", "invalid duration: "+toString(args[0]), "fix", "use '500ms', '2s', '1m', '1h'")
	}
	time.Sleep(d)
	return nil
}

func cTimeNow(args ...Value) Value {
	return float64(time.Now().UnixMilli())
}

func cTimeNowIso(args ...Value) Value {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func cTimeFormat(args ...Value) Value {
	if len(args) < 2 { return "" }
	tsMs := toFloat(args[0])
	fmtStr := toString(args[1])
	t := time.UnixMilli(int64(tsMs)).UTC()
	switch fmtStr {
	case "date": fmtStr = "2006-01-02"
	case "datetime": fmtStr = "2006-01-02 15:04:05"
	case "iso": fmtStr = time.RFC3339
	case "rfc2822": fmtStr = "Mon, 02 Jan 2006 15:04:05 -0700"
	}
	return t.Format(fmtStr)
}

func cTimeParse(args ...Value) Value {
	if len(args) < 1 { return nil }
	s := toString(args[0])
	formats := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}
	if len(args) >= 2 { formats = []string{toString(args[1])} }
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return float64(t.UnixMilli())
		}
	}
	return nil
}

func cTimeDiff(args ...Value) Value {
	if len(args) < 2 { return nil }
	t1 := int64(toFloat(args[0]))
	t2 := int64(toFloat(args[1]))
	ms := t2 - t1
	if ms < 0 { ms = -ms }
	return cMap("ms", float64(ms), "s", float64(ms/1000), "m", float64(ms/60000), "h", float64(ms/3600000), "days", float64(ms/86400000))
}

func cTimeSince(args ...Value) Value {
	if len(args) < 1 { return nil }
	t1 := toFloat(args[0])
	now := float64(time.Now().UnixMilli())
	ms := now - t1
	if ms < 0 { ms = 0 }
	return cMap("ms", ms, "s", ms/1000, "m", ms/60000, "h", ms/3600000, "days", ms/86400000)
}

func cTimeUntil(args ...Value) Value {
	if len(args) < 1 { return nil }
	t1 := toFloat(args[0])
	now := float64(time.Now().UnixMilli())
	ms := t1 - now
	if ms < 0 { ms = 0 }
	return cMap("ms", ms, "s", ms/1000, "m", ms/60000, "h", ms/3600000, "days", ms/86400000)
}

func cTimeAdd(args ...Value) Value {
	if len(args) < 2 { return nil }
	tsMs := toFloat(args[0])
	offset := toString(args[1])
	d, err := time.ParseDuration(offset)
	if err != nil { return nil }
	t := time.UnixMilli(int64(tsMs))
	return float64(t.Add(d).UnixMilli())
}

func cTimeIsBefore(args ...Value) Value {
	if len(args) < 2 { return false }
	return toFloat(args[0]) < toFloat(args[1])
}

func cTimeIsAfter(args ...Value) Value {
	if len(args) < 2 { return false }
	return toFloat(args[0]) > toFloat(args[1])
}

func cTimeTodayStart(args ...Value) Value {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return float64(start.UnixMilli())
}

func cTimeTodayEnd(args ...Value) Value {
	now := time.Now().UTC()
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999000000, time.UTC)
	return float64(end.UnixMilli())
}

// Ensure all imports are used
var _ = bytes.NewReader
var _ = context.Background
var _ = io.ReadAll
var _ = math.Mod
var _ = os.Exit
var _ = regexp.MustCompile
var _ = signal.Notify
var _ = sort.SliceStable
var _ = sql.Open
var _ = strconv.Atoi
var _ = syscall.SIGINT
var _ = time.Second
var _ = url.PathUnescape
var _ = filepath.Join
var _ = bufio.NewScanner
`
