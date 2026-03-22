package goirgen

// RuntimeSource is the Codong runtime library embedded in every generated Go program.
// It provides dynamic types, operators, and built-in functions.
const RuntimeSource = `
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"bytes"
	"context"

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
	Cause   *CodongError
	IsError bool // always true, used for type checking
}

func (e *CodongError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
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
		switch opts[i].(string) {
		case "fix":
			e.Fix = toString(opts[i+1])
		case "retry":
			e.Retry = toBool(opts[i+1])
		case "docs":
			e.Docs = toString(opts[i+1])
		}
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
		return s.Error()
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
	}
	return nil
}

func cSetIndex(obj Value, idx Value, val Value) {
	switch o := obj.(type) {
	case *CodongList:
		i := int(toFloat(idx))
		if i < 0 { i = len(o.Elements) + i }
		if i >= 0 && i < len(o.Elements) { o.Elements[i] = val }
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
					if len(args) > 0 { cWebMiddlewares = append(cWebMiddlewares, args[0]) }
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

func cDiscard(_ Value) {}

func cRange(start, end float64) *CodongList {
	var elems []Value
	for i := start; i < end; i++ { elems = append(elems, i) }
	return &CodongList{Elements: elems}
}

// --- Error Propagation (?) ---

type cReturnSignal struct{ Value Value }

func cPropagate(v Value) Value {
	if e, ok := v.(*CodongError); ok {
		panic(&cReturnSignal{Value: e})
	}
	return v
}

// --- Web Module ---

var cWebRoutes []struct{ method, pattern string; handler func(...Value) Value }
var cWebServers []*struct{ port int }
var cWebMiddlewares []Value
var cWebMiddlewareNS = &CodongMap{Entries: map[string]interface{}{"_type": "web_middleware_ns"}, Order: []string{"_type"}}

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
			// Client IP
			ip := req.RemoteAddr
			if fwd := req.Header.Get("X-Forwarded-For"); fwd != "" { ip = fwd }
			cSet(reqMap, "ip", ip)
			// Build context from auth middleware headers
			ctxMap := cMap()
			for k, v := range req.Header {
				if strings.HasPrefix(k, "X-Codong-Auth-") {
					ctxKey := strings.ToLower(k[len("X-Codong-Auth-"):])
					cSet(ctxMap, ctxKey, v[0])
				}
			}
			cSet(reqMap, "context", ctxMap)
			// query_all() returns the full query map
			cSet(reqMap, "query_all", func(args ...Value) Value { return qm })
			// Parse body
			if req.Body != nil {
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
					// Store auth context in request header for handler access
					if rm, ok := result.(*CodongMap); ok {
						for k, v := range rm.Entries { r.Header.Set("X-Codong-Auth-"+k, toString(v)) }
					}
					prev.ServeHTTP(w, r)
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
		switch rt {
		case "json":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			jb, _ := json.Marshal(valueToGo(m.Entries["data"]))
			w.Write(jb)
		case "text":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(status)
			fmt.Fprint(w, toString(m.Entries["body"]))
		case "html":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(status)
			fmt.Fprint(w, toString(m.Entries["body"]))
		case "redirect":
			url := toString(m.Entries["url"])
			http.Redirect(w, req, url, status)
			return
		default:
			// Apply custom headers if present
			if hdrs, ok := m.Entries["headers"].(*CodongMap); ok {
				for k, v := range hdrs.Entries { w.Header().Set(k, toString(v)) }
			}
			w.Header().Set("Content-Type", "application/json")
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
	return cMap("_type", "json", "data", data, "status", status)
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
	return true
}

func cDbDisconnectRT() Value {
	if cDB != nil { cDB.Close(); cDB = nil }
	if cDbTempFile != "" { os.Remove(cDbTempFile); cDbTempFile = "" }
	return nil
}

func cDbDisconnect() {
	if cDB != nil { cDB.Close(); cDB = nil }
}

func cDbQuery(sqlStr string, params ...Value) Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	args := make([]interface{}, len(params))
	for i, p := range params { args[i] = valueToGo(p) }
	trimmed := strings.TrimSpace(strings.ToUpper(sqlStr))
	if strings.HasPrefix(trimmed, "SELECT") {
		rows, err := cDB.Query(sqlStr, args...)
		if err != nil { return cError("E2003", err.Error()) }
		defer rows.Close()
		return rowsToList(rows)
	}
	result, err := cDB.Exec(sqlStr, args...)
	if err != nil { return cError("E2003", err.Error()) }
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
	if err := cDB.QueryRow(q, args...).Scan(&count); err != nil { return cError("E2003", err.Error()) }
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
	r, err := cDB.Exec(q, vals...)
	if err != nil { return cError("E2003", err.Error()) }
	id, _ := r.LastInsertId()
	return cMap("id", float64(id))
}

func cDbFind(table string, filterVal Value) *CodongList {
	if cDB == nil { cPrint(cError("E2002", "no database connection")); return &CodongList{} }
	var filter *CodongMap
	if filterVal != nil { filter, _ = filterVal.(*CodongMap) }
	where, args := filterSQL(filter)
	q := "SELECT * FROM " + table
	if where != "" { q += " WHERE " + where }
	rows, err := cDB.Query(q, args...)
	if err != nil { cPrint(cError("E2003", err.Error())); return &CodongList{} }
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
	rows, err := cDB.Query(q, args...)
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
	r, err := cDB.Exec(q, allArgs...)
	if err != nil { return cError("E2003", err.Error()) }
	aff, _ := r.RowsAffected()
	return cMap("affected", float64(aff))
}

func cDbDelete(table string, filterVal Value) Value {
	var filter *CodongMap
	if filterVal != nil { filter, _ = filterVal.(*CodongMap) }
	where, args := filterSQL(filter)
	q := "DELETE FROM " + table
	if where != "" { q += " WHERE " + where }
	r, err := cDB.Exec(q, args...)
	if err != nil { return cError("E2003", err.Error()) }
	aff, _ := r.RowsAffected()
	return cMap("affected", float64(aff))
}

func filterSQL(filter *CodongMap) (string, []interface{}) {
	if filter == nil || len(filter.Entries) == 0 { return "", nil }
	var clauses []string; var args []interface{}
	for _, k := range filter.Order {
		v := filter.Entries[k]
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
	req, _ := http.NewRequest(method, url, bodyReader)
	req.Header.Set("User-Agent", "Codong/0.1")
	if body != nil { req.Header.Set("Content-Type", "application/json") }
	// Apply custom headers from opts
	for _, opt := range opts {
		if m, ok := opt.(*CodongMap); ok {
			if h, ok := m.Entries["headers"].(*CodongMap); ok {
				for k, v := range h.Entries {
					req.Header.Set(k, toString(v))
				}
			}
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil { return cMap("status", float64(0), "ok", false, "body", err.Error()) }
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	rawBody := string(respBody)
	// Build headers map
	hm := cMap()
	for k, v := range resp.Header {
		cSet(hm, strings.ToLower(k), v[0])
	}
	// Build response with callable json() and text()
	m := cMap(
		"status", float64(resp.StatusCode),
		"ok", resp.StatusCode >= 200 && resp.StatusCode < 300,
		"body", rawBody,
		"headers", hm,
	)
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
	jb, _ := json.Marshal(data)
	return string(jb)
}

func cErrorToCompact(err Value) Value {
	e, ok := err.(*CodongError)
	if !ok { return nil }
	return fmt.Sprintf("err_code:%s|src:%s|msg:%s|fix:%s|retry:%v", e.Code, e.Source, e.Message, e.Fix, e.Retry)
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

func cErrorRetry(fn Value, maxAttempts Value) Value {
	max := int(toFloat(maxAttempts))
	var lastErr Value
	for i := 0; i < max; i++ {
		result := cCallFn(fn)
		if e, ok := result.(*CodongError); ok {
			if e.Retry { lastErr = result; continue }
			return result
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

func cCallFn(fn Value, args ...Value) Value {
	if f, ok := fn.(func(...Value) Value); ok {
		return f(args...)
	}
	// Try to call as a CodongMap with callable entries (for req.param("id") pattern)
	if m, ok := fn.(*CodongMap); ok && len(args) > 0 {
		key := toString(args[0])
		if v, exists := m.Entries[key]; exists { return v }
	}
	return nil
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
`
