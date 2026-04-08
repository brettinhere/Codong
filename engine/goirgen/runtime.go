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

	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	imgdraw "image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	goredis "github.com/redis/go-redis/v9"
	_ "modernc.org/sqlite"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
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
		parts := make([]string, len(s.Elements))
		for i, el := range s.Elements { parts[i] = toInspect(el) }
		return "[" + strings.Join(parts, ", ") + "]"
	case *CodongMap:
		parts := make([]string, 0, len(s.Order))
		for _, k := range s.Order {
			parts = append(parts, k+": "+toInspect(s.Entries[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *CodongError:
		return s.Error()
	case func(...Value) Value:
		return "fn (...)"
	}
	return fmt.Sprintf("%v", v)
}

// toInspect returns the display representation of a value (strings are quoted in list/map context).
func toInspect(v Value) string {
	if v == nil { return "null" }
	if s, ok := v.(string); ok { return s } // strings display without quotes at top level
	return toString(v)
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
	if la, ok := a.(*CodongList); ok {
		if lb, ok := b.(*CodongList); ok {
			combined := make([]Value, 0, len(la.Elements)+len(lb.Elements))
			combined = append(combined, la.Elements...)
			combined = append(combined, lb.Elements...)
			return &CodongList{Elements: combined}
		}
	}
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
func cPow(a, b Value) Value { return math.Pow(toFloat(a), toFloat(b)) }

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
		bl, ok := b.(*CodongList)
		if !ok { return false }
		if len(av.Elements) != len(bl.Elements) { return false }
		for i, el := range av.Elements {
			if !cEq(el, bl.Elements[i]) { return false }
		}
		return true
	case *CodongMap:
		bm, ok := b.(*CodongMap)
		if !ok { return false }
		if len(av.Entries) != len(bm.Entries) { return false }
		for k, v := range av.Entries {
			bv, exists := bm.Entries[k]
			if !exists || !cEq(v, bv) { return false }
		}
		return true
	}
	return false
}

// --- Global built-in conversion functions ---

func cToInt(v Value) Value {
	switch s := v.(type) {
	case float64: return math.Trunc(s)
	case string:
		n, err := strconv.ParseFloat(s, 64)
		if err != nil { return float64(0) }
		return math.Trunc(n)
	case bool:
		if s { return float64(1) }
		return float64(0)
	}
	return float64(0)
}

func cToFloat(v Value) Value {
	switch s := v.(type) {
	case float64: return s
	case string:
		n, err := strconv.ParseFloat(s, 64)
		if err != nil { return float64(0) }
		return n
	case bool:
		if s { return float64(1) }
		return float64(0)
	}
	return float64(0)
}

func cToStr(v Value) Value {
	return toString(v)
}

func cChr(v Value) Value {
	if n, ok := v.(float64); ok {
		if n >= 0 && n <= 255 {
			return string(rune(int(n)))
		}
		return ""
	}
	return ""
}

func cEncodingBase64Decode(v Value) Value {
	if s, ok := v.(string); ok {
		decoded, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return ""
		}
		return string(decoded)
	}
	return ""
}

func cEncodingBase64Encode(v Value) Value {
	if s, ok := v.(string); ok {
		encoded := base64.StdEncoding.EncodeToString([]byte(s))
		return encoded
	}
	return ""
}

func cToBool(v Value) Value {
	if v == nil { return false }
	switch s := v.(type) {
	case bool: return s
	case float64: return s != 0
	case string: return s != "" && s != "false" && s != "0"
	}
	return true
}

func cLen(v Value) Value {
	switch s := v.(type) {
	case string: return float64(len(s))
	case *CodongList: return float64(len(s.Elements))
	case *CodongMap: return float64(len(s.Entries))
	}
	return float64(0)
}

func cSort(args ...Value) Value {
	if len(args) < 1 { return &CodongList{} }
	list, ok := args[0].(*CodongList)
	if !ok { return args[0] }
	// Make a copy
	newElems := make([]Value, len(list.Elements))
	copy(newElems, list.Elements)
	result := &CodongList{Elements: newElems}
	if len(args) > 1 {
		compareFn, ok := args[1].(func(...Value) Value)
		if ok {
			sort.Slice(result.Elements, func(a, b int) bool {
				r := compareFn(result.Elements[a], result.Elements[b])
				return toFloat(r) < 0
			})
		}
	} else {
		sort.Slice(result.Elements, func(a, b int) bool {
			return cLt(result.Elements[a], result.Elements[b])
		})
	}
	return result
}

func cGrep(args ...Value) Value {
	if len(args) < 2 { return &CodongList{} }
	list, ok := args[0].(*CodongList)
	if !ok { return &CodongList{} }
	pattern := toString(args[1])
	var result []Value
	for _, el := range list.Elements {
		if strings.Contains(toString(el), pattern) {
			result = append(result, el)
		}
	}
	return &CodongList{Elements: result}
}

func cRand(min, max float64) Value {
	var b [8]byte
	rand.Read(b[:])
	n := float64(binary.BigEndian.Uint64(b[:])>>11) / float64(1<<53)
	return min + n*(max-min)
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
		depth := 1<<31 - 1 // deep flatten by default
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
	case "sum":
		var total float64
		for _, el := range l.Elements { total += toFloat(el) }
		return total
	case "min":
		if len(l.Elements) == 0 { return nil }
		m := toFloat(l.Elements[0])
		for _, el := range l.Elements[1:] { if v := toFloat(el); v < m { m = v } }
		return m
	case "max":
		if len(l.Elements) == 0 { return nil }
		m := toFloat(l.Elements[0])
		for _, el := range l.Elements[1:] { if v := toFloat(el); v > m { m = v } }
		return m
	case "avg":
		if len(l.Elements) == 0 { return nil }
		var total float64
		for _, el := range l.Elements { total += toFloat(el) }
		return total / float64(len(l.Elements))
	case "count":
		if len(args) > 0 {
			cnt := 0
			for _, el := range l.Elements { if cEq(el, args[0]) { cnt++ } }
			return float64(cnt)
		}
		return float64(len(l.Elements))
	case "every":
		if len(args) == 0 { return true }
		fn := args[0].(func(...Value) Value)
		for _, el := range l.Elements { if !isTruthy(fn(el)) { return false } }
		return true
	case "some":
		if len(args) == 0 { return false }
		fn := args[0].(func(...Value) Value)
		for _, el := range l.Elements { if isTruthy(fn(el)) { return true } }
		return false
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
	case "index":
		if len(args) < 1 { return float64(-1) }
		return float64(strings.Index(s, toString(args[0])))
	case "reverse":
		runes := []rune(s)
		for l, r := 0, len(runes)-1; l < r; l, r = l+1, r-1 { runes[l], runes[r] = runes[r], runes[l] }
		return string(runes)
	case "format":
		result := s
		for j, arg := range args {
			result = strings.ReplaceAll(result, fmt.Sprintf("{%d}", j), toString(arg))
		}
		for _, arg := range args {
			result = strings.Replace(result, "{}", toString(arg), 1)
		}
		return result
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
	case "from_entries":
		if len(args) < 1 { return m }
		list, ok := args[0].(*CodongList)
		if !ok { return m }
		nm := &CodongMap{Entries: map[string]Value{}, Order: []string{}}
		for _, el := range list.Elements {
			pair, ok := el.(*CodongList)
			if !ok || len(pair.Elements) < 2 { continue }
			k := toString(pair.Elements[0])
			v := pair.Elements[1]
			if _, exists := nm.Entries[k]; !exists { nm.Order = append(nm.Order, k) }
			nm.Entries[k] = v
		}
		return nm
	case "cookie":
		// Response map: res.cookie(name, value, opts) — stores cookie header for writeResponse
		cookieVal := cWebSetCookie(args...)
		if sc, ok := cookieVal.(*CodongMap).Entries["Set-Cookie"]; ok {
			if existing, ok := m.Entries["_cookies"].(*CodongList); ok {
				existing.Elements = append(existing.Elements, sc)
			} else {
				cSet(m, "_cookies", &CodongList{Elements: []Value{sc}})
			}
		}
		return m
	case "clear_cookie":
		name := ""; if len(args) > 0 { name = toString(args[0]) }
		hdr := fmt.Sprintf("%s=; Path=/; Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:00 GMT", name)
		if existing, ok := m.Entries["_cookies"].(*CodongList); ok {
			existing.Elements = append(existing.Elements, hdr)
		} else {
			cSet(m, "_cookies", &CodongList{Elements: []Value{hdr}})
		}
		return m
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
					if len(args) >= 3 {
						// Route-level middleware: server.get("/path", mw1, mw2, handler)
						finalRouteHandler := args[len(args)-1]
						routeMiddlewares := args[1 : len(args)-1]
						chained := func(reqArgs ...Value) Value {
							var callChain func(idx int, req Value) Value
							callChain = func(idx int, req Value) Value {
								if idx >= len(routeMiddlewares) {
									return cCallFn(finalRouteHandler, req)
								}
								mw := routeMiddlewares[idx]
								nextFn := func(nextArgs ...Value) Value {
									r := req
									if len(nextArgs) > 0 { r = nextArgs[0] }
									return callChain(idx+1, r)
								}
								return cCallFn(mw, req, nextFn)
							}
							req := Value(nil)
							if len(reqArgs) > 0 { req = reqArgs[0] }
							return callChain(0, req)
						}
						return cWebRoute(strings.ToUpper(method), args[0], chained)
					}
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
				case "sse":
					if len(args) >= 2 {
						path := toString(args[0])
						handler := args[1]
						cWebRoutes = append(cWebRoutes, struct{ method, pattern string; handler func(...Value) Value }{"GET", path, func(routeArgs ...Value) Value {
							return cMap("_type", "sse", "_handler", handler)
						}})
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
					m := cMap("_mw_type", "cors")
					if len(args) > 0 {
						if opts, ok := args[0].(*CodongMap); ok {
							if v, exists := opts.Entries["origins"]; exists { cSet(m, "origins", v) }
						}
					}
					return m
				case "rate_limit":
					m := cMap("_mw_type", "rate_limit")
					if len(args) > 0 {
						if opts, ok := args[0].(*CodongMap); ok {
							for k, v := range opts.Entries { cSet(m, k, v) }
						}
					}
					return m
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
		// If the method is not a known error field, propagate the error
		return o
	case *cImageObj:
		return cImageCall(o, method, args...)
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

type cReturnSignal struct{ Value Value; IsErrorProp bool }

// cMapToError converts a Codong error map {code, message, ...} to a CodongError if it looks like one.
func cMapToError(m *CodongMap) *CodongError {
	codeVal, hasCode := m.Entries["code"]
	msgVal, hasMsg := m.Entries["message"]
	if !hasCode || !hasMsg {
		return nil
	}
	code := toString(codeVal)
	msg := toString(msgVal)
	if code == "" && msg == "" {
		return nil
	}
	e := &CodongError{Code: code, Message: msg}
	if fixVal, ok := m.Entries["fix"]; ok {
		e.Fix = toString(fixVal)
	}
	if retryVal, ok := m.Entries["retry"]; ok {
		e.Retry = toBool(retryVal)
	}
	return e
}

func cPropagate(v Value) Value {
	// If it's an error, propagate it up the call stack via panic
	// IsErrorProp=true so function defer doesn't intercept it (only try/catch does)
	if e, ok := v.(*CodongError); ok {
		panic(&cReturnSignal{Value: e, IsErrorProp: true})
	}
	if m, ok := v.(*CodongMap); ok {
		// Check for {error: CodongError} wrapper
		if errVal, ok := m.Entries["error"]; ok {
			if e, ok := errVal.(*CodongError); ok {
				panic(&cReturnSignal{Value: e, IsErrorProp: true})
			}
		}
		// Check for direct error map {code, message, ...}
		if e := cMapToError(m); e != nil {
			panic(&cReturnSignal{Value: e, IsErrorProp: true})
		}
	}
	return v
}

func cPropagateStmt(v Value) {
	// In standalone context (expr?), panic to propagate error up call stack
	if e, ok := v.(*CodongError); ok {
		panic(&cReturnSignal{Value: e, IsErrorProp: true})
	}
	if m, ok := v.(*CodongMap); ok {
		if errVal, ok := m.Entries["error"]; ok {
			if e, ok := errVal.(*CodongError); ok {
				panic(&cReturnSignal{Value: e, IsErrorProp: true})
			}
		}
		// Check for direct error map {code, message, ...}
		if e := cMapToError(m); e != nil {
			panic(&cReturnSignal{Value: e, IsErrorProp: true})
		}
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

// cWebCatchAll registers a catch-all handler for all unmatched routes
var cWebCatchAllHandler func(...Value) Value

func cWebCatchAll(handler Value) Value {
	cWebCatchAllHandler = handler.(func(...Value) Value)
	return nil
}

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
			cSet(reqMap, "header", func(args ...Value) Value {
				if len(args) > 0 { if v, ok := hm.Entries[strings.ToLower(toString(args[0]))]; ok { return v } }
				return nil
			})
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
	
	// Register catch-all handler (after specific routes, so it only catches unmatched)
	if cWebCatchAllHandler != nil {
		catchHandler := cWebCatchAllHandler
		mux.HandleFunc("GET /{path...}", func(w http.ResponseWriter, req *http.Request) {
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
			cSet(reqMap, "param", pm)
			// Parse headers
			hm := cMap()
			for k, v := range req.Header {
				cSet(hm, k, v[0])
				cSet(hm, strings.ToLower(k), v[0])
			}
			cSet(reqMap, "headers", hm)
			cSet(reqMap, "header", func(args ...Value) Value {
				if len(args) > 0 { if v, ok := hm.Entries[strings.ToLower(toString(args[0]))]; ok { return v } }
				return nil
			})
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
			// Context
			ctxMap := cMap()
			cSet(reqMap, "context", ctxMap)
			// query_all()
			cSet(reqMap, "query_all", func(args ...Value) Value { return qm })
			// Parse body
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
			result := catchHandler(reqMap)
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
				corsOrigin := "*"
				if origins, ok := m.Entries["origins"].(*CodongList); ok && len(origins.Elements) > 0 {
					corsOrigin = toString(origins.Elements[0])
				}
				corsO := corsOrigin
				prevCors := prev
				finalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Access-Control-Allow-Origin", corsO)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
					if r.Method == "OPTIONS" { w.WriteHeader(204); return }
					prevCors.ServeHTTP(w, r)
				})
			case "rate_limit":
				maxReqs := 100
				windowDur := time.Minute
				if mv, ok := m.Entries["max"]; ok { maxReqs = int(toFloat(mv)) }
				if wv, ok := m.Entries["window"]; ok { windowDur = cParseDuration(toString(wv)) }
				type rlEntry struct { count int; reset time.Time }
				rlMap := &sync.Map{}
				rlMax := maxReqs; rlWindow := windowDur
				prevRL := prev
				finalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ip := r.RemoteAddr
					now := time.Now()
					actual, _ := rlMap.LoadOrStore(ip, &rlEntry{count: 0, reset: now.Add(rlWindow)})
					entry := actual.(*rlEntry)
					if now.After(entry.reset) { entry.count = 0; entry.reset = now.Add(rlWindow) }
					entry.count++
					if entry.count > rlMax {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(429)
						fmt.Fprint(w, "{\"error\":\"rate limit exceeded\"}")
						return
					}
					prevRL.ServeHTTP(w, r)
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
		} else if fn, ok := mw.(func(...Value) Value); ok {
			// User-defined Codong middleware function: fn(req, next) -> response
			userFn := fn
			prevU := finalHandler
			finalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Build minimal reqMap for the middleware
				reqMap := cMap("method", r.Method, "path", r.URL.Path, "url", r.URL.String())
				hm := cMap()
				for k, v := range r.Header { cSet(hm, strings.ToLower(k), v[0]); cSet(hm, k, v[0]) }
				cSet(reqMap, "headers", hm)
				cSet(reqMap, "header", func(args ...Value) Value {
					if len(args) > 0 { if v, ok := hm.Entries[strings.ToLower(toString(args[0]))]; ok { return v } }
					return nil
				})
				ctxMap := cMap()
				cSet(reqMap, "context", ctxMap)
				// Create next function
				var responded bool
				nextFn := func(args ...Value) Value {
					// Extract context from modified req
					if len(args) > 0 {
						if rm, ok := args[0].(*CodongMap); ok {
							if ctx, ok := rm.Entries["context"].(*CodongMap); ok {
								cWebAuthContext = ctx
							}
						}
					}
					responded = true
					prevU.ServeHTTP(w, r)
					return nil
				}
				result := userFn(reqMap, nextFn)
				if !responded && result != nil {
					writeResponse(w, r, result)
				}
			})
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
	err := server.ListenAndServe()
	if err != nil {
		if err == http.ErrServerClosed {
			// Normal shutdown, exit cleanly
			return nil
		}
		if strings.Contains(err.Error(), "address already in use") || strings.Contains(err.Error(), "bind: address already in use") {
			fmt.Fprintf(os.Stderr, "[E9001_PORT_IN_USE] Port %d is already in use\n", port)
			fmt.Fprintf(os.Stderr, "  fix: stop the process using port %d or use a different port\n", port)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "[E9002_SERVER_ERROR] Server error: %v\n", err)
		os.Exit(1)
	}
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
		// Apply Set-Cookie cookies stored directly on response map
		if cookies, ok := m.Entries["_cookies"].(*CodongList); ok {
			for _, c := range cookies.Elements { w.Header().Add("Set-Cookie", toString(c)) }
		}
		// Legacy: single Set-Cookie field
		if sc, ok := m.Entries["Set-Cookie"]; ok {
			w.Header().Add("Set-Cookie", toString(sc))
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
	m := cMap("_type", "json", "data", data, "body", data, "status", status)
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
		var data Value
		if len(args) == 1 {
			// stream.send(value) — send as data
			data = args[0]
			jb, _ := json.Marshal(valueToGo(data))
			fmt.Fprintf(stream.w, "data: %s\n\n", string(jb))
		} else if len(args) >= 2 {
			// stream.send(event, data)
			event := toString(args[0])
			data = args[1]
			jb, _ := json.Marshal(valueToGo(data))
			fmt.Fprintf(stream.w, "event: %s\ndata: %s\n\n", event, string(jb))
		}
		stream.flusher.Flush()
		return nil
	}
	closeFn := func(args ...Value) Value {
		stream.closed = true
		return nil
	}
	streamMap.Entries["send"] = CodongFn(sendFn)
	streamMap.Entries["close"] = CodongFn(closeFn)
	streamMap.Order = append(streamMap.Order, "send", "close")

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

		// Check if an API route is registered for this exact path
		for _, route := range cWebRoutes {
			rp := route.pattern
			// Strip Go 1.22 {param} patterns for comparison
			rp = strings.Split(rp, " ")[0] // in case method is in pattern
			if rp == r.URL.Path || strings.Contains(rp, "{") {
				// API route exists — let mux handle it
				next.ServeHTTP(w, r)
				return
			}
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
				if dotfiles == "deny" { http.Error(w, "forbidden", http.StatusForbidden) }
				return
			}
		}
		info, err := os.Stat(fsPath)
		if err != nil {
			if os.IsNotExist(err) {
				// SPA fallback
				if spa {
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

// responseRecorder buffers the response to check status before writing
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	body       []byte
	headers    http.Header
	written    bool
}
func newRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: w, headers: make(http.Header)}
}
func (rr *responseRecorder) Header() http.Header { return rr.headers }
func (rr *responseRecorder) WriteHeader(code int) {
	rr.statusCode = code
	rr.written = true
}
func (rr *responseRecorder) Write(b []byte) (int, error) {
	if !rr.written { rr.statusCode = 200; rr.written = true }
	rr.body = append(rr.body, b...)
	return len(b), nil
}
func (rr *responseRecorder) flush(w http.ResponseWriter) {
	for k, vals := range rr.headers {
		for _, v := range vals { w.Header().Add(k, v) }
	}
	if rr.statusCode > 0 && rr.statusCode != 200 { w.WriteHeader(rr.statusCode) }
	w.Write(rr.body)
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

func cParseDuration(s string) time.Duration {
	s = strings.TrimSpace(strings.ToLower(s))
	if d, err := time.ParseDuration(s); err == nil { return d }
	// Handle "1m", "1h", "30s" explicitly via time.ParseDuration-friendly suffixes
	// Support "1min" -> "1m" conversion
	s = strings.ReplaceAll(s, "min", "m")
	s = strings.ReplaceAll(s, "hour", "h")
	s = strings.ReplaceAll(s, "sec", "s")
	s = strings.ReplaceAll(s, "ms", "ms") // already fine
	if d, err := time.ParseDuration(s); err == nil { return d }
	return time.Minute // default fallback
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

func cDbConnect(dsn string, opts ...Value) Value {
	// Auto-detect driver from URL scheme
	if strings.HasPrefix(dsn, "mysql://") || strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		var optsVal Value
		if len(opts) > 0 { optsVal = opts[0] }
		result := cDbConnectMulti(dsn, optsVal)
		// Store named connection if name option provided
		if len(opts) > 0 {
			if m, ok := opts[0].(*CodongMap); ok {
				if name, ok := m.Entries["name"]; ok {
					cDbNamedConns[toString(name)] = cDB
					cDbNamedSchemes[toString(name)] = cDbScheme
				}
			}
		}
		return result
	}

	// Strip SQLite URL prefix if present
	cleanDSN := dsn
	if strings.HasPrefix(dsn, "sqlite:///") { cleanDSN = dsn[len("sqlite:///"):]
	} else if strings.HasPrefix(dsn, "sqlite://") { cleanDSN = dsn[len("sqlite://"):] }
	cDbScheme = "sqlite"
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
	// Apply options
	if len(opts) > 0 {
		if m, ok := opts[0].(*CodongMap); ok {
			if v, ok := m.Entries["max_open"]; ok { cDB.SetMaxOpenConns(int(toFloat(v))) }
			if v, ok := m.Entries["max_idle"]; ok { cDB.SetMaxIdleConns(int(toFloat(v))) }
		}
	}
	if err := cDB.Ping(); err != nil { return cError("E2003", "connection failed: " + err.Error()) }
	// WAL mode for file-based only
	if cleanDSN != "" && !strings.Contains(cleanDSN, "codong-mem") {
		cDB.Exec("PRAGMA journal_mode=WAL")
	}
	// Store named connection if name option provided
	if len(opts) > 0 {
		if m, ok := opts[0].(*CodongMap); ok {
			if name, ok := m.Entries["name"]; ok {
				cDbNamedConns[toString(name)] = cDB
				cDbNamedSchemes[toString(name)] = cDbScheme
			}
		}
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
		return cError("E2002_DUPLICATE_KEY", msg, "fix", "use db.upsert() or check for existing record before insert")
	case strings.Contains(msg, "syntax error") ||
		strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "no such column") ||
		strings.Contains(msg, "near \""):
		return cError("E2004_QUERY_FAILED", msg, "fix", "check SQL syntax and table/column names")
	default:
		return cError("E2003", msg, "fix", "check database connection and query")
	}
}

func cDbQuery(sqlStr string, params ...Value) Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	// Expand IN arrays: if a param is a CodongList, expand the corresponding ? into (?,?,...)
	sqlStr, params = cDbExpandIN(sqlStr, params)
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

// cDbExpandIN expands list parameters into multiple placeholders
func cDbExpandIN(sqlStr string, params []Value) (string, []Value) {
	// Check if any param is a CodongList
	hasLists := false
	for _, p := range params {
		if _, ok := p.(*CodongList); ok { hasLists = true; break }
	}
	if !hasLists { return sqlStr, params }

	var newSQL strings.Builder
	var newParams []Value
	paramIdx := 0
	inSQ := false; inDQ := false
	runes := []rune(sqlStr)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if ch == '\'' && !inDQ { inSQ = !inSQ; newSQL.WriteRune(ch); continue }
		if ch == '"' && !inSQ { inDQ = !inDQ; newSQL.WriteRune(ch); continue }
		if inSQ || inDQ { newSQL.WriteRune(ch); continue }
		if ch == '?' && paramIdx < len(params) {
			p := params[paramIdx]; paramIdx++
			if list, ok := p.(*CodongList); ok {
				if len(list.Elements) == 0 {
					// Empty IN list: use impossible condition
					newSQL.WriteString("NULL")
				} else {
					phs := make([]string, len(list.Elements))
					for j, el := range list.Elements {
						phs[j] = "?"
						newParams = append(newParams, el)
					}
					newSQL.WriteString(strings.Join(phs, ","))
				}
			} else {
				newSQL.WriteRune('?')
				newParams = append(newParams, p)
			}
		} else {
			newSQL.WriteRune(ch)
		}
	}
	return newSQL.String(), newParams
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

func cDbTransaction(fnVal Value, opts ...Value) Value {
	if cDB == nil { return cError("E2002", "no database connection") }
	tx, err := cDB.Begin()
	if err != nil { return cDbError(err) }
	cDbTx = tx
	// Create a tx object with methods that work within the transaction
	txObj := cMap("_type", "db_tx")
	// cTxAutoErr auto-panics with errors in transaction context
	cTxAutoErr := func(result Value) Value {
		if e, ok := result.(*CodongError); ok {
			panic(&cReturnSignal{Value: e, IsErrorProp: true})
		}
		return result
	}
	cSet(txObj, "update", func(args ...Value) Value {
		return cTxAutoErr(cDbUpdate(toString(args[0]), args[1], args[2]))
	})
	cSet(txObj, "insert", func(args ...Value) Value {
		return cTxAutoErr(cDbInsert(toString(args[0]), args[1]))
	})
	cSet(txObj, "delete", func(args ...Value) Value {
		return cTxAutoErr(cDbDelete(toString(args[0]), args[1]))
	})
	cSet(txObj, "query", func(args ...Value) Value {
		var result Value
		if len(args) > 1 {
			if l, ok := args[1].(*CodongList); ok {
				result = cDbQuery(toString(args[0]), l.Elements...)
			} else {
				result = cDbQuery(toString(args[0]), args[1])
			}
		} else {
			result = cDbQuery(toString(args[0]))
		}
		return cTxAutoErr(result)
	})
	cSet(txObj, "query_one", func(args ...Value) Value {
		if len(args) > 1 {
			if l, ok := args[1].(*CodongList); ok { return cDbQueryOne(toString(args[0]), l.Elements...) }
			return cDbQueryOne(toString(args[0]), args[1])
		}
		return cDbQueryOne(toString(args[0]))
	})
	cSet(txObj, "find", func(args ...Value) Value {
		if len(args) > 1 {
			return cDbFind(toString(args[0]), args[1])
		}
		return cDbFind(toString(args[0]), nil)
	})
	cSet(txObj, "find_one", func(args ...Value) Value {
		if len(args) > 1 {
			return cDbFindOne(toString(args[0]), args[1])
		}
		return cDbFindOne(toString(args[0]), nil)
	})
	cSet(txObj, "savepoint", func(args ...Value) Value {
		name := "sp_" + fmt.Sprintf("%d", time.Now().UnixNano())
		if len(args) > 0 { name = toString(args[0]) }
		cDbTx.Exec("SAVEPOINT " + name)
		return name
	})
	cSet(txObj, "rollback_to", func(args ...Value) Value {
		if len(args) > 0 {
			name := toString(args[0])
			cDbTx.Exec("ROLLBACK TO SAVEPOINT " + name)
		}
		return nil
	})
	cSet(txObj, "set_isolation", func(args ...Value) Value {
		// SQLite ignores isolation levels; for MySQL/PG this would be set before BEGIN
		return nil
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
		result = cCallFn(fnVal, txObj)
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

// cDbAggregate performs SUM/AVG/MIN/MAX aggregation on a table column with optional filter.
func cDbAggregate(aggFn, table, col string, filterVal Value) Value {
	if cDB == nil { return nil }
	var filter *CodongMap
	if filterVal != nil { filter, _ = filterVal.(*CodongMap) }
	where, args := filterSQL(filter)
	q := fmt.Sprintf("SELECT %s(%s) FROM %s", aggFn, col, table)
	if where != "" { q += " WHERE " + where }
	var result float64
	if err := cDbQueryRowOne(q, args...).Scan(&result); err != nil { return nil }
	return result
}

// cDbExec executes SQL using the transaction if active, otherwise the main connection.
func cDbExec(q string, args ...interface{}) (sql.Result, error) {
	var result sql.Result
	var err error
	if cDbTx != nil { result, err = cDbTx.Exec(q, args...) } else { result, err = cDB.Exec(q, args...) }
	if err == nil && result != nil {
		if lid, e := result.LastInsertId(); e == nil { cDbLastInsertIdVal = float64(lid) }
	}
	return result, err
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

func cHttpGet(url string, opts ...Value) Value {
	return cHttpDo("GET", url, nil, opts...)
}
func cHttpPost(url string, body Value, opts ...Value) Value {
	return cHttpDo("POST", url, body, opts...)
}
func cHttpDo(method, url string, body Value, opts ...Value) Value {
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
			return cError("E3001_TIMEOUT", "request timed out: " + errStr, "retry", true)
		}
		return cError("E3005_CONN_FAILED", "connection failed: " + errStr)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	rawBody := string(respBody)
	// Build headers map
	hm := cMap()
	for k, v := range resp.Header {
		cSet(hm, strings.ToLower(k), v[0])
	}
	// Build response with callable json() and text()
	// For 4xx/5xx, also add an error field that ? operator can detect
	m := cMap(
		"status", float64(resp.StatusCode),
		"ok", resp.StatusCode >= 200 && resp.StatusCode < 300,
		"body", rawBody,
		"headers", hm,
	)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		cSet(m, "error", cError("E3003_HTTP_4XX", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))))
	} else if resp.StatusCode >= 500 {
		cSet(m, "error", cError("E3004_HTTP_5XX", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))))
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

func cHttpRequest(optsVal Value) Value {
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
	systemMsg := ""; useCache := false
	for _, a := range args {
		if s, ok := a.(string); ok && prompt == "" { prompt = s }
		if m, ok := a.(*CodongMap); ok {
			if v, ok := m.Entries["model"].(string); ok { model = v }
			if v, ok := m.Entries["api_key"].(string); ok { apiKey = v }
			if v, ok := m.Entries["prompt"].(string); ok { prompt = v }
			if v, ok := m.Entries["system"].(string); ok { systemMsg = v }
			if v, ok := m.Entries["cache"].(bool); ok { useCache = v }
		}
	}
	if apiKey == "" {
		if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
			apiKey = k
			if !strings.HasPrefix(model, "claude") { model = "claude-sonnet-4-20250514" }
		}
	}
	if apiKey == "" { return cError("E4005", "no API key", "fix", "export OPENAI_API_KEY") }
	if prompt == "" { return cError("E1005", "no prompt provided") }

	var jb []byte
	url := "https://api.openai.com/v1/chat/completions"

	if strings.HasPrefix(model, "claude") {
		url = "https://api.anthropic.com/v1/messages"
		body := map[string]interface{}{
			"model": model, "max_tokens": 4096,
			"messages": []interface{}{map[string]string{"role": "user", "content": prompt}},
		}
		// Add system message with optional prompt caching
		if systemMsg != "" {
			if useCache {
				// Anthropic prompt caching: system message with cache_control
				body["system"] = []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": systemMsg,
						"cache_control": map[string]string{"type": "ephemeral"},
					},
				}
			} else {
				body["system"] = systemMsg
			}
		}
		jb, _ = json.Marshal(body)
	} else {
		// OpenAI format
		msgs := []map[string]string{}
		if systemMsg != "" {
			msgs = append(msgs, map[string]string{"role": "system", "content": systemMsg})
		}
		msgs = append(msgs, map[string]string{"role": "user", "content": prompt})
		body := map[string]interface{}{
			"model": model, "messages": msgs, "temperature": 0.7, "max_tokens": 4096,
		}
		jb, _ = json.Marshal(body)
	}

	req, _ := http.NewRequest("POST", url, bytes.NewReader(jb))
	req.Header.Set("Content-Type", "application/json")
	if strings.HasPrefix(model, "claude") {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		if useCache {
			req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
		}
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
	// Extract usage info
	var promptTokens, completionTokens, cacheRead, cacheCreate float64
	if u, ok := result["usage"].(map[string]interface{}); ok {
		if pt, ok := u["input_tokens"].(float64); ok { promptTokens = pt }
		if pt, ok := u["prompt_tokens"].(float64); ok { promptTokens = pt }
		if ct, ok := u["output_tokens"].(float64); ok { completionTokens = ct }
		if ct, ok := u["completion_tokens"].(float64); ok { completionTokens = ct }
		// Anthropic cache tokens
		if cr, ok := u["cache_read_input_tokens"].(float64); ok { cacheRead = cr }
		if cc, ok := u["cache_creation_input_tokens"].(float64); ok { cacheCreate = cc }
	}
	// For display: show non-cached prompt tokens only (fair comparison)
	// cached tokens = cacheRead + cacheCreate (these are the SPEC tokens)
	effectivePrompt := promptTokens - cacheRead - cacheCreate
	if effectivePrompt < 0 { effectivePrompt = promptTokens }
	usageMap := cMap(
		"prompt_tokens", effectivePrompt,
		"completion_tokens", completionTokens,
		"total_tokens", effectivePrompt + completionTokens,
		"cache_read", cacheRead,
		"cache_create", cacheCreate,
		"raw_prompt_tokens", promptTokens,
	)

	// OpenAI format
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if c, ok := choices[0].(map[string]interface{}); ok {
			if m, ok := c["message"].(map[string]interface{}); ok {
				if t, ok := m["content"].(string); ok {
					return cMap("text", t, "usage", usageMap, "model", model)
				}
			}
		}
	}
	// Anthropic format
	if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
		if b, ok := content[0].(map[string]interface{}); ok {
			if t, ok := b["text"].(string); ok {
				return cMap("text", t, "usage", usageMap, "model", model)
			}
		}
	}
	return cMap("text", string(respBody), "usage", usageMap, "model", model)
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
			return nil // return null for missing files
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

func cFsIsDir(args ...Value) Value {
	if len(args) < 1 { return false }
	p := cFsResolve(toString(args[0]))
	info, err := os.Stat(p)
	if err != nil { return false }
	return info.IsDir()
}

func cFsLs(args ...Value) Value {
	return cFsList(args...)
}

func cFsRename(args ...Value) Value {
	if len(args) < 2 { return cError("E5008_IO_ERROR", "fs.rename requires src and dst", "fix", "fs.rename(src, dst)") }
	src := cFsResolve(toString(args[0]))
	dst := cFsResolve(toString(args[1]))
	if err := os.Rename(src, dst); err != nil {
		return cError("E5008_IO_ERROR", err.Error(), "fix", "check paths and permissions")
	}
	return true
}

func cFsExt(args ...Value) Value {
	if len(args) < 1 { return "" }
	return filepath.Ext(toString(args[0]))
}

func cFsIsFile(args ...Value) Value {
	if len(args) < 1 { return false }
	p := cFsResolve(toString(args[0]))
	info, err := os.Stat(p)
	if err != nil { return false }
	return !info.IsDir()
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
		if os.IsNotExist(err) {
			return nil // return null for missing files
		}
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
	cwd, _ := os.Getwd()
	return filepath.ToSlash(cwd)
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

func cJsonPretty(args ...Value) Value {
	if len(args) < 1 { return "null" }
	goVal := codongToGo(args[0])
	data, err := json.MarshalIndent(goVal, "", "  ")
	if err != nil {
		return cError("E6002_STRINGIFY_ERROR", fmt.Sprintf("JSON stringify error: %s", err.Error()), "fix", "remove circular references")
	}
	return string(data)
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

// cArgsAll returns all command-line arguments (excluding program name).
func cArgsAll(args ...Value) Value {
	// os.Args[0] is the program name, skip it
	m := &CodongList{Elements: []Value{}}
	if len(os.Args) > 1 {
		for _, arg := range os.Args[1:] {
			m.Elements = append(m.Elements, arg)
		}
	}
	return m
}

// cArgsGet returns a specific command-line argument by index.
func cArgsGet(args ...Value) Value {
	if len(args) < 1 { return nil }
	idx, ok := args[0].(int)
	if !ok || idx < 0 || idx >= len(os.Args)-1 {
		if len(args) >= 2 { return args[1] } // default value
		return nil
	}
	return os.Args[idx+1] // +1 because os.Args[0] is program name
}

// cArgsHas checks if a specific argument exists.
func cArgsHas(args ...Value) Value {
	if len(args) < 1 { return false }
	search := toString(args[0])
	for _, arg := range os.Args[1:] {
		if arg == search {
			return true
		}
	}
	return false
}

// cArgsLen returns the number of command-line arguments.
func cArgsLen(args ...Value) Value {
	return len(os.Args) - 1
}

func cEnvSet(args ...Value) Value {
	if len(args) < 2 { return nil }
	name := toString(args[0])
	val := toString(args[1])
	os.Setenv(name, val)
	return true
}

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
	return float64(ms)
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

func cTimeUnix(args ...Value) Value {
	return float64(time.Now().Unix())
}

func cTimeWeekday(args ...Value) Value {
	if len(args) < 1 { return float64(time.Now().Weekday()) }
	tsMs := int64(toFloat(args[0]))
	t := time.UnixMilli(tsMs).UTC()
	return float64(t.Weekday())
}

func cTimeQuarter(args ...Value) Value {
	if len(args) < 1 {
		m := int(time.Now().Month())
		return float64((m-1)/3 + 1)
	}
	tsMs := int64(toFloat(args[0]))
	t := time.UnixMilli(tsMs).UTC()
	m := int(t.Month())
	return float64((m-1)/3 + 1)
}

func cTimeBefore(args ...Value) Value {
	if len(args) < 2 { return false }
	return toFloat(args[0]) < toFloat(args[1])
}

func cTimeAfter(args ...Value) Value {
	if len(args) < 2 { return false }
	return toFloat(args[0]) > toFloat(args[1])
}

func cTimeTimezone(args ...Value) Value {
	if len(args) < 1 { return cTimeNow() }
	tzName := toString(args[0])
	loc, err := time.LoadLocation(tzName)
	if err != nil { return cTimeNow() }
	t := time.Now().In(loc)
	return float64(t.UnixMilli())
}

func cTimeStat(args ...Value) Value {
	if len(args) < 1 { return nil }
	tsMs := int64(toFloat(args[0]))
	t := time.UnixMilli(tsMs).UTC()
	return cMap(
		"year", float64(t.Year()),
		"month", float64(t.Month()),
		"day", float64(t.Day()),
		"hour", float64(t.Hour()),
		"minute", float64(t.Minute()),
		"second", float64(t.Second()),
		"weekday", float64(t.Weekday()),
		"quarter", float64((int(t.Month())-1)/3+1),
	)
}

// --- DB Extension: MySQL/PostgreSQL ---

var cDbScheme string = "sqlite" // Track active driver scheme

func cDbConnectMulti(dsn string, opts Value) Value {
	scheme := "sqlite"
	if strings.HasPrefix(dsn, "mysql://") { scheme = "mysql" }
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") { scheme = "postgres" }
	if strings.HasPrefix(dsn, "sqlite://") { scheme = "sqlite" }

	var driver, normalizedDSN string
	switch scheme {
	case "mysql":
		driver = "mysql"
		normalizedDSN = cNormalizeMySQLDSN(dsn)
	case "postgres":
		driver = "postgres"
		normalizedDSN = dsn // lib/pq accepts postgres:// URLs
	case "sqlite":
		driver = "sqlite"
		if strings.HasPrefix(dsn, "sqlite:///") { normalizedDSN = dsn[len("sqlite:///"):] } else if strings.HasPrefix(dsn, "sqlite://") { normalizedDSN = dsn[len("sqlite://"):] } else { normalizedDSN = dsn }
	default:
		return cError("E2002", "unsupported database: "+scheme, "fix", "use mysql://, postgres://, or sqlite:// prefix")
	}

	if cDB != nil { cDB.Close() }
	var err error
	cDB, err = sql.Open(driver, normalizedDSN)
	if err != nil { return cError("E2002", "connection failed: "+err.Error()) }

	// Pool defaults
	cDB.SetMaxOpenConns(25)
	cDB.SetMaxIdleConns(5)

	// Apply options
	if m, ok := opts.(*CodongMap); ok && m != nil {
		if v, ok := m.Entries["max_open"]; ok { cDB.SetMaxOpenConns(int(toFloat(v))) }
		if v, ok := m.Entries["max_idle"]; ok { cDB.SetMaxIdleConns(int(toFloat(v))) }
	}

	if err := cDB.Ping(); err != nil {
		cDB.Close(); cDB = nil
		return cError("E2002", "ping failed: "+err.Error())
	}

	if scheme == "sqlite" { cDB.Exec("PRAGMA journal_mode=WAL") }
	cDbScheme = scheme
	return cMap("_type", "db_connection", "status", "connected", "scheme", scheme)
}

func cNormalizeMySQLDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil { return dsn }
	host := u.Hostname(); port := u.Port()
	if port == "" { port = "3306" }
	dbname := strings.TrimPrefix(u.Path, "/")
	user := ""; pass := ""
	if u.User != nil { user = u.User.Username(); pass, _ = u.User.Password() }
	result := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", user, pass, host, port, dbname)
	return result
}

func cDbNormalizeQuery(q string) string {
	if cDbScheme != "postgres" { return q }
	var result strings.Builder
	n := 1; inSQ := false; inDQ := false
	runes := []rune(q)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if ch == '\'' && !inDQ { inSQ = !inSQ; result.WriteRune(ch); continue }
		if ch == '"' && !inSQ { inDQ = !inDQ; result.WriteRune(ch); continue }
		if inSQ || inDQ { result.WriteRune(ch); continue }
		if ch == '?' && i+1 < len(runes) && runes[i+1] == '?' { result.WriteRune('?'); i++; continue }
		if ch == '?' && i+1 < len(runes) && (runes[i+1] == '|' || runes[i+1] == '&') { result.WriteRune('?'); continue }
		if ch == '?' { result.WriteString(fmt.Sprintf("$%d", n)); n++; continue }
		result.WriteRune(ch)
	}
	return result.String()
}

func cDbMigrate(migrations Value) Value {
	if cDB == nil { return cError("E2005", "no database connection") }
	list, ok := migrations.(*CodongList)
	if !ok { return cError("E2005", "migrations must be a list") }
	// Ensure migrations table
	switch cDbScheme {
	case "postgres":
		cDB.Exec("CREATE TABLE IF NOT EXISTS _codong_migrations (version BIGINT PRIMARY KEY, applied_at TIMESTAMPTZ DEFAULT NOW())")
	case "mysql":
		cDB.Exec("CREATE TABLE IF NOT EXISTS _codong_migrations (version BIGINT PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)")
	default:
		cDB.Exec("CREATE TABLE IF NOT EXISTS _codong_migrations (version INTEGER PRIMARY KEY, applied_at TEXT DEFAULT (datetime('now')))")
	}
	// Get applied versions
	applied := map[int64]bool{}
	rows, err := cDB.Query("SELECT version FROM _codong_migrations")
	if err == nil { defer rows.Close(); for rows.Next() { var v int64; rows.Scan(&v); applied[v] = true } }
	// Execute pending
	for _, item := range list.Elements {
		m, ok := item.(*CodongMap)
		if !ok { continue }
		vf, ok := m.Entries["version"]
		if !ok { continue }
		version := int64(toFloat(vf))
		if applied[version] { continue }
		upSQL := ""
		if v, ok := m.Entries["up_"+cDbScheme]; ok { upSQL = toString(v) } else if v, ok := m.Entries["up"]; ok { upSQL = toString(v) }
		if upSQL == "" { continue }
		if _, err := cDB.Exec(upSQL); err != nil { return cError("E2005", fmt.Sprintf("migration %d failed: %s", version, err.Error())) }
		cDB.Exec("INSERT INTO _codong_migrations (version) VALUES (?)", version)
	}
	return nil
}

func cDbMigrationStatus() Value {
	if cDB == nil { return cError("E2005", "no database connection") }
	applied := []Value{}
	var maxV int64
	rows, err := cDB.Query("SELECT version FROM _codong_migrations ORDER BY version")
	if err == nil { defer rows.Close(); for rows.Next() { var v int64; rows.Scan(&v); applied = append(applied, float64(v)); if v > maxV { maxV = v } } }
	return cMap("current", float64(maxV), "pending", float64(0), "applied", &CodongList{Elements: applied})
}

var cDbNamedConns = map[string]*sql.DB{}
var cDbNamedSchemes = map[string]string{}

func cDbUsing(name string) Value {
	if db, ok := cDbNamedConns[name]; ok {
		cDB = db
		if s, ok := cDbNamedSchemes[name]; ok { cDbScheme = s }
		// Return a proxy map with db methods for chaining
		proxy := cMap("_type", "db_using", "_name", name)
		cSet(proxy, "query", func(args ...Value) Value {
			if len(args) > 1 {
				if l, ok := args[1].(*CodongList); ok { return cDbQuery(toString(args[0]), l.Elements...) }
				return cDbQuery(toString(args[0]), args[1])
			}
			return cDbQuery(toString(args[0]))
		})
		cSet(proxy, "query_one", func(args ...Value) Value {
			if len(args) > 1 {
				if l, ok := args[1].(*CodongList); ok { return cDbQueryOne(toString(args[0]), l.Elements...) }
				return cDbQueryOne(toString(args[0]), args[1])
			}
			return cDbQueryOne(toString(args[0]))
		})
		cSet(proxy, "find", func(args ...Value) Value {
			if len(args) > 1 { return cDbFind(toString(args[0]), args[1]) }
			return cDbFind(toString(args[0]), nil)
		})
		cSet(proxy, "find_one", func(args ...Value) Value {
			if len(args) > 1 { return cDbFindOne(toString(args[0]), args[1]) }
			return cDbFindOne(toString(args[0]), nil)
		})
		cSet(proxy, "insert", func(args ...Value) Value {
			return cDbInsert(toString(args[0]), args[1])
		})
		cSet(proxy, "update", func(args ...Value) Value {
			return cDbUpdate(toString(args[0]), args[1], args[2])
		})
		cSet(proxy, "delete", func(args ...Value) Value {
			return cDbDelete(toString(args[0]), args[1])
		})
		return proxy
	}
	return cError("E2003", "no database connection named: "+name)
}

var cDbLastInsertIdVal float64

func cDbLastInsertId() Value { return cDbLastInsertIdVal }

// --- Redis Module ---

var cRedisClients = map[string]*goredis.Client{}
var cRedisDefaultName string
var cRedisDefaultClient *goredis.Client
var cRedisMu sync.Mutex
var cRedisCacheMu sync.Mutex
var cRedisCacheStore = map[string]Value{}
var cRedisCacheLoaderCounts = map[string]int{}
var cRedisSingleflight = map[string]chan struct{}{}

func cRedisGetClient() *goredis.Client {
	if cRedisDefaultName != "" {
		if c, ok := cRedisClients[cRedisDefaultName]; ok { return c }
	}
	return cRedisDefaultClient
}

func cRedisConnect(url string, opts Value) Value {
	// Parse name from opts
	name := ""
	if m, ok := opts.(*CodongMap); ok && m != nil {
		if n, ok := m.Entries["name"]; ok { name = toString(n) }
	}
	// Parse Redis URL
	opt, err := goredis.ParseURL(url)
	if err != nil {
		return cError("E7001_REDIS_ERROR", "failed to parse Redis URL: " + err.Error())
	}
	client := goredis.NewClient(opt)
	if name != "" {
		cRedisClients[name] = client
		if cRedisDefaultClient == nil { cRedisDefaultClient = client; cRedisDefaultName = name }
	} else {
		cRedisDefaultClient = client
	}
	return nil
}

func cRedisDisconnect() Value {
	if c := cRedisGetClient(); c != nil { c.Close() }
	cRedisDefaultClient = nil
	return nil
}

func cRedisUsing(name string) *CodongMap {
	c, ok := cRedisClients[name]
	if !ok { return nil }
	proxy := cMap("_name", name)
	ctx := context.Background()
	// Prefix keys with the namespace to provide isolation
	ns := name + ":"
	cSet(proxy, "set", func(args ...Value) Value {
		key := ns + toString(args[0])
		val := toString(args[1])
		ttl := time.Duration(0)
		if len(args) > 2 {
			if m, ok := args[2].(*CodongMap); ok {
				if t, ok := m.Entries["ttl"]; ok {
					if d, err := time.ParseDuration(toString(t)); err == nil { ttl = d }
				}
			}
		}
		c.Set(ctx, key, val, ttl)
		return true
	})
	cSet(proxy, "get", func(args ...Value) Value {
		key := ns + toString(args[0])
		val, err := c.Get(ctx, key).Result()
		if err != nil {
			if len(args) > 1 { return args[1] }
			return nil
		}
		return val
	})
	cSet(proxy, "delete", func(args ...Value) Value {
		c.Del(ctx, ns+toString(args[0]))
		return nil
	})
	cSet(proxy, "exists", func(args ...Value) Value {
		n, _ := c.Exists(ctx, ns+toString(args[0])).Result()
		return n > 0
	})
	return proxy
}

func cRedisPing() Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	r, err := c.Ping(context.Background()).Result()
	if err != nil { return nil }
	return r
}

func cRedisSet(key, value string, opts Value) Value {
	c := cRedisGetClient()
	if c == nil { return false }
	ttl := time.Duration(0)
	xx := false
	nx := false
	if m, ok := opts.(*CodongMap); ok && m != nil {
		if t, ok := m.Entries["ttl"]; ok {
			ds := toString(t)
			if d, err := time.ParseDuration(ds); err == nil { ttl = d }
		}
		if v, ok := m.Entries["xx"]; ok { xx = toBool(v) }
		if v, ok := m.Entries["nx"]; ok { nx = toBool(v) }
	}
	ctx := context.Background()
	if xx {
		// Only set if key exists
		exists, _ := c.Exists(ctx, key).Result()
		if exists == 0 { return false }
	}
	if nx {
		ok, err := c.SetNX(ctx, key, value, ttl).Result()
		if err != nil { return false }
		return ok
	}
	err := c.Set(ctx, key, value, ttl).Err()
	return err == nil
}

func cRedisGet(key string, defaultVal Value) Value {
	c := cRedisGetClient()
	if c == nil { return defaultVal }
	val, err := c.Get(context.Background(), key).Result()
	if err != nil {
		if defaultVal != nil { return defaultVal }
		return nil
	}
	return val
}

func cRedisDelete(key Value) Value {
	c := cRedisGetClient()
	if c == nil { return float64(0) }
	keys := []string{}
	switch k := key.(type) {
	case string: keys = append(keys, k)
	case *CodongList:
		for _, e := range k.Elements { keys = append(keys, toString(e)) }
	default: keys = append(keys, toString(key))
	}
	n, _ := c.Del(context.Background(), keys...).Result()
	return float64(n)
}

func cRedisExists(key string) Value {
	c := cRedisGetClient()
	if c == nil { return false }
	n, _ := c.Exists(context.Background(), key).Result()
	return n > 0
}

func cRedisExpire(key, dur string) Value {
	c := cRedisGetClient()
	if c == nil { return false }
	d, err := time.ParseDuration(dur)
	if err != nil { return false }
	ok, _ := c.Expire(context.Background(), key, d).Result()
	return ok
}

func cRedisTTL(key string) Value {
	c := cRedisGetClient()
	if c == nil { return float64(-2) }
	d, err := c.TTL(context.Background(), key).Result()
	if err != nil { return float64(-2) }
	if d < 0 { return float64(d.Seconds()) }
	return float64(d.Seconds())
}

func cRedisIncr(key string) Value {
	c := cRedisGetClient()
	if c == nil { return float64(0) }
	n, _ := c.Incr(context.Background(), key).Result()
	return float64(n)
}

func cRedisIncrBy(key string, amount Value) Value {
	c := cRedisGetClient()
	if c == nil { return float64(0) }
	n, _ := c.IncrBy(context.Background(), key, int64(toFloat(amount))).Result()
	return float64(n)
}

func cRedisDecr(key string) Value {
	c := cRedisGetClient()
	if c == nil { return float64(0) }
	n, _ := c.Decr(context.Background(), key).Result()
	return float64(n)
}

func cRedisPipeline(fnVal Value) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	pipe := c.Pipeline()
	ctx := context.Background()
	var results []Value
	// Create pipe proxy object with set/get/del/incr etc methods
	pipeObj := cMap("_type", "redis_pipe")
	cSet(pipeObj, "set", func(args ...Value) Value {
		pipe.Set(ctx, toString(args[0]), toString(args[1]), 0)
		results = append(results, nil) // placeholder
		return nil
	})
	cSet(pipeObj, "get", func(args ...Value) Value {
		pipe.Get(ctx, toString(args[0]))
		results = append(results, nil) // placeholder
		return nil
	})
	cSet(pipeObj, "del", func(args ...Value) Value {
		pipe.Del(ctx, toString(args[0]))
		results = append(results, nil)
		return nil
	})
	cSet(pipeObj, "delete", func(args ...Value) Value {
		pipe.Del(ctx, toString(args[0]))
		results = append(results, nil)
		return nil
	})
	cSet(pipeObj, "incr", func(args ...Value) Value {
		pipe.Incr(ctx, toString(args[0]))
		results = append(results, nil)
		return nil
	})
	cSet(pipeObj, "zadd", func(args ...Value) Value {
		key := toString(args[0])
		if m, ok := args[1].(*CodongMap); ok {
			members := []goredis.Z{}
			for k, v := range m.Entries {
				members = append(members, goredis.Z{Score: toFloat(v), Member: k})
			}
			pipe.ZAdd(ctx, key, members...)
		}
		results = append(results, nil)
		return nil
	})
	cSet(pipeObj, "zremrangebyscore", func(args ...Value) Value {
		pipe.ZRemRangeByScore(ctx, toString(args[0]), toString(args[1]), toString(args[2]))
		results = append(results, nil)
		return nil
	})
	cSet(pipeObj, "expire", func(args ...Value) Value {
		d, _ := time.ParseDuration(toString(args[1]))
		pipe.Expire(ctx, toString(args[0]), d)
		results = append(results, nil)
		return nil
	})
	// Call the callback with the pipe proxy
	if f, ok := fnVal.(func(...Value) Value); ok {
		f(pipeObj)
	} else if f, ok := fnVal.(CodongFn); ok {
		f(pipeObj)
	}
	// Execute pipeline
	cmds, _ := pipe.Exec(ctx)
	// Collect results using type switch for each command type
	finalResults := make([]Value, len(cmds))
	for i, cmd := range cmds {
		switch c := cmd.(type) {
		case *goredis.StringCmd:
			val, e := c.Result()
			if e != nil { finalResults[i] = nil } else { finalResults[i] = val }
		case *goredis.IntCmd:
			val, e := c.Result()
			if e != nil { finalResults[i] = nil } else { finalResults[i] = float64(val) }
		case *goredis.StatusCmd:
			val, e := c.Result()
			if e != nil { finalResults[i] = nil } else { finalResults[i] = val }
		case *goredis.BoolCmd:
			val, e := c.Result()
			if e != nil { finalResults[i] = nil } else { finalResults[i] = val }
		case *goredis.FloatCmd:
			val, e := c.Result()
			if e != nil { finalResults[i] = nil } else { finalResults[i] = val }
		case *goredis.Cmd:
			val, e := c.Result()
			if e != nil { finalResults[i] = nil } else { finalResults[i] = goToValue(val) }
		default:
			finalResults[i] = nil
		}
	}
	return &CodongList{Elements: finalResults}
}

func cRedisCache(key string, fn Value, opts Value) Value {
	c := cRedisGetClient()
	if c == nil {
		if f, ok := fn.(CodongFn); ok { return f() }
		return nil
	}
	ctx := context.Background()
	ttl := 5 * time.Minute
	cacheNull := true
	nullTTLDivisor := float64(10)
	if m, ok := opts.(*CodongMap); ok && m != nil {
		if t, ok := m.Entries["ttl"]; ok {
			if d, err := time.ParseDuration(toString(t)); err == nil { ttl = d }
		}
		if v, ok := m.Entries["cache_null"]; ok { cacheNull = toBool(v) }
	}
	// Singleflight: if another goroutine is loading this key, wait for it
	cRedisCacheMu.Lock()
	if ch, loading := cRedisSingleflight[key]; loading {
		cRedisCacheMu.Unlock()
		<-ch // wait for the other loader to finish
		// Try cache again
		val, err := c.Get(ctx, key).Result()
		if err == nil {
			if val == "__codong_null__" { return nil }
			return val // return raw string
		}
		return nil
	}
	ch := make(chan struct{})
	cRedisSingleflight[key] = ch
	cRedisCacheMu.Unlock()
	defer func() {
		cRedisCacheMu.Lock()
		delete(cRedisSingleflight, key)
		close(ch)
		cRedisCacheMu.Unlock()
	}()
	// Check cache first
	val, err := c.Get(ctx, key).Result()
	if err == nil {
		if val == "__codong_null__" { return nil }
		return val // return raw string
	}
	// Cache miss — call loader (catch panics so singleflight is released)
	cRedisCacheMu.Lock()
	cRedisCacheLoaderCounts[key]++
	cRedisCacheMu.Unlock()
	var result Value
	var loaderPanic interface{}
	func() {
		defer func() { loaderPanic = recover() }()
		if f, ok := fn.(CodongFn); ok { result = f() }
	}()
	if loaderPanic != nil {
		panic(loaderPanic) // re-throw after releasing singleflight
	}
	// If loader returned a CodongError (from ? operator), don't cache — propagate error
	if ce, ok := result.(*CodongError); ok {
		panic(&cReturnSignal{Value: ce, IsErrorProp: true})
	}
	// Store in cache
	if result == nil {
		if cacheNull {
			nullTTL := time.Duration(float64(ttl) / nullTTLDivisor)
			c.Set(ctx, key, "__codong_null__", nullTTL)
		}
	} else {
		jsonStr := cJsonStringify(result)
		c.Set(ctx, key, toString(jsonStr), ttl)
	}
	return result
}

func cRedisInvalidate(key string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	c.Del(context.Background(), key)
	return nil
}

func cRedisInvalidatePattern(pattern string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	ctx := context.Background()
	keys, _ := c.Keys(ctx, pattern).Result()
	if len(keys) > 0 { c.Del(ctx, keys...) }
	return nil
}

func cRedisLock(key string, opts Value) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	lockTTL := 10 * time.Second
	timeout := 5 * time.Second
	retryInterval := 100 * time.Millisecond
	if m, ok := opts.(*CodongMap); ok && m != nil {
		if t, ok := m.Entries["ttl"]; ok {
			if d, err := time.ParseDuration(toString(t)); err == nil { lockTTL = d }
		}
		if t, ok := m.Entries["timeout"]; ok {
			if d, err := time.ParseDuration(toString(t)); err == nil { timeout = d }
		}
		if t, ok := m.Entries["retry"]; ok {
			if d, err := time.ParseDuration(toString(t)); err == nil { retryInterval = d }
		}
	}
	ctx := context.Background()
	lockID := cGenerateRandomHex(16)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok, err := c.SetNX(ctx, key, lockID, lockTTL).Result()
		if err == nil && ok {
			lockObj := cMap("key", key, "_lock_id", lockID, "ttl", lockTTL.String())
			cSet(lockObj, "release", func(args ...Value) Value {
				// Only release if we still own the lock
				val, err := c.Get(ctx, key).Result()
				if err == nil && val == lockID {
					c.Del(ctx, key)
				}
				return nil
			})
			// Watchdog: extend TTL periodically
			go func() {
				ticker := time.NewTicker(lockTTL / 3)
				defer ticker.Stop()
				for range ticker.C {
					val, err := c.Get(ctx, key).Result()
					if err != nil || val != lockID { return }
					c.Expire(ctx, key, lockTTL)
				}
			}()
			return lockObj
		}
		time.Sleep(retryInterval)
	}
	return nil // timeout
}

func cRedisPublish(channel, message string) Value {
	c := cRedisGetClient()
	if c == nil { return float64(0) }
	n, _ := c.Publish(context.Background(), channel, message).Result()
	return float64(n)
}

func cRedisSubscribe(channel string, handler Value) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	sub := c.Subscribe(ctx, channel)
	subObj := cMap("channel", channel)
	cSet(subObj, "unsubscribe", func(args ...Value) Value {
		cancel()
		sub.Close()
		return nil
	})
	go func() {
		ch := sub.Channel()
		for msg := range ch {
			if f, ok := handler.(CodongFn); ok {
				f(msg.Payload)
			} else if f, ok := handler.(func(...Value) Value); ok {
				f(msg.Payload)
			}
		}
	}()
	return subObj
}

func cRedisZadd(key string, members Value) Value {
	c := cRedisGetClient()
	if c == nil { return float64(0) }
	m, ok := members.(*CodongMap)
	if !ok { return float64(0) }
	zMembers := []goredis.Z{}
	for k, v := range m.Entries {
		zMembers = append(zMembers, goredis.Z{Score: toFloat(v), Member: k})
	}
	n, _ := c.ZAdd(context.Background(), key, zMembers...).Result()
	return float64(n)
}

func cRedisZrange(key string, start, stop Value) Value {
	c := cRedisGetClient()
	if c == nil { return &CodongList{} }
	vals, _ := c.ZRange(context.Background(), key, int64(toFloat(start)), int64(toFloat(stop))).Result()
	elems := make([]Value, len(vals))
	for i, v := range vals { elems[i] = v }
	return &CodongList{Elements: elems}
}

func cRedisZrevrange(key string, start, stop Value, opts Value) Value {
	c := cRedisGetClient()
	if c == nil { return &CodongList{} }
	withScores := false
	if m, ok := opts.(*CodongMap); ok && m != nil {
		if v, ok := m.Entries["with_scores"]; ok { withScores = toBool(v) }
	}
	if withScores {
		zs, _ := c.ZRevRangeWithScores(context.Background(), key, int64(toFloat(start)), int64(toFloat(stop))).Result()
		elems := make([]Value, len(zs))
		for i, z := range zs {
			elems[i] = cMap("member", fmt.Sprintf("%v", z.Member), "score", z.Score)
		}
		return &CodongList{Elements: elems}
	}
	vals, _ := c.ZRevRange(context.Background(), key, int64(toFloat(start)), int64(toFloat(stop))).Result()
	elems := make([]Value, len(vals))
	for i, v := range vals { elems[i] = v }
	return &CodongList{Elements: elems}
}

func cRedisZcard(key string) Value {
	c := cRedisGetClient()
	if c == nil { return float64(0) }
	n, _ := c.ZCard(context.Background(), key).Result()
	return float64(n)
}

func cRedisZrank(key, member string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	n, err := c.ZRank(context.Background(), key, member).Result()
	if err != nil { return nil }
	return float64(n)
}

func cRedisZrevrank(key, member string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	n, err := c.ZRevRank(context.Background(), key, member).Result()
	if err != nil { return nil }
	return float64(n)
}

func cRedisZscore(key, member string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	s, err := c.ZScore(context.Background(), key, member).Result()
	if err != nil { return nil }
	return float64(s)
}

func cRedisZincrby(key, member string, incr Value) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	s, err := c.ZIncrBy(context.Background(), key, toFloat(incr), member).Result()
	if err != nil { return nil }
	return float64(s)
}

// Hash operations
func cRedisHSet(key, field, value string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	err := c.HSet(context.Background(), key, field, value).Err()
	if err != nil { return nil }
	return true
}

func cRedisHGet(key, field string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	val, err := c.HGet(context.Background(), key, field).Result()
	if err != nil { return nil }
	return val
}

func cRedisHGetAll(key string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	result, err := c.HGetAll(context.Background(), key).Result()
	if err != nil { return nil }
	m := &CodongMap{Entries: make(map[string]interface{}), Order: []string{}}
	for k, v := range result {
		m.Entries[k] = v
		m.Order = append(m.Order, k)
	}
	return m
}

func cRedisHDel(key, field string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	n, _ := c.HDel(context.Background(), key, field).Result()
	return float64(n)
}

// List operations
func cRedisLPush(key, value string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	n, err := c.LPush(context.Background(), key, value).Result()
	if err != nil { return nil }
	return float64(n)
}

func cRedisRPush(key, value string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	n, err := c.RPush(context.Background(), key, value).Result()
	if err != nil { return nil }
	return float64(n)
}

func cRedisLPop(key string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	val, err := c.LPop(context.Background(), key).Result()
	if err != nil { return nil }
	return val
}

func cRedisRPop(key string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	val, err := c.RPop(context.Background(), key).Result()
	if err != nil { return nil }
	return val
}

func cRedisLRange(key string, start, stop Value) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	s := int64(toFloat(start))
	e := int64(toFloat(stop))
	vals, err := c.LRange(context.Background(), key, s, e).Result()
	if err != nil { return nil }
	elems := make([]Value, len(vals))
	for i, v := range vals { elems[i] = v }
	return &CodongList{Elements: elems}
}

func cRedisLLen(key string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	n, err := c.LLen(context.Background(), key).Result()
	if err != nil { return nil }
	return float64(n)
}

// ZSet count / remove
func cRedisZCount(key string, min, max Value) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	minStr := fmt.Sprintf("%v", toFloat(min))
	maxStr := fmt.Sprintf("%v", toFloat(max))
	n, err := c.ZCount(context.Background(), key, minStr, maxStr).Result()
	if err != nil { return nil }
	return float64(n)
}

func cRedisZRem(key, member string) Value {
	c := cRedisGetClient()
	if c == nil { return nil }
	c.ZRem(context.Background(), key, member)
	return nil
}

func cRedisRateLimiter(config Value) Value {
	c := cRedisGetClient()
	m, ok := config.(*CodongMap)
	if !ok { return nil }
	key := toString(m.Entries["key"])
	rate := int64(toFloat(m.Entries["rate"]))
	if maxV, ok := m.Entries["max"]; ok && rate == 0 { rate = int64(toFloat(maxV)) }
	burst := int64(toFloat(m.Entries["burst"]))
	if burst == 0 { burst = rate }
	window := 60 * time.Second
	if w, ok := m.Entries["window"]; ok {
		if d, err := time.ParseDuration(toString(w)); err == nil { window = d }
	}
	limiter := cMap("key", key, "rate", float64(rate), "burst", float64(burst))
	checkFn := func(args ...Value) Value {
		if c == nil { return cMap("allowed", true, "remaining", float64(burst)) }
		ctx := context.Background()
		now := time.Now()
		// Build per-user key if argument provided
		rlKey := key
		if len(args) > 0 && args[0] != nil { rlKey = key + ":" + toString(args[0]) }
		windowStart := now.Add(-window).UnixMilli()
		// Remove old entries
		c.ZRemRangeByScore(ctx, rlKey, "0", fmt.Sprintf("%d", windowStart))
		// Count current entries
		count, _ := c.ZCard(ctx, rlKey).Result()
		if count >= burst {
			return cMap("allowed", false, "remaining", float64(0))
		}
		// Add new entry
		c.ZAdd(ctx, rlKey, goredis.Z{Score: float64(now.UnixMilli()), Member: fmt.Sprintf("%d", now.UnixNano())})
		c.Expire(ctx, rlKey, window)
		return cMap("allowed", true, "remaining", float64(burst-count-1))
	}
	cSet(limiter, "allow", checkFn)
	cSet(limiter, "check", checkFn)
	return limiter
}

// --- Image Module ---

const cMaxImageWidth = 8192
const cMaxImageHeight = 8192
const cMaxImagePixels = 50000000
const cMaxImageFileSize int64 = 100 * 1024 * 1024

func cImageOpen(path string) Value {
	absPath := path
	if !filepath.IsAbs(path) { absPath = filepath.Join(cFsWorkDir, path) }
	info, err := os.Stat(absPath)
	if err != nil { return cError("E12007", "cannot open image: "+err.Error()) }
	if info.Size() > cMaxImageFileSize { return cError("E12003", "file too large") }
	f, err := os.Open(absPath)
	if err != nil { return cError("E12007", "cannot open: "+err.Error()) }
	defer f.Close()
	config, format, err := image.DecodeConfig(f)
	if err != nil { return cError("E12002", "invalid image: "+err.Error()) }
	if config.Width > cMaxImageWidth || config.Height > cMaxImageHeight { return cError("E12003", "image dimensions exceed limit") }
	if config.Width * config.Height > cMaxImagePixels { return cError("E12003", "total pixels exceed limit") }
	f.Seek(0, 0)
	img, _, err := image.Decode(f)
	if err != nil { return cError("E12002", "decode failed: "+err.Error()) }
	return &cImageObj{img: img, format: format, path: absPath}
}

type cImageObj struct {
	img    image.Image
	format string
	path   string
}

func cImageFromBytes(args ...string) Value {
	if len(args) < 1 { return cError("E12002", "from_bytes requires data") }
	data := args[0]
	reader := bytes.NewReader([]byte(data))
	config, format, err := image.DecodeConfig(reader)
	if err != nil { return cError("E12002", "invalid image") }
	if config.Width > cMaxImageWidth || config.Height > cMaxImageHeight { return cError("E12003", "too large") }
	reader.Seek(0, 0)
	img, _, err := image.Decode(reader)
	if err != nil { return cError("E12002", "decode failed") }
	// If format hint provided, use it
	if len(args) > 1 {
		hint := strings.ToLower(args[1])
		hint = strings.TrimPrefix(hint, "image/")
		if hint != "" { format = hint }
	}
	return &cImageObj{img: img, format: format}
}

func cImageInfo(path string) Value {
	absPath := path
	if !filepath.IsAbs(path) { absPath = filepath.Join(cFsWorkDir, path) }
	info, err := os.Stat(absPath)
	if err != nil { return nil }
	f, err := os.Open(absPath)
	if err != nil { return nil }
	defer f.Close()
	config, format, err := image.DecodeConfig(f)
	if err != nil { return nil }
	channels := float64(3)
	f.Seek(0, 0)
	tmpImg, _, _ := image.Decode(f)
	if tmpImg != nil {
		switch tmpImg.ColorModel() {
		case color.RGBAModel, color.NRGBA64Model, color.NRGBAModel:
			channels = 4
		}
	}
	return cMap("width", float64(config.Width), "height", float64(config.Height), "format", format, "size_bytes", float64(info.Size()), "channels", channels)
}

func cImageReadExif(path string) Value { return cMap() }

// cImageCall handles method calls on image objects
func cImageCall(obj *cImageObj, method string, args ...Value) Value {
	switch method {
	case "resize":
		return cImageResize(obj, args...)
	case "save":
		return cImageSave(obj, args...)
	case "width":
		return float64(obj.img.Bounds().Dx())
	case "height":
		return float64(obj.img.Bounds().Dy())
	case "crop":
		return cImageCropNamed(obj, args...)
	case "crop_center":
		return cImageCropCenter(obj, args...)
	case "to_grayscale":
		return cImageGrayscale(obj)
	case "flip_horizontal":
		return cImageFlipH(obj)
	case "flip_vertical":
		return cImageFlipV(obj)
	case "rotate":
		return cImageRotate(obj, args...)
	case "fit":
		return cImageFit(obj, args...)
	case "cover":
		return cImageCover(obj, args...)
	case "thumbnail":
		return cImageThumbnail(obj, args...)
	case "auto_rotate", "strip_metadata":
		return obj
	case "to_base64":
		return cImageToBase64(obj, args...)
	case "to_bytes":
		return cImageToBytes(obj, args...)
	case "info":
		return cImageObjInfo(obj)
	case "brightness":
		return cImageBrightness(obj, args...)
	case "blur":
		return cImageBlur(obj, args...)
	case "sharpen":
		return cImageSharpen(obj, args...)
	case "contrast":
		return cImageContrast(obj, args...)
	case "tint":
		return cImageTint(obj, args...)
	case "gamma":
		return cImageGamma(obj, args...)
	case "extend":
		return cImageExtend(obj, args...)
	case "watermark_text":
		return cImageWatermarkText(obj, args...)
	case "watermark_image":
		return cImageWatermarkImage(obj, args...)
	case "set_metadata":
		return obj // no-op, return same image
	case "optimize":
		return obj // optimization is a no-op, return same image
	case "watermark_tile":
		return cImageWatermarkTile(obj, args...)
	case "smart_crop":
		return cImageSmartCrop(obj, args...)
	case "to_rgb":
		return cImageToRGB(obj)
	}
	return nil
}

// cImageWatermarkTile tiles a watermark image across the base image.
func cImageWatermarkTile(obj *cImageObj, args ...Value) Value {
	// args[0] = watermark image obj or path, args[1] = spacing (optional)
	if len(args) < 1 { return obj }
	spacing := 0
	if len(args) > 1 { spacing = int(toFloat(args[1])) }
	var wmImg image.Image
	switch wm := args[0].(type) {
	case *cImageObj:
		wmImg = wm.img
	case string:
		if opened, ok := cImageOpen(wm).(*cImageObj); ok {
			wmImg = opened.img
		}
	}
	if wmImg == nil { return obj }
	dst := image.NewRGBA(obj.img.Bounds())
	imgdraw.Draw(dst, dst.Bounds(), obj.img, image.Point{}, imgdraw.Src)
	wmB := wmImg.Bounds()
	stepX := wmB.Dx() + spacing
	stepY := wmB.Dy() + spacing
	b := obj.img.Bounds()
	for y := 0; y < b.Dy(); y += stepY {
		for x := 0; x < b.Dx(); x += stepX {
			imgdraw.Draw(dst, image.Rect(x, y, x+wmB.Dx(), y+wmB.Dy()), wmImg, wmB.Min, imgdraw.Over)
		}
	}
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

// cImageSmartCrop crops the image to center-weighted smart crop.
func cImageSmartCrop(obj *cImageObj, args ...Value) Value {
	if len(args) < 2 { return obj }
	tw := int(toFloat(args[0]))
	th := int(toFloat(args[1]))
	return &cImageObj{img: cCropImg(obj.img, 0, 0, tw, th), format: obj.format, path: obj.path}
}

// cImageToRGB converts image to RGBA (no-op for our impl, already RGBA).
func cImageToRGB(obj *cImageObj) Value {
	dst := image.NewRGBA(obj.img.Bounds())
	imgdraw.Draw(dst, dst.Bounds(), obj.img, image.Point{}, imgdraw.Src)
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

func cImageResize(obj *cImageObj, args ...Value) Value {
	bounds := obj.img.Bounds()
	origW := float64(bounds.Dx()); origH := float64(bounds.Dy())
	var newW, newH int
	if len(args) >= 2 && args[0] != nil && args[1] != nil {
		newW = int(toFloat(args[0])); newH = int(toFloat(args[1]))
	} else if len(args) >= 1 && args[0] != nil {
		newW = int(toFloat(args[0])); newH = int(float64(newW) * origH / origW)
	} else { return obj }
	if newW <= 0 || newH <= 0 { return obj }
	return &cImageObj{img: cResizeImg(obj.img, newW, newH), format: obj.format, path: obj.path}
}

func cImageFit(obj *cImageObj, args ...Value) Value {
	if len(args) < 2 { return obj }
	maxW := int(toFloat(args[0])); maxH := int(toFloat(args[1]))
	b := obj.img.Bounds()
	rX := float64(maxW) / float64(b.Dx()); rY := float64(maxH) / float64(b.Dy())
	r := rX; if rY < r { r = rY }
	if r >= 1.0 { return obj }
	return &cImageObj{img: cResizeImg(obj.img, int(float64(b.Dx())*r), int(float64(b.Dy())*r)), format: obj.format, path: obj.path}
}

func cImageCover(obj *cImageObj, args ...Value) Value {
	if len(args) < 2 { return obj }
	tw := int(toFloat(args[0])); th := int(toFloat(args[1]))
	b := obj.img.Bounds()
	rX := float64(tw) / float64(b.Dx()); rY := float64(th) / float64(b.Dy())
	r := rX; if rY > r { r = rY }
	nw := int(float64(b.Dx()) * r); nh := int(float64(b.Dy()) * r)
	resized := cResizeImg(obj.img, nw, nh)
	x := (nw - tw) / 2; y := (nh - th) / 2
	return &cImageObj{img: cCropImg(resized, x, y, tw, th), format: obj.format, path: obj.path}
}

func cImageCrop(obj *cImageObj, args ...Value) Value {
	if len(args) < 4 { return obj }
	x := int(toFloat(args[0])); y := int(toFloat(args[1])); w := int(toFloat(args[2])); h := int(toFloat(args[3]))
	return &cImageObj{img: cCropImg(obj.img, x, y, w, h), format: obj.format, path: obj.path}
}

// cImageCropNamed handles crop with named args: img.crop(x:0, y:0, width:100, height:100)
func cImageCropNamed(obj *cImageObj, args ...Value) Value {
	if len(args) >= 4 {
		// Positional args
		return cImageCrop(obj, args...)
	}
	if len(args) >= 1 {
		if m, ok := args[0].(*CodongMap); ok {
			x := 0; y := 0; w := obj.img.Bounds().Dx(); h := obj.img.Bounds().Dy()
			if v, ok := m.Entries["x"]; ok { x = int(toFloat(v)) }
			if v, ok := m.Entries["y"]; ok { y = int(toFloat(v)) }
			if v, ok := m.Entries["width"]; ok { w = int(toFloat(v)) }
			if v, ok := m.Entries["height"]; ok { h = int(toFloat(v)) }
			// Clamp to image bounds
			b := obj.img.Bounds()
			if x + w > b.Dx() { w = b.Dx() - x }
			if y + h > b.Dy() { h = b.Dy() - y }
			if w <= 0 || h <= 0 { return obj }
			return &cImageObj{img: cCropImg(obj.img, x, y, w, h), format: obj.format, path: obj.path}
		}
	}
	return obj
}

func cImageCropCenter(obj *cImageObj, args ...Value) Value {
	if len(args) < 2 { return obj }
	w := int(toFloat(args[0])); h := int(toFloat(args[1]))
	b := obj.img.Bounds()
	x := (b.Dx() - w) / 2; y := (b.Dy() - h) / 2
	if x < 0 { x = 0 }; if y < 0 { y = 0 }
	if w > b.Dx() { w = b.Dx() }; if h > b.Dy() { h = b.Dy() }
	return &cImageObj{img: cCropImg(obj.img, x, y, w, h), format: obj.format, path: obj.path}
}

func cImageThumbnail(obj *cImageObj, args ...Value) Value {
	if len(args) < 2 { return obj }
	maxW := int(toFloat(args[0])); maxH := int(toFloat(args[1]))
	b := obj.img.Bounds()
	rX := float64(maxW) / float64(b.Dx()); rY := float64(maxH) / float64(b.Dy())
	r := rX; if rY < r { r = rY }
	nw := int(float64(b.Dx()) * r); nh := int(float64(b.Dy()) * r)
	if nw <= 0 { nw = 1 }; if nh <= 0 { nh = 1 }
	return &cImageObj{img: cResizeImg(obj.img, nw, nh), format: obj.format, path: obj.path}
}

func cImageObjInfo(obj *cImageObj) Value {
	b := obj.img.Bounds()
	channels := float64(3)
	switch obj.img.ColorModel() {
	case color.RGBAModel, color.NRGBA64Model, color.NRGBAModel:
		channels = 4
	}
	return cMap("width", float64(b.Dx()), "height", float64(b.Dy()), "format", obj.format, "channels", channels)
}

func cImageToBytes(obj *cImageObj, args ...Value) Value {
	format := "jpeg"
	quality := 85
	if len(args) > 0 { format = strings.ToLower(toString(args[0])) }
	if len(args) > 1 {
		if m, ok := args[1].(*CodongMap); ok {
			if v, ok := m.Entries["quality"]; ok { quality = int(toFloat(v)) }
		}
	}
	var buf bytes.Buffer
	switch format {
	case "jpeg", "jpg": jpeg.Encode(&buf, obj.img, &jpeg.Options{Quality: quality})
	case "png": png.Encode(&buf, obj.img)
	default: jpeg.Encode(&buf, obj.img, &jpeg.Options{Quality: quality})
	}
	return string(buf.Bytes())
}

func cImageBrightness(obj *cImageObj, args ...Value) Value {
	factor := 1.0
	if len(args) > 0 { factor = toFloat(args[0]) }
	b := obj.img.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := obj.img.At(x, y).RGBA()
			nr := cClamp8(float64(r>>8) * factor)
			ng := cClamp8(float64(g>>8) * factor)
			nb := cClamp8(float64(bl>>8) * factor)
			dst.SetRGBA(x, y, color.RGBA{nr, ng, nb, uint8(a>>8)})
		}
	}
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

func cImageBlur(obj *cImageObj, args ...Value) Value {
	radius := 2
	if len(args) > 0 {
		if m, ok := args[0].(*CodongMap); ok {
			if v, ok := m.Entries["radius"]; ok { radius = int(toFloat(v)) }
		} else {
			radius = int(toFloat(args[0]))
		}
	}
	if radius < 1 { radius = 1 }
	b := obj.img.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			var rr, gg, bb, aa float64; count := 0.0
			for dy := -radius; dy <= radius; dy++ {
				for dx := -radius; dx <= radius; dx++ {
					nx := x + dx; ny := y + dy
					if nx >= b.Min.X && nx < b.Max.X && ny >= b.Min.Y && ny < b.Max.Y {
						r, g, bl, a := obj.img.At(nx, ny).RGBA()
						rr += float64(r>>8); gg += float64(g>>8); bb += float64(bl>>8); aa += float64(a>>8); count++
					}
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(rr/count), uint8(gg/count), uint8(bb/count), uint8(aa/count)})
		}
	}
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

func cImageSharpen(obj *cImageObj, args ...Value) Value {
	b := obj.img.Bounds()
	dst := image.NewRGBA(b)
	// Simple 3x3 sharpen kernel: center=5, neighbors=-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			cr, cg, cb, ca := obj.img.At(x, y).RGBA()
			var rr, gg, bb float64
			rr = float64(cr>>8) * 5; gg = float64(cg>>8) * 5; bb = float64(cb>>8) * 5
			for _, d := range [][2]int{{-1,0},{1,0},{0,-1},{0,1}} {
				nx, ny := x+d[0], y+d[1]
				if nx >= b.Min.X && nx < b.Max.X && ny >= b.Min.Y && ny < b.Max.Y {
					r, g, bl, _ := obj.img.At(nx, ny).RGBA()
					rr -= float64(r>>8); gg -= float64(g>>8); bb -= float64(bl>>8)
				}
			}
			dst.SetRGBA(x, y, color.RGBA{cClamp8(rr), cClamp8(gg), cClamp8(bb), uint8(ca>>8)})
		}
	}
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

func cImageContrast(obj *cImageObj, args ...Value) Value {
	factor := 1.0
	if len(args) > 0 { factor = toFloat(args[0]) }
	b := obj.img.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := obj.img.At(x, y).RGBA()
			nr := cClamp8((float64(r>>8) - 128) * factor + 128)
			ng := cClamp8((float64(g>>8) - 128) * factor + 128)
			nb := cClamp8((float64(bl>>8) - 128) * factor + 128)
			dst.SetRGBA(x, y, color.RGBA{nr, ng, nb, uint8(a>>8)})
		}
	}
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

func cImageTint(obj *cImageObj, args ...Value) Value {
	tr, tg, tb := uint8(0), uint8(0), uint8(255)
	if len(args) > 0 {
		hex := toString(args[0])
		hex = strings.TrimPrefix(hex, "#")
		if len(hex) == 6 {
			if v, err := strconv.ParseUint(hex[0:2], 16, 8); err == nil { tr = uint8(v) }
			if v, err := strconv.ParseUint(hex[2:4], 16, 8); err == nil { tg = uint8(v) }
			if v, err := strconv.ParseUint(hex[4:6], 16, 8); err == nil { tb = uint8(v) }
		}
	}
	b := obj.img.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := obj.img.At(x, y).RGBA()
			nr := uint8((float64(r>>8) + float64(tr)) / 2)
			ng := uint8((float64(g>>8) + float64(tg)) / 2)
			nb := uint8((float64(bl>>8) + float64(tb)) / 2)
			dst.SetRGBA(x, y, color.RGBA{nr, ng, nb, uint8(a>>8)})
		}
	}
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

func cImageGamma(obj *cImageObj, args ...Value) Value {
	gamma := 1.0
	if len(args) > 0 { gamma = toFloat(args[0]) }
	if gamma <= 0 { gamma = 1.0 }
	invGamma := 1.0 / gamma
	b := obj.img.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := obj.img.At(x, y).RGBA()
			nr := uint8(255 * math.Pow(float64(r>>8)/255.0, invGamma))
			ng := uint8(255 * math.Pow(float64(g>>8)/255.0, invGamma))
			nb := uint8(255 * math.Pow(float64(bl>>8)/255.0, invGamma))
			dst.SetRGBA(x, y, color.RGBA{nr, ng, nb, uint8(a>>8)})
		}
	}
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

func cImageExtend(obj *cImageObj, args ...Value) Value {
	top, right, bottom, left := 0, 0, 0, 0
	if len(args) > 0 {
		if m, ok := args[0].(*CodongMap); ok {
			if v, ok := m.Entries["top"]; ok { top = int(toFloat(v)) }
			if v, ok := m.Entries["right"]; ok { right = int(toFloat(v)) }
			if v, ok := m.Entries["bottom"]; ok { bottom = int(toFloat(v)) }
			if v, ok := m.Entries["left"]; ok { left = int(toFloat(v)) }
		}
	}
	b := obj.img.Bounds()
	newW := b.Dx() + left + right
	newH := b.Dy() + top + bottom
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	// Fill with white by default
	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			dst.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	// Draw original image at offset
	imgdraw.Draw(dst, image.Rect(left, top, left+b.Dx(), top+b.Dy()), obj.img, b.Min, imgdraw.Over)
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

func cImageWatermarkText(obj *cImageObj, args ...Value) Value {
	// Basic watermark: just return the image as-is (text rendering requires freetype)
	return obj
}

func cImageWatermarkImage(obj *cImageObj, args ...Value) Value {
	if len(args) < 1 { return obj }
	overlayPath := toString(args[0])
	if !filepath.IsAbs(overlayPath) { overlayPath = filepath.Join(cFsWorkDir, overlayPath) }
	f, err := os.Open(overlayPath)
	if err != nil { return obj }
	defer f.Close()
	overlay, _, err := image.Decode(f)
	if err != nil { return obj }
	b := obj.img.Bounds()
	dst := image.NewRGBA(b)
	imgdraw.Draw(dst, b, obj.img, b.Min, imgdraw.Src)
	// Default: bottom-right corner, scale overlay
	scale := 0.2
	if len(args) > 1 {
		if m, ok := args[1].(*CodongMap); ok {
			if v, ok := m.Entries["scale"]; ok { scale = toFloat(v) }
		}
	}
	ob := overlay.Bounds()
	ow := int(float64(b.Dx()) * scale); oh := int(float64(ob.Dy()) * float64(ow) / float64(ob.Dx()))
	if ow < 1 { ow = 1 }; if oh < 1 { oh = 1 }
	resized := cResizeImg(overlay, ow, oh)
	ox := b.Dx() - ow - 10; oy := b.Dy() - oh - 10
	imgdraw.Draw(dst, image.Rect(ox, oy, ox+ow, oy+oh), resized, image.Point{}, imgdraw.Over)
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

func cClamp8(v float64) uint8 {
	if v < 0 { return 0 }
	if v > 255 { return 255 }
	return uint8(v)
}

func cImageGrayscale(obj *cImageObj) Value {
	b := obj.img.Bounds()
	gray := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ { gray.Set(x, y, obj.img.At(x, y)) }
	}
	return &cImageObj{img: gray, format: obj.format, path: obj.path}
}

func cImageFlipH(obj *cImageObj) Value {
	b := obj.img.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ { dst.Set(b.Max.X-1-x, y, obj.img.At(x, y)) }
	}
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

func cImageFlipV(obj *cImageObj) Value {
	b := obj.img.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ { dst.Set(x, b.Max.Y-1-y, obj.img.At(x, y)) }
	}
	return &cImageObj{img: dst, format: obj.format, path: obj.path}
}

func cImageRotate(obj *cImageObj, args ...Value) Value {
	if len(args) < 1 { return obj }
	deg := int(toFloat(args[0])) % 360; if deg < 0 { deg += 360 }
	b := obj.img.Bounds()
	switch deg {
	case 90:
		dst := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
		for y := b.Min.Y; y < b.Max.Y; y++ { for x := b.Min.X; x < b.Max.X; x++ { dst.Set(b.Max.Y-1-y, x, obj.img.At(x, y)) } }
		return &cImageObj{img: dst, format: obj.format}
	case 180:
		dst := image.NewRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ { for x := b.Min.X; x < b.Max.X; x++ { dst.Set(b.Max.X-1-x, b.Max.Y-1-y, obj.img.At(x, y)) } }
		return &cImageObj{img: dst, format: obj.format}
	case 270:
		dst := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
		for y := b.Min.Y; y < b.Max.Y; y++ { for x := b.Min.X; x < b.Max.X; x++ { dst.Set(y, b.Max.X-1-x, obj.img.At(x, y)) } }
		return &cImageObj{img: dst, format: obj.format}
	}
	return obj
}

func cImageSave(obj *cImageObj, args ...Value) Value {
	if len(args) < 1 { return cError("E12006", "save requires path") }
	outPath := toString(args[0])
	if !filepath.IsAbs(outPath) { outPath = filepath.Join(cFsWorkDir, outPath) }
	quality := 85
	if len(args) > 1 { if m, ok := args[1].(*CodongMap); ok { if v, ok := m.Entries["quality"]; ok { quality = int(toFloat(v)) } } }
	os.MkdirAll(filepath.Dir(outPath), 0755)
	f, err := os.Create(outPath)
	if err != nil { return cError("E12006", "cannot create file: "+err.Error()) }
	defer f.Close()
	ext := strings.ToLower(filepath.Ext(outPath))
	switch ext {
	case ".jpg", ".jpeg":
		jpeg.Encode(f, obj.img, &jpeg.Options{Quality: quality})
	case ".png":
		png.Encode(f, obj.img)
	case ".gif":
		gif.Encode(f, obj.img, nil)
	case ".webp":
		// Go stdlib has no WebP encoder; save as PNG (lossless) in WebP container
		png.Encode(f, obj.img)
	default:
		png.Encode(f, obj.img)
	}
	return obj
}

func cImageToBase64(obj *cImageObj, args ...Value) Value {
	format := "jpeg"
	if len(args) > 0 { format = strings.ToLower(toString(args[0])) }
	var buf bytes.Buffer
	switch format {
	case "jpeg", "jpg": jpeg.Encode(&buf, obj.img, &jpeg.Options{Quality: 85})
	case "png": png.Encode(&buf, obj.img)
	default: png.Encode(&buf, obj.img)
	}
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	return fmt.Sprintf("data:image/%s;base64,%s", format, b64)
}

// cImageCreate creates a blank image with the given dimensions and background color.
func cImageCreate(widthVal, heightVal Value, colorHex string) Value {
	w := int(toFloat(widthVal))
	h := int(toFloat(heightVal))
	if w <= 0 || h <= 0 { return nil }
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Parse hex color
	r, g, b := uint8(255), uint8(255), uint8(255)
	hex := strings.TrimPrefix(colorHex, "#")
	if len(hex) == 6 {
		fmt.Sscanf(hex[0:2], "%02x", &r)
		fmt.Sscanf(hex[2:4], "%02x", &g)
		fmt.Sscanf(hex[4:6], "%02x", &b)
	}
	c := color.RGBA{R: r, G: g, B: b, A: 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return &cImageObj{img: img, format: "png"}
}

func cResizeImg(src image.Image, nw, nh int) image.Image {
	b := src.Bounds(); dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	sx := float64(b.Dx()) / float64(nw); sy := float64(b.Dy()) / float64(nh)
	for y := 0; y < nh; y++ { for x := 0; x < nw; x++ {
		srcX := int(float64(x)*sx) + b.Min.X; srcY := int(float64(y)*sy) + b.Min.Y
		if srcX >= b.Max.X { srcX = b.Max.X - 1 }; if srcY >= b.Max.Y { srcY = b.Max.Y - 1 }
		dst.Set(x, y, src.At(srcX, srcY))
	}}
	return dst
}

func cCropImg(src image.Image, x, y, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	imgdraw.Draw(dst, dst.Bounds(), src, image.Pt(x+src.Bounds().Min.X, y+src.Bounds().Min.Y), imgdraw.Src)
	return dst
}

// --- OAuth Module ---

type cOAuthProviderCfg struct {
	Name, ClientID, ClientSecret, RedirectURI string
	Scopes []string
	AuthURL, TokenURL, UserInfoURL, TenantID string
}

var cOAuthProviders = map[string]*cOAuthProviderCfg{}
var cJWTSecret string = "codong-default-secret"
var cJWTExpiresIn int64 = 86400
var cJWTRefreshExpiresIn int64 = 2592000
var cJWTIncludeJTI bool

var cOAuthRoles = map[string][]string{}

func cOAuthProvider(name string, config Value) Value {
	m := config.(*CodongMap)
	p := &cOAuthProviderCfg{Name: name}
	if v, ok := m.Entries["client_id"]; ok { p.ClientID = toString(v) }
	if v, ok := m.Entries["client_secret"]; ok { p.ClientSecret = toString(v) }
	if v, ok := m.Entries["redirect_uri"]; ok { p.RedirectURI = toString(v) }
	if v, ok := m.Entries["tenant_id"]; ok { p.TenantID = toString(v) }
	if v, ok := m.Entries["scopes"]; ok { if l, ok := v.(*CodongList); ok { for _, s := range l.Elements { p.Scopes = append(p.Scopes, toString(s)) } } }

	switch name {
	case "github":
		p.AuthURL = "https://github.com/login/oauth/authorize"
		p.TokenURL = "https://github.com/login/oauth/access_token"
		p.UserInfoURL = "https://api.github.com/user"
	case "google":
		p.AuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
		p.TokenURL = "https://oauth2.googleapis.com/token"
		p.UserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	case "microsoft":
		tenant := "common"; if p.TenantID != "" { tenant = p.TenantID }
		p.AuthURL = "https://login.microsoftonline.com/"+tenant+"/oauth2/v2.0/authorize"
		p.TokenURL = "https://login.microsoftonline.com/"+tenant+"/oauth2/v2.0/token"
		p.UserInfoURL = "https://graph.microsoft.com/v1.0/me"
	}
	cOAuthProviders[name] = p
	return nil
}

func cOAuthConfigureJWT(config Value) Value {
	m := config.(*CodongMap)
	if v, ok := m.Entries["secret"]; ok { cJWTSecret = toString(v) }
	if v, ok := m.Entries["expires_in"]; ok { if d, err := time.ParseDuration(toString(v)); err == nil { cJWTExpiresIn = int64(d.Seconds()) } }
	if v, ok := m.Entries["refresh_expires_in"]; ok { if d, err := time.ParseDuration(toString(v)); err == nil { cJWTRefreshExpiresIn = int64(d.Seconds()) } }
	if v, ok := m.Entries["include_jti"]; ok { cJWTIncludeJTI = toBool(v) }
	return nil
}

func cOAuthAuthorizationURL(name string, opts Value) Value {
	p, ok := cOAuthProviders[name]
	if !ok { return cError("E14007", "provider not configured: "+name) }
	u, _ := url.Parse(p.AuthURL)
	q := u.Query()
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("response_type", "code")
	if len(p.Scopes) > 0 { q.Set("scope", strings.Join(p.Scopes, " ")) }
	if m, ok := opts.(*CodongMap); ok && m != nil {
		if v, ok := m.Entries["state"]; ok { q.Set("state", toString(v)) }
		if v, ok := m.Entries["code_challenge"]; ok { q.Set("code_challenge", toString(v)) }
		if v, ok := m.Entries["code_challenge_method"]; ok { q.Set("code_challenge_method", toString(v)) }
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func cOAuthExchangeCode(name, code string, opts Value) Value {
	p, ok := cOAuthProviders[name]
	if !ok { return cError("E14002", "provider not configured: "+name) }
	params := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {p.RedirectURI}, "client_id": {p.ClientID}, "client_secret": {p.ClientSecret},
	}
	if m, ok := opts.(*CodongMap); ok && m != nil {
		if v, ok := m.Entries["code_verifier"]; ok { params.Set("code_verifier", toString(v)) }
	}
	req, _ := http.NewRequest("POST", p.TokenURL, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return cError("E14002", "exchange failed: "+err.Error()) }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var tokenResp map[string]interface{}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		vals, err2 := url.ParseQuery(string(body))
		if err2 != nil { return cError("E14002", "parse failed") }
		tokenResp = map[string]interface{}{}
		for k, v := range vals { if len(v) > 0 { tokenResp[k] = v[0] } }
	}
	if e, ok := tokenResp["error"]; ok { return cError("E14002", fmt.Sprintf("provider error: %v", e)) }
	result := cMap()
	for k, v := range tokenResp { cSet(result, k, fmt.Sprintf("%v", v)) }
	return result
}

func cOAuthGetProfile(name, accessToken string) Value {
	p, ok := cOAuthProviders[name]
	if !ok { return cError("E14008", "provider not configured: "+name) }
	req, _ := http.NewRequest("GET", p.UserInfoURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	if name == "github" { req.Header.Set("User-Agent", "Codong-OAuth") }
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return cError("E14008", "profile fetch failed: "+err.Error()) }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var profile map[string]interface{}
	if err := json.Unmarshal(body, &profile); err != nil { return cError("E14008", "parse failed") }
	result := cMap("provider", name)
	for k, v := range profile { cSet(result, k, fmt.Sprintf("%v", v)) }
	return result
}

func cOAuthSignJWT(claims Value, opts Value) Value {
	m := claims.(*CodongMap)
	payload := map[string]interface{}{}
	for _, k := range m.Order {
		v := m.Entries[k]
		switch val := v.(type) {
		case float64: payload[k] = val
		case string: payload[k] = val
		case bool: payload[k] = val
		case *CodongList:
			arr := make([]interface{}, len(val.Elements))
			for j, el := range val.Elements { arr[j] = toString(el) }
			payload[k] = arr
		default: payload[k] = toString(v)
		}
	}
	now := time.Now()
	exp := cJWTExpiresIn
	if mo, ok := opts.(*CodongMap); ok && mo != nil {
		if v, ok := mo.Entries["expires_in"]; ok { if d, err := time.ParseDuration(toString(v)); err == nil { exp = int64(d.Seconds()) } }
	}
	payload["iat"] = float64(now.Unix())
	payload["exp"] = float64(now.Unix() + exp)
	if cJWTIncludeJTI { payload["jti"] = cGenerateRandomHex(16) }
	return cSignHS256(payload, cJWTSecret)
}

func cOAuthSignRefreshToken(claims Value) Value {
	m := claims.(*CodongMap)
	payload := map[string]interface{}{}
	for _, k := range m.Order { payload[k] = toString(m.Entries[k]) }
	now := time.Now()
	payload["iat"] = float64(now.Unix())
	payload["exp"] = float64(now.Unix() + cJWTRefreshExpiresIn)
	payload["type"] = "refresh"
	return cSignHS256(payload, cJWTSecret)
}

func cOAuthVerifyJWT(token string) Value {
	claims, err := cVerifyHS256(token, cJWTSecret)
	if err != nil { return nil }
	if exp, ok := claims["exp"].(float64); ok { if time.Now().Unix() >= int64(exp) { return nil } }
	result := cMap()
	for k, v := range claims {
		switch val := v.(type) {
		case float64: cSet(result, k, val)
		case string: cSet(result, k, val)
		case bool: cSet(result, k, val)
		case []interface{}:
			elems := make([]Value, len(val))
			for j, el := range val { elems[j] = fmt.Sprintf("%v", el) }
			cSet(result, k, &CodongList{Elements: elems})
		default: cSet(result, k, fmt.Sprintf("%v", v))
		}
	}
	return result
}

func cOAuthDecodeJWT(token string) Value {
	parts := strings.Split(token, ".")
	if len(parts) != 3 { return nil }
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil { return nil }
	var claims map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil { return nil }
	result := cMap()
	for k, v := range claims { cSet(result, k, fmt.Sprintf("%v", v)) }
	return result
}

var cRevokedJWTs = map[string]int64{} // jti → exp
var cRevokedMu sync.RWMutex

func cOAuthRevokeJWT(token string) Value {
	parts := strings.Split(token, ".")
	if len(parts) != 3 { return nil }
	payloadBytes, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]interface{}
	json.Unmarshal(payloadBytes, &claims)
	jti, _ := claims["jti"].(string)
	if jti == "" { h := sha256.Sum256([]byte(token)); jti = hex.EncodeToString(h[:]) }
	exp := int64(0)
	if e, ok := claims["exp"].(float64); ok { exp = int64(e) }
	cRevokedMu.Lock()
	cRevokedJWTs[jti] = exp
	cRevokedMu.Unlock()
	return nil
}

func cOAuthIsRevoked(token string) Value {
	parts := strings.Split(token, ".")
	if len(parts) != 3 { return false }
	payloadBytes, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]interface{}
	json.Unmarshal(payloadBytes, &claims)
	jti, _ := claims["jti"].(string)
	if jti == "" { h := sha256.Sum256([]byte(token)); jti = hex.EncodeToString(h[:]) }
	cRevokedMu.RLock()
	_, revoked := cRevokedJWTs[jti]
	cRevokedMu.RUnlock()
	return revoked
}

func cOAuthGenerateState() Value {
	return cGenerateRandomHex(32)
}

func cOAuthGeneratePKCE() Value {
	verifierBytes := make([]byte, 32)
	rand.Read(verifierBytes)
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	return cMap("code_verifier", verifier, "code_challenge", challenge, "method", "S256")
}

func cOAuthHashToken(token string) Value {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// PKCE sub-module: oauth.pkce.verifier() / oauth.pkce.challenge(verifier)
func cOAuthPKCEVerifier() Value {
	verifierBytes := make([]byte, 32)
	rand.Read(verifierBytes)
	return base64.RawURLEncoding.EncodeToString(verifierBytes)
}

func cOAuthPKCEChallenge(verifier string) Value {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// RBAC sub-module: oauth.rbac.define / oauth.rbac.assign / oauth.rbac.check
var cOAuthUserRoles = map[string]string{} // username -> role

func cOAuthRBACDefine(roles Value) Value {
	return cOAuthDefineRoles(roles)
}

func cOAuthRBACAssign(user, role string) Value {
	cOAuthUserRoles[user] = role
	return nil
}

func cOAuthRBACCheck(roleOrUser, permission string) Value {
	// Check if roleOrUser is a defined role first
	if perms, ok := cOAuthRoles[roleOrUser]; ok {
		for _, p := range perms {
			if p == permission { return true }
		}
		return false
	}
	// Otherwise treat as user, look up their role
	if role, ok := cOAuthUserRoles[roleOrUser]; ok {
		if perms, ok2 := cOAuthRoles[role]; ok2 {
			for _, p := range perms {
				if p == permission { return true }
			}
		}
	}
	return false
}

func cOAuthDefineRoles(roles Value) Value {
	m := roles.(*CodongMap)
	for _, k := range m.Order {
		v := m.Entries[k]
		if l, ok := v.(*CodongList); ok {
			perms := make([]string, len(l.Elements))
			for j, el := range l.Elements { perms[j] = toString(el) }
			cOAuthRoles[k] = perms
		}
	}
	return nil
}

func cOAuthHasPermission(roles Value, permission string) Value {
	var userRoles []string
	if l, ok := roles.(*CodongList); ok {
		for _, el := range l.Elements { userRoles = append(userRoles, toString(el)) }
	}
	for _, role := range userRoles {
		if perms, ok := cOAuthRoles[role]; ok {
			for _, p := range perms {
				if p == permission { return true }
				if strings.HasSuffix(p, ":*") && strings.HasPrefix(permission, strings.TrimSuffix(p, "*")) { return true }
			}
		}
	}
	return false
}

func cGenerateRandomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func cSignHS256(payload map[string]interface{}, secret string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(` + "`" + `{"alg":"HS256","typ":"JWT"}` + "`" + `))
	payloadBytes, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signingInput := header + "." + payloadB64
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature
}

func cVerifyHS256(token, secret string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 { return nil, fmt.Errorf("invalid token") }
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) { return nil, fmt.Errorf("invalid signature") }
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil { return nil, err }
	var claims map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil { return nil, err }
	return claims, nil
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
var _ = hmac.New
var _ = rand.Read
var _ = sha256.New
var _ = hex.EncodeToString
var _ = image.DecodeConfig
var _ = color.RGBA{}
var _ = imgdraw.Src
var _ = gif.Encode
var _ = jpeg.Encode
var _ = png.Encode
var _ = http.NewRequest
`
