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
			if v, exists := o.Entries[s]; ok { return v; _ = exists }
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
		// User-defined function fields take priority
		if fn, ok := o.Entries[method]; ok {
			if f, ok := fn.(func(...Value) Value); ok { return f(args...) }
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

func cWebGet(pattern string, handler func(...Value) Value) {
	cWebRoutes = append(cWebRoutes, struct{ method, pattern string; handler func(...Value) Value }{"GET", pattern, handler})
}
func cWebPost(pattern string, handler func(...Value) Value) {
	cWebRoutes = append(cWebRoutes, struct{ method, pattern string; handler func(...Value) Value }{"POST", pattern, handler})
}

func cWebServe(port int) {
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
			writeResponse(w, result)
		})
	}
	addr := fmt.Sprintf(":%d", port)
	server := &http.Server{Addr: addr, Handler: mux}
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
}

func writeResponse(w http.ResponseWriter, result Value) {
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
		default:
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

func cWebJson(data Value) *CodongMap {
	return cMap("_type", "json", "data", data, "status", float64(200))
}
func cWebText(body Value) *CodongMap {
	return cMap("_type", "text", "body", body, "status", float64(200))
}
func cWebHtml(body Value) *CodongMap {
	return cMap("_type", "html", "body", body, "status", float64(200))
}

// --- DB Module ---

var cDB *sql.DB

func cDbConnect(dsn string) {
	var err error
	cDB, err = sql.Open("sqlite", dsn)
	if err != nil { panic(cError("E2002", "db connect failed: " + err.Error())) }
	cDB.Exec("PRAGMA journal_mode=WAL")
}

func cDbDisconnect() {
	if cDB != nil { cDB.Close(); cDB = nil }
}

func cDbQuery(sqlStr string, params ...Value) Value {
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

func cDbInsert(table string, data *CodongMap) Value {
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

func cDbFind(table string, filter *CodongMap) *CodongList {
	where, args := filterSQL(filter)
	q := "SELECT * FROM " + table
	if where != "" { q += " WHERE " + where }
	rows, err := cDB.Query(q, args...)
	if err != nil { panic(cError("E2003", err.Error())) }
	defer rows.Close()
	return rowsToList(rows)
}

func cDbFindOne(table string, filter *CodongMap) Value {
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

func cDbUpdate(table string, filter *CodongMap, data *CodongMap) Value {
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

func cDbDelete(table string, filter *CodongMap) Value {
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil { return cMap("status", float64(0), "ok", false, "body", err.Error()) }
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return cMap("status", float64(resp.StatusCode), "ok", resp.StatusCode >= 200 && resp.StatusCode < 300, "body", string(respBody))
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
