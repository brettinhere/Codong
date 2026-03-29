# Codong Language Specification — AI Edition

This document is the AI-optimized version of SPEC.md. Every rule includes a correct example and an incorrect example. Inject this into any LLM system prompt to enable correct Codong code generation.

---

## 1. Basic Syntax

**File encoding:** UTF-8. Statements end at newline. No semicolons.

```
// CORRECT
x = 10
name = "codong"

// WRONG — no semicolons
x = 10;
```

**Comments:**

```
// CORRECT — single line
// this is a comment

// CORRECT — multi line
/* this spans
   multiple lines */

// WRONG — no # comments
# this is not valid
```

**Identifiers:** letters, digits, `_`. Must start with letter or `_`.

```
// CORRECT
user_name = "Ada"
_private = 42

// WRONG — no hyphens, no starting with digit
user-name = "Ada"
2fast = true
```

**String escape sequences:**

```
// CORRECT — supported escapes
msg = "line1\nline2"       // newline
msg = "col1\tcol2"         // tab
msg = "say \"hello\""      // escaped quote
path = "C:\\Users\\brett"   // backslash
// Also supported: \r (carriage return), \0 (null byte)
```

---

## 2. Data Types

Six types: `string`, `number`, `bool`, `null`, `list`, `map`. All type-inferred.

```
// CORRECT
name = "hello"
count = 42
pi = 3.14
active = true
nothing = null
items = [1, 2, 3]
user = {name: "Ada", age: 30}

// WRONG — no type annotations on variables
string name = "hello"
int count = 42
```

**String interpolation:** `"text {expr}"` with double quotes only. Any expression is valid inside `{}`.

```
// CORRECT — simple variable
greeting = "Hello {name}"

// CORRECT — arithmetic expression
result = "Total: {a + b}"

// CORRECT — member access
info = "User: {user.name}"

// CORRECT — method call
msg = "Count: {items.len()}"

// CORRECT
greeting = "Hello {name}, you are {age} years old"

// WRONG — no backtick templates, no f-strings, no single quotes
greeting = `Hello ${name}`
greeting = f"Hello {name}"
greeting = 'hello'
```

**Multi-line strings:** use triple double quotes `"""..."""`. Content preserved as-is (including leading whitespace). Interpolation works inside.

```
// CORRECT — multi-line string
html = """
<div>
    <h1>Hello {name}</h1>
    <p>Welcome to Codong</p>
</div>
"""

// WRONG — no heredoc, no backtick multi-line
html = `
<div>hello</div>
`
```

**Map keys:** bare identifiers when valid. Keys with special characters must use double quotes.

```
// CORRECT — bare key (valid identifier)
config = {host: "localhost", port: 8080}

// CORRECT — quoted key (special characters)
headers = {"Content-Type": "application/json", "X-Request-ID": "abc123"}

// WRONG — bare key with special characters (Parser cannot parse this)
headers = {Content-Type: "application/json"}
```

**Map access:** dot notation and bracket notation both legal.

```
// CORRECT — dot access (key is valid identifier)
user = {name: "Ada", age: 30}
x = user.name        // "Ada"
x = user.age         // 30

// CORRECT — bracket access (any string key)
headers = {"Content-Type": "application/json"}
x = headers["Content-Type"]

// CORRECT — accessing non-existent key returns null (no error)
user = {name: "Ada"}
x = user.email       // null
x = user["phone"]    // null

// use m.has(key) to check existence
if user.has("email") {
    send_email(user.email)
}
```

**List access:** zero-indexed, negative indices supported.

```
// CORRECT — index access
items = [10, 20, 30]
first = items[0]     // 10
last = items[-1]     // 30 (negative = from end)
x = items[99]        // null (out-of-bounds returns null)

// WRONG — no slice syntax with ..
sub = items[1..3]
```

**null rules:**

```
// null equality
null == null         // true
null == false        // false (null is NOT false)
null == 0            // false

// falsy values: ONLY null and false
// truthy values: everything else, including 0, "", [], {}
if null { }          // does NOT enter block (falsy)
if false { }         // does NOT enter block (falsy)
if 0 { }             // ENTERS block (0 is truthy)
if "" { }            // ENTERS block ("" is truthy)
if [] { }            // ENTERS block ([] is truthy)
```

---

## 3. Variables & Assignment

`=` is the only assignment. `const` prevents rebinding only. Compound assignment allowed on non-const.

```
// CORRECT
x = 10
const MAX = 100
x += 5               // compound assignment
x -= 1
x *= 2
x /= 3

// WRONG — no var, let, :=
var x = 10
let x = 10
x := 10

// CORRECT — discard return value with _
_ = some_side_effect()

// WRONG — const cannot be rebound or use compound assignment
const MAX = 100
MAX += 1              // ERROR: cannot assign to const
MAX = 200             // ERROR: cannot assign to const

// CORRECT — const list/map can still be mutated via methods
const items = [1, 2, 3]
items.push(4)         // OK: items is now [1, 2, 3, 4] (mutation, not rebinding)

// WRONG — but cannot rebind const
const items = [1, 2, 3]
items = [4, 5]        // ERROR: cannot assign to const
```

---

## 4. Functions

`fn` is the only keyword. Arrow form for single expressions.

```
// CORRECT — standard function
fn add(a, b) {
    return a + b
}

// CORRECT — with type annotations (for agent.tool auto Schema)
fn search(query: string, limit: number) {
    return db.find("results", {q: query})
}

// CORRECT — default parameter values (= in definition)
fn create_user(name, role = "member") {
    return {name: name, role: role}
}

// CORRECT — arrow function (single expression)
double = fn(x) => x * 2

// CORRECT — anonymous function
handler = fn(req) { return {status: 200} }

// CORRECT — nested function (closure, captures outer scope)
fn make_counter() {
    count = 0
    fn increment() {
        count += 1
        return count
    }
    return increment
}
```

```
// WRONG — no function keyword
function add(a, b) { return a + b }

// WRONG — no def keyword
def add(a, b):
    return a + b

// WRONG — no bare arrow without fn
double = (x) => x * 2
double = x => x * 2

// WRONG — no lambda
square = lambda x: x * x
```

**Named arguments at call site:** use `:` (not `=`). Positional args first.

```
// CORRECT — named arguments at call site use :
server = web.serve(port: 8080)
response = llm.ask(model: "gpt-4o", prompt: "hello")
user = create_user("Ada", role: "admin")

// WRONG — named args before positional
server = web.serve(port: 8080, "localhost")

// IMPORTANT DISTINCTION:
// = in function DEFINITION means default value
// : at function CALL SITE means named argument
fn greet(name, greeting = "Hello") { }   // = is default
greet("Ada", greeting: "Hi")             // : is named arg
```

**export:** can modify `fn`, `const`, and `type`.

```
// CORRECT — export functions, constants, and types
export fn square(x) { return x * x }
export const PI = 3.14159
export type Point = { x: number, y: number }

// WRONG — cannot export bare variables
export x = 10
```

**Single return value only:**

```
// CORRECT — return one value; use map for multiple
fn divide(a, b) {
    if b == 0 {
        return {result: null, error: "division by zero"}
    }
    return {result: a / b, error: null}
}

// WRONG — no multiple return values
fn divide(a, b) {
    return a / b, null
}

// WRONG — no implicit return (last expression is NOT auto-returned)
fn add(a, b) {
    a + b           // this does nothing, function returns null
}

// CORRECT — explicit return required
fn add(a, b) {
    return a + b
}
```

---

## 5. Control Flow

```
// CORRECT — if / else if / else
if x > 0 {
    print("positive")
} else if x == 0 {
    print("zero")
} else {
    print("negative")
}

// WRONG — no parentheses around condition
if (x > 0) {
    print("positive")
}

// CORRECT — for iteration
for item in items {
    print(item)
}

// CORRECT — for range (range is a built-in function, not a keyword)
for i in range(0, 10) {
    print(i)
}

// WRONG — no C-style for
for (i = 0; i < 10; i++) {
    print(i)
}

// CORRECT — while
while running {
    data = poll()
}

// WRONG — no do-while
do {
    data = poll()
} while (running)

// CORRECT — match (=> for single expressions)
match status {
    200 => print("ok")
    404 => print("not found")
    _ => print("other: {status}")
}

// WRONG — no switch/case
switch (status) {
    case 200: print("ok")
}
```

**match arms:** only literals (`number`, `string`, `bool`, `null`) and `_` wildcard. No variable matching.

```
// CORRECT — literals and _ only
match status {
    200 => print("ok")
    "error" => print("failed")
    null => print("no status")
    _ => print("other")
}

// WRONG — cannot match against variables
expected = 200
match status {
    expected => print("matched")  // ERROR: this is not valid
}

// CORRECT — use if/else for variable comparison
if status == expected {
    print("matched")
}

// WRONG — no string interpolation in match arms
match msg {
    "error: {code}" => print("caught")  // ERROR: no interpolation in match
}

// CORRECT — match with plain string literals only
match msg {
    "not found" => print("404")
    "forbidden" => print("403")
    _ => print("other")
}
```

**for...in only works on list.** To iterate a map, use `.keys()`, `.values()`, or `.entries()`.

```
// CORRECT — iterate list
for item in [1, 2, 3] {
    print(item)
}

// CORRECT — iterate map keys
m = {a: 1, b: 2}
for key in m.keys() {
    print("{key}: {m[key]}")
}

// CORRECT — iterate map entries
for entry in m.entries() {
    print("{entry[0]}: {entry[1]}")
}

// WRONG — cannot for...in a map directly
for item in m {
    print(item)
}
```

`_` in `match` is the wildcard (matches anything).

---

## 6. Type System

```
// CORRECT — type declaration
type User = {
    name: string,
    age: number,
    email: string,
}

// CORRECT — interface
interface Searchable {
    fn search(query: string) => list
}

// WRONG — no class keyword
class User {
    constructor(name) { this.name = name }
}

// WRONG — no struct keyword
struct User {
    name string
}
```

**Interface:** structural typing — any value with matching methods automatically satisfies the interface. No `implements` keyword needed.

```
// CORRECT — structural typing (duck typing)
interface Searchable {
    fn search(query: string) => list
}

fn process(s: Searchable) {
    results = s.search("test")  // works if s has a search method
}

// WRONG — no explicit implements
class MySearcher implements Searchable { }
```

**Type conversion:** use built-in functions `int()`, `float()`, `str()`, `bool()`.

```
// CORRECT
s = str(42)          // "42"
n = int("42")        // 42
f = float("3.14")    // 3.14
b = bool(1)          // true

// ALSO CORRECT (legacy aliases)
s = to_string(42)
n = to_number("42")

// WRONG — no casting syntax
s = (string)42
s = 42 as string
```

**Power operator:** `**`

```
// CORRECT
result = 2**10      // 1024
cube   = x**3

// WRONG — no ^ or pow()
result = 2^10
result = pow(2, 10)
```

**Global utility functions:**

```
// CORRECT
n = len([1, 2, 3])           // 3 — global alias for .len()
sorted = sort([3, 1, 2])     // [1, 2, 3]
sorted = sort([3, 1, 2], fn(a, b){ return a > b })  // descending

// ALSO CORRECT — method form
n = [1, 2, 3].len()
```

---

## 7. Module System

**Built-in modules:** `web`, `db`, `http`, `llm`, `fs`, `json`, `env`, `time`, `redis`, `image`, `oauth`, `agent`, `cloud`, `queue`, `cron`, `error` — no import needed.

```
// CORRECT — built-in modules used directly
server = web.serve(port: 8080)
conn = db.connect("postgres://localhost/mydb")
response = llm.ask(model: "gpt-4o", prompt: "hello")

// WRONG — do not import built-in modules
import web
from web import serve
```

**Custom modules:**

```
// CORRECT — export (fn, const, type all allowed)
// math_utils.cod
export fn square(x) { return x * x }
export const PI = 3.14159
export type Point = { x: number, y: number }

// CORRECT — import
import { square, PI, Point } from "./math_utils.cod"

// WRONG — no require, no default export
const m = require("./math_utils")
export default fn square(x) { return x * x }
```

**Third-party packages:** use `@namespace` scoped names.

```
// CORRECT — import from @namespace package
import { verify } from "@codong/jwt"
import { hash } from "@alice/crypto"

// WRONG — no bare package names for third-party (prevents name squatting)
import { hash } from "crypto"
```

- Official packages: no scope (e.g., `codong-jwt`)
- Third-party: must use `@namespace` (e.g., `@alice/utils`)
- `codong.lock` pins all dependencies to SHA-256 hash for 100% reproducible builds

---

## 8. Concurrency

```
// CORRECT — goroutine
go fn() {
    data = fetch_data()
    ch <- data
}()

// WRONG — no async/await
async fn fetch() {
    data = await get_data()
}

// CORRECT — channel
ch = channel()
ch <- "message"       // send: space before <-
msg = <-ch            // receive: no space, <-ch is prefix operator

ch = channel(size: 10) // buffered

// WRONG — no channel methods
ch.send("message")
msg = ch.receive()

// CORRECT — select uses { } blocks (NOT => arrows)
select {
    msg = <-ch1 {
        handle(msg)
    }
    msg = <-ch2 {
        process(msg)
    }
}

// CORRECT — select without assignment (discard received value)
select {
    <-done {
        break
    }
}

// WRONG — select with => is ambiguous, do NOT use
select {
    msg = <- ch1 => handle(msg)
}
```

**Channel syntax summary:**
- Send: `ch <- value` (space before `<-`, binary operator)
- Receive: `<-ch` (no space, prefix operator)
- This distinction is unambiguous for the Parser.

---

## 9. Error Handling

All errors are structured JSON: `code`, `message`, `fix`, `retry`.

```
// CORRECT — create error
err = error.new("E1001", "invalid type", fix: "use number instead")

// WRONG — no new Error() or raise
err = new Error("invalid type")
raise Exception("invalid type")

// CORRECT — try/catch
try {
    result = db.find("users", {id: 1})
} catch err {
    print(err.code)    // "E2001_NOT_FOUND"
    print(err.fix)     // "check if table exists"
}

// CORRECT — ? operator propagates errors (postfix)
data = db.find("users", {id: 1})?

// CORRECT — wrap error with context
wrapped = error.wrap(err, "while loading user profile")

// CORRECT — compact format (saves 39% tokens)
error.set_format("compact")
// output: err_code:E2001_NOT_FOUND|src:db.find|fix:run db.migrate()|retry:false
```

**`?` postfix operator semantics:** if the expression evaluates to an error, immediately return that error to the caller. Otherwise, evaluate to the expression's value unchanged. No optional chaining `?.`.

**Error identity:** an error object is created exclusively via `error.new()` or `error.wrap()` and carries an internal type tag. Regular maps with `code` fields are NOT errors.

```
// CORRECT — only error.new() creates real errors
err = error.new("E1001", "bad input")
data = might_fail()?   // ? only triggers on real error objects

// This is just a normal map, ? will NOT propagate it
result = {code: "SUCCESS", message: "ok"}
x = result?            // x = {code: "SUCCESS", message: "ok"} (not an error)

// WRONG — Codong does NOT have optional chaining
x = user?.address?.city

// CORRECT — check null explicitly
if user != null {
    if user.address != null {
        city = user.address.city
    }
}
```

---

## 10. Built-in Functions

Globally available without import. These are functions, NOT keywords.

| Function | Returns | Description |
|----------|---------|-------------|
| `print(value)` | null | standard output (the ONLY output function) |
| `type_of(x)` | string | returns type name |
| `to_string(x)` | string | convert to string |
| `to_number(x)` | number/null | convert to number, null if invalid |
| `to_bool(x)` | bool | convert to bool |
| `range(start, end)` | list | integers from start to end-1 |

```
// CORRECT — print is the standard output function
print("Hello World")
print("count: {count}")

// WRONG — log() is NOT a Codong function
log("Hello World")
console.log("Hello World")
fmt.Println("Hello World")

// print() takes a single argument. Use interpolation for multiple values:
// CORRECT
print("{a} {b} {c}")

// WRONG — no multiple arguments
print(a, b, c)

// CORRECT — use .len() method for length (no global len() function)
items = [1, 2, 3]
n = items.len()       // 3
s = "hello"
n = s.len()           // 5
m = {a: 1, b: 2}
n = m.len()           // 2

// WRONG — no global len() function
n = len(items)

// CORRECT — range is a built-in function (not a keyword)
nums = range(0, 5)    // [0, 1, 2, 3, 4]
for i in range(1, 4) {
    print(i)           // 1, 2, 3
}
```

---

## 11. Built-in Type Methods

**Mutability rule:**
- **Strings** are immutable — all methods return new strings, original unchanged.
- **Lists** are mutable — `push`, `pop`, `sort`, `reverse`, `shift`, `unshift` modify in place and return `self` for chaining. **Maps:** only `delete` mutates in place; `merge`, `filter`, `map_values` all return new maps.

```
// CORRECT — list mutation (modifies original)
items = [1, 2, 3]
items.push(4)          // items is now [1, 2, 3, 4], no reassignment needed
last = items.pop()     // last = 4, items is now [1, 2, 3]

// CORRECT — chaining mutating methods (returns self)
items.push(4).push(5)  // items is now [1, 2, 3, 4, 5]

// PITFALL — sort() mutates in place (unlike filter/map)
items = [3, 1, 2]
sorted = items.sort()  // items is now [1, 2, 3], sorted is same reference
// To preserve original, copy first:
original = [3, 1, 2]
copy = original.slice(0)
copy.sort()            // copy is [1, 2, 3], original still [3, 1, 2]

// CORRECT — non-mutating methods return NEW values (original unchanged)
items = [1, 2, 3, 4]
evens = items.filter(fn(x) => x % 2 == 0)  // evens = [2, 4], items unchanged

// CORRECT — string is immutable
s = "hello"
upper = s.upper()      // upper = "HELLO", s is still "hello"
```

### string (21 methods, all return new strings)

```
s = "Hello World"
s.len()              // 11
s.upper()            // "HELLO WORLD" (s unchanged)
s.lower()            // "hello world" (s unchanged)
s.trim()             // removes whitespace
s.trim_start()       // removes leading whitespace
s.trim_end()         // removes trailing whitespace
s.split(" ")         // ["Hello", "World"]
s.contains("World")  // true
s.starts_with("He")  // true
s.ends_with("ld")    // true
s.replace("World", "Codong")  // "Hello Codong" (s unchanged)
s.index_of("World")  // 6
s.index("World")     // 6 (alias for index_of)
s.slice(0, 5)        // "Hello"
s.repeat(2)          // "Hello WorldHello World"
s.reverse()          // "dlroW olleH"
s.format(42)         // "Hello World" (positional: "1+2={0}".format(3) → "1+2=3")
s.count("l")         // 3 (occurrences of substring)
s.pad_start(15, "0") // "0000Hello World"
s.pad_end(15, ".")   // "Hello World...."
"42".to_number()     // 42
"true".to_bool()     // true
"abc123".match("[0-9]+")  // ["123"]
```

### list (20 methods)

Mutating: `push`, `pop`, `shift`, `unshift`, `sort`, `reverse` — modify original, return self (or removed item for pop/shift).
Non-mutating: all others — return new values.

```
// Mutating methods (modify original)
l = [3, 1, 2]
l.push(4)            // l is now [3, 1, 2, 4], returns l
l.pop()              // returns 4, l is now [3, 1, 2]
l.shift()            // returns 3, l is now [1, 2]
l.unshift(0)         // l is now [0, 1, 2], returns l
l.sort()             // l is now [0, 1, 2] (sorted in place), returns l
l.reverse()          // l is now [2, 1, 0] (reversed in place), returns l

// Non-mutating methods (return new values, original unchanged)
l = [3, 1, 2]
l.len()              // 3
l.slice(0, 2)        // [3, 1] (new list, l unchanged)
l.map(fn(x) => x * 2)     // [6, 2, 4] (new list)
l.filter(fn(x) => x > 1)  // [3, 2] (new list)
l.reduce(fn(a, b) => a + b, 0)  // 6
l.find(fn(x) => x > 2)    // 3
l.find_index(fn(x) => x > 2)  // 0
l.contains(1)        // true
l.index_of(1)        // 1
l.flat()             // deep flattens nested lists recursively (new list)
                     // [1, [2, [3, 4]]] → [1, 2, 3, 4]
l.flatten()          // alias for flat()
l.count(2)           // number of occurrences of element 2 in list
l.chunk(2)           // split into sub-lists of size 2: [[3,1],[2]]
l.zip([4,5,6])       // [[3,4],[1,5],[2,6]]
l.unique()           // deduplicates (new list)
l.join("-")          // "3-1-2" (returns string)
l.first()            // 3
l.last()             // 2
```

### map (11 methods)

Mutating: `delete` only — modifies original, returns self.
Non-mutating: all others — return new values.

```
m = {a: 1, b: 2, c: 3}

// Non-mutating
m.len()              // 3
m.keys()             // ["a", "b", "c"]
m.values()           // [1, 2, 3]
m.entries()          // [["a",1], ["b",2], ["c",3]]
m.has("a")           // true
m.get("x", 0)        // 0 (default)
m.map_values(fn(v) => v * 10)   // {a: 10, b: 20, c: 30} (new map)
m.filter(fn(v) => v > 1)       // {b: 2, c: 3} (new map)
m.merge({d: 4})      // {a: 1, b: 2, c: 3, d: 4} (new map, m unchanged)
m.from_entries([{k:"x",v:1},{k:"y",v:2}])  // {x: 1, y: 2}

// Map equality
{a:1} == {a:1}       // true (value-based comparison)
{a:1} == {a:2}       // false

// Mutating
m.delete("a")        // m is now {b: 2, c: 3}, returns m

// CORRECT — safe merge pattern (creates new map, originals untouched)
config = defaults.merge(user_config)   // defaults is NOT modified
```

Note: use `m.map_values(fn)` to transform values (not `m.map(fn)`, avoids confusion with type name).

---

## 12. Operator Precedence (highest to lowest)

| # | Operators | Description |
|---|-----------|-------------|
| 1 | `()` `[]` `.` `?` | grouping, index, member, error propagation (postfix) |
| 2 | `!` `-`(unary) | not, negate |
| 3 | `*` `/` `%` | multiply, divide, modulo |
| 4 | `+` `-` | add, subtract |
| 5 | `<` `>` `<=` `>=` | comparison |
| 6 | `==` `!=` | equality |
| 7 | `&&` | logical and |
| 8 | `\|\|` | logical or |
| 9 | `<-` | channel send/receive |
| 10 | `=` `+=` `-=` `*=` `/=` | assignment |

```
// WRONG — no === or !== or ternary
if x === 10 { }
result = x > 0 ? "yes" : "no"
```

---

## 13. Keywords (23 total)

```
fn       return   if       else     for      while    match
break    continue const    import   export   try      catch
go       select   interface type    null     true     false
in       _
```

`_` is a keyword: match wildcard and discard marker (`_ = side_effect()`).

**NOT keywords** (built-in modules/functions, can appear in expressions):

```
// These are NOT keywords — they are built-in modules or functions:
error      // built-in module: error.new(), error.wrap()
channel    // built-in function: channel(), channel(size: 10)
range      // built-in function: range(0, 10)
print      // built-in function: print("hello")
type_of    // built-in function: type_of(x)
to_string  // built-in function: to_string(42)
to_number  // built-in function: to_number("42")
to_bool    // built-in function: to_bool("true")

// CORRECT — error and channel used as expressions
err = error.new("E1001", "msg")   // error is a module, not keyword
ch = channel()                     // channel is a function, not keyword

// This is why they CANNOT be keywords:
// Keywords cannot appear as the left side of . access or be called as functions
// error.new() requires error to be an identifier, not a keyword
// channel() requires channel to be a callable, not a keyword
```

```
// WRONG — these are NOT Codong keywords either
var  let  class  struct  async  await  def  function  switch
case throw new   this    self   lambda yield require  bridge  use
```

---

## 14. Mandatory Code Style

```
// CORRECT — 4 space indent, snake_case, double quotes, same-line brace
fn calculate_total(items) {
    total = 0
    for item in items {
        total = total + item.price
    }
    return total
}

// WRONG — tabs
fn calculate_total(items) {
	total = 0
}

// WRONG — camelCase
fn calculateTotal(items) {
    myVar = 10
}

// WRONG — single quotes
name = 'hello'

// WRONG — brace on new line
fn add(a, b)
{
    return a + b
}

// CORRECT — trailing comma in multi-line
config = {
    host: "localhost",
    port: 8080,
    debug: true,
}

// WRONG — no trailing comma in multi-line
config = {
    host: "localhost",
    port: 8080,
    debug: true
}
```

Type names use `PascalCase`:

```
// CORRECT
type UserProfile = { name: string, age: number }

// WRONG
type user_profile = { name: string, age: number }
```

---

## 15. Go Bridge Extension Protocol

Go Bridge lets human architects wrap Go libraries for AI to call. AI only calls the registered function name.

```
// CORRECT — call registered bridge function
result = wechat_pay(amount: 99.9, order_id: "ORD001")

// WRONG — AI must NOT implement Go internals
import "github.com/wechatpay-apiv3/wechatpay-go"
```

### Registration in codong.toml

```toml
[bridge]
wechat_pay = { fn = "bridge.WechatPay", permissions = ["net:outbound"] }
pdf_render = { fn = "bridge.RenderPDF", permissions = ["fs:write:/tmp/codong-sandbox"] }
hash_md5   = { fn = "bridge.HashMD5", permissions = [] }
```

### Permission Rules

| Permission | Format | Meaning |
|------------|--------|---------|
| No I/O | `[]` | pure computation |
| Network | `["net:outbound"]` | HTTP requests allowed |
| Read files | `["fs:read:<path>"]` | read specific directory |
| Write files | `["fs:write:<path>"]` | write specific directory |

**Prohibited:** `os.Exit`, `syscall`, `os/exec`, `net.Listen`, host root filesystem access.

```
// CORRECT — handle bridge errors via map
result = pdf_render(html: content, output: "report.pdf")
if result.error {
    print("render failed: {result.error}")
}

// Bridge error convention:
// On failure:  return {error: "description", ...}
// On success:  error field must be null or absent
//              (accessing absent key returns null, which is falsy)
// WRONG — never use empty string for success ("" is truthy in Codong!)
//   return {error: ""}   // BAD: "" is truthy, triggers error branch
```

---

## 16. Infrastructure Modules (fs, json, env, time, http)

### fs — File System

```
// CORRECT
content = fs.read("./config.txt")      // returns string, or null if missing
fs.write("./out.txt", "hello\n")
fs.append("./log.txt", "line\n")
fs.delete("./tmp.txt")
fs.copy("./src.txt", "./dst.txt")
fs.move("./old.txt", "./new.txt")
exists = fs.exists("./data.txt")       // bool
files = fs.list("./uploads")           // [{name, path, is_dir, size}, ...]
fs.mkdir("./data/cache")
fs.rmdir("./old_dir")
data = fs.read_json("./config.json")   // returns map/list
fs.write_json("./out.json", data)
lines = fs.read_lines("./data.csv")    // returns list of strings
fs.write_lines("./out.csv", lines)

// CORRECT — fs.read returns null (not error) for missing files
content = fs.read("./maybe.txt")
if content == null {
    content = "default"
}

// WRONG — do not use ? on fs.read (it returns null, not error)
content = fs.read("./maybe.txt")?   // WRONG: null is not an error
```

### json — JSON

```
// CORRECT
data = json.parse('{"key": "value"}')
str = json.stringify(data)
str = json.stringify(data, indent: 2)     // pretty print
ok = json.valid(str)                      // bool
merged = json.merge(map_a, map_b)
city = json.get(data, "user.address.city")     // dot-path access
data = json.set(data, "user.address.city", "NYC")
flat = json.flatten(data)                 // {"a.b.c": value}
nested = json.unflatten(flat)

// WRONG — do not use single quotes for JSON strings in Codong
data = json.parse('{"key": "value"}')    // the JSON itself uses ", fine
// WRONG — json.parse expects a string argument
data = json.parse(42)
```

### env — Environment Variables

```
// CORRECT
db_url = env.get("DATABASE_URL")              // null if not set
host = env.get("HOST", "localhost")           // with default
secret = env.require("JWT_SECRET")            // throws E7001 if not set
has_debug = env.has("DEBUG")                  // bool
all_vars = env.all()                          // map of all env vars
env.load("./.env")                            // load .env file

// WRONG — do not hardcode secrets; use env.require
secret = "hardcoded_secret_123"
```

### time — Date and Time

```
// CORRECT
ts = time.now()                               // Unix timestamp (number)
iso = time.now_iso()                          // "2026-03-26T10:30:00Z"
time.sleep(1000)                              // sleep 1 second (1000ms)
formatted = time.format(ts, "datetime")       // "2026-03-26 10:30:00"
formatted = time.format(ts, "date")           // "2026-03-26"
formatted = time.format(ts, "iso")            // ISO 8601
formatted = time.format(ts, "rfc2822")        // RFC 2822
parsed = time.parse("2026-03-26T10:30:00Z")   // returns Unix timestamp
delta = time.diff(ts1, ts2)                   // seconds between (absolute)
elapsed = time.since(ts)                      // seconds since ts
remaining = time.until(ts)                    // seconds until ts
future = time.add(ts, "1h")                   // add 1 hour
future = time.add(ts, "30m")                  // add 30 minutes
future = time.add(ts, "7d")                   // add 7 days
is_past = time.is_before(ts, time.now())
is_future = time.is_after(ts, time.now())
today_start = time.today_start()              // midnight today (Unix ts)
today_end = time.today_end()                  // 23:59:59 today (Unix ts)

// WRONG — time.sleep takes milliseconds, not seconds
time.sleep(1)     // WRONG: sleeps 1ms, not 1 second
time.sleep(1000)  // CORRECT: sleeps 1 second
```

### http — HTTP Client

```
// CORRECT — GET request
resp = http.get("https://api.example.com/users")
resp = http.get("https://api.example.com/users", headers: {"Authorization": "Bearer {token}"})

// CORRECT — POST with JSON body
resp = http.post("https://api.example.com/users", {name: "Ada", role: "admin"})

// CORRECT — other methods
resp = http.put("https://api.example.com/users/1", {name: "Ada"})
resp = http.patch("https://api.example.com/users/1", {name: "Ada"})
resp = http.delete("https://api.example.com/users/1")
resp = http.request("GET", "https://api.example.com/users", headers: {"X-Key": "value"})

// Response fields:
// resp.status    → number (200, 404, ...)
// resp.ok        → bool (status < 400)
// resp.body      → string (raw response body)
// resp.json      → parsed body (map/list), or null if not JSON
// resp.headers   → map
// resp.error     → CodongError or null

// CORRECT — propagate HTTP errors with ?
resp = http.get("https://api.example.com/data")?
// On 4xx: throws E3003_HTTP_4XX
// On 5xx: throws E3004_HTTP_5XX
// On timeout: throws E3001_HTTP_TIMEOUT

// CORRECT — handle specific error codes
try {
    resp = http.get("https://api.example.com/data")?
    print(resp.json)
} catch e {
    match e.code {
        "E3003_HTTP_4XX" => print("not found or forbidden")
        "E3001_HTTP_TIMEOUT" => print("request timed out")
        _ => print("other error: {e.code}")
    }
}

// WRONG — do not check resp.ok manually when using ?
resp = http.get(url)?
if resp.ok {        // WRONG: ? already guaranteed ok at this point
    ...
}
```

---

## 17. Redis Module

```
// CORRECT — connect (required before any redis operation)
redis.connect("redis://localhost:6379")
redis.connect("redis://localhost:6379", name: "session")   // named instance
redis.using("session")                                     // switch instance

// WRONG — no connect() before use
redis.set("key", "value")   // WRONG: not connected
```

**Key-Value:**

```
// CORRECT
redis.set("user:1", "Ada")
redis.set("user:1", "Ada", ttl: 3600)        // expires in 1 hour
val = redis.get("user:1")                    // "Ada" or null
redis.delete("user:1")
exists = redis.exists("user:1")              // bool
redis.expire("user:1", 600)                  // set TTL on existing key
ttl = redis.ttl("user:1")                   // remaining seconds
redis.incr("counter")                        // increment by 1
redis.incr_by("counter", 5)
redis.decr("counter")
```

**Caching (singleflight, no thundering herd):**

```
// CORRECT — loader called only on cache miss; concurrent misses wait for one load
result = redis.cache("user:1:profile", ttl: 300, loader: fn() {
    return db.find("users", {id: 1})
})

// CORRECT — invalidate
redis.invalidate("user:1:profile")
redis.invalidate_pattern("user:1:*")         // glob pattern

// WRONG — do not manually get/set for caching (misses singleflight)
val = redis.get("key")
if val == null {
    val = expensive_load()
    redis.set("key", val, ttl: 300)
}
```

**Distributed Lock:**

```
// CORRECT — acquire lock with TTL
lock = redis.lock("order:{order_id}", ttl: 30)
try {
    process_order(order_id)
} catch e {
    print(e.code)    // E8004_LOCK_TIMEOUT if lock not acquired
} finally {
    lock.release()
}

// WRONG — no manual lock pattern
redis.set("lock:key", "1")   // WRONG: not atomic, not safe
```

**Sorted Sets (Leaderboards):**

```
// CORRECT
redis.zadd("leaderboard", {alice: 100, bob: 200, carol: 150})
top3 = redis.zrevrange("leaderboard", 0, 2)                   // ["bob", "carol", "alice"]
top3_scores = redis.zrevrange("leaderboard", 0, 2, with_scores: true)
// returns [{member: "bob", score: 200}, ...]
rank = redis.zrevrank("leaderboard", "bob")                    // 0 (top)
score = redis.zscore("leaderboard", "alice")                   // 100
redis.zincrby("leaderboard", "alice", 50)                     // alice score: 150

// WRONG — zadd expects a map of member->score
redis.zadd("leaderboard", ["alice", 100])   // WRONG: must be map
```

**Rate Limiter (sliding window):**

```
// CORRECT
limiter = redis.rate_limiter("api:{user_id}", requests: 100, window: 60)
// window is in seconds
if limiter.allow() {
    handle_request()
} else {
    print("rate limit exceeded, reset at: {limiter.reset_at()}")
    print("remaining: {limiter.remaining()}")
}

// WRONG — do not implement rate limiting manually with incr/expire
```

**Pub/Sub:**

```
// CORRECT — publish
redis.publish("notifications", json.stringify({user: "Ada", msg: "hello"}))

// CORRECT — subscribe (blocking)
redis.subscribe("notifications", fn(msg) {
    data = json.parse(msg)
    print("received: {data.msg}")
})

// WRONG — subscribe does not return a value
sub = redis.subscribe("ch", fn(msg) { })   // WRONG: subscribe is blocking
```

---

## 18. Image Module

```
// CORRECT — open from file
img = image.open("./photo.jpg")

// CORRECT — open from bytes (e.g., upload)
img = image.from_bytes(req.file_bytes)

// Info without loading pixels
info = image.info("./photo.jpg")    // {width, height, format, size}
exif = image.read_exif("./photo.jpg")

// Dimensions
w = img.width()
h = img.height()
```

**Resize / Crop:**

```
// CORRECT — resize (exact, may distort)
img.resize(800, 600)

// CORRECT — fit (letterbox, no distortion)
img.fit(800, 600)

// CORRECT — cover (crop edges, no distortion, fills box)
img.cover(800, 600)

// CORRECT — crop
img.crop(100, 100, 400, 300)    // x, y, width, height
img.crop_center(400, 300)       // center crop
img.smart_crop(400, 300)        // content-aware (face/subject detection)
img.thumbnail(200)              // fit within 200x200

// CORRECT — extend canvas
img.extend(1000, 1000, color: "#ffffff")    // add white padding

// WRONG — resize modifies the image object in place, do not expect a new object
original = image.open("./photo.jpg")
small = original.resize(200, 200)   // NOTE: original is also modified
// To preserve original, reload:
original = image.open("./photo.jpg")
img = image.open("./photo.jpg")
img.resize(200, 200)
```

**Transform:**

```
img.rotate(90)          // rotate 90 degrees clockwise
img.auto_rotate()       // use EXIF orientation
img.flip_horizontal()
img.flip_vertical()
```

**Filters:**

```
img.to_grayscale()
img.blur(2.0)           // gaussian blur, sigma value
img.sharpen(1.0)
img.brightness(0.1)     // +10% brightness (-1.0 to 1.0)
img.contrast(-0.1)      // -10% contrast (-1.0 to 1.0)
img.gamma(1.2)
img.saturation(0.5)     // increase saturation
img.tint("#ff6600")     // orange tint
img.to_rgb()            // convert to RGB colorspace
img.strip_metadata()    // remove EXIF/ICC data
img.optimize(quality: 80)    // set JPEG/WebP quality (1-100)
```

**Watermark:**

```
// CORRECT — text watermark
img.watermark_text("© 2026", position: "bottom_right", color: "#ffffff", size: 24)
// positions: "top_left", "top_right", "bottom_left", "bottom_right", "center"

// CORRECT — image watermark
logo = image.open("./logo.png")
img.watermark(logo, position: "bottom_right")
img.watermark_tile(logo)                   // tiled across whole image
img.watermark_image(logo, 50, 50)          // at exact x, y coordinates
```

**Output:**

```
// CORRECT — save to file (format from extension)
img.save("./output.jpg")
img.save("./output.png")
img.save("./output.webp")

// CORRECT — to bytes (for HTTP response or further processing)
bytes = img.to_bytes("jpeg")         // format: "jpeg", "png", "gif", "webp"
b64 = img.to_base64("jpeg")          // base64 string

// WRONG — unknown format
img.save("./output.bmp")   // WRONG: bmp not supported; use jpg/png/gif/webp
```

---

## 19. OAuth Module

```
// CORRECT — configure OAuth provider (call once at startup)
oauth.provider("github", {
    client_id: env.require("GITHUB_CLIENT_ID"),
    client_secret: env.require("GITHUB_CLIENT_SECRET"),
    redirect_uri: "https://example.com/auth/callback",
})
// providers: "github", "google", "microsoft"

// CORRECT — configure JWT (call once at startup)
oauth.configure_jwt(
    secret: env.require("JWT_SECRET"),
    algorithm: "HS256",
    expires_in: 3600,
)
```

**OAuth Flow:**

```
// Step 1: redirect user
url = oauth.authorization_url("github")
// With PKCE (recommended):
pkce = oauth.generate_pkce()          // {verifier, challenge, method}
state = oauth.generate_state()        // random CSRF state
url = oauth.authorization_url("github", state: state, pkce: pkce)

// Step 2: exchange code (in callback handler)
token_data = oauth.exchange_code("github", req.query.code)?
profile = oauth.get_profile("github", token_data.access_token)?
// profile has: {id, name, email, avatar_url, ...} (provider-specific)

// WRONG — do not exchange code twice (codes are single-use)
token1 = oauth.exchange_code("github", code)
token2 = oauth.exchange_code("github", code)   // WRONG: code already used
```

**JWT:**

```
// CORRECT — sign JWT
token = oauth.sign_jwt({user_id: user.id, role: user.role})
refresh = oauth.sign_refresh_token({user_id: user.id})

// CORRECT — verify JWT (throws E9001_INVALID_TOKEN on failure)
payload = oauth.verify_jwt(token)?
print(payload.user_id)
print(payload.role)

// CORRECT — decode without verification (for debugging only)
decoded = oauth.decode_jwt(token)

// CORRECT — revoke token
oauth.revoke_jwt(token)
is_bad = oauth.is_revoked(token)     // bool

// WRONG — do not use verify_jwt without ? or try/catch
payload = oauth.verify_jwt(expired_token)   // WRONG: panics on invalid token
// CORRECT:
try {
    payload = oauth.verify_jwt(token)?
} catch e {
    print(e.code)    // E9001_INVALID_TOKEN or E9002_TOKEN_EXPIRED
}
```

**RBAC:**

```
// CORRECT — define roles once at startup
oauth.define_roles({
    admin: ["read", "write", "delete", "manage"],
    editor: ["read", "write"],
    viewer: ["read"],
})

// CORRECT — check permission (returns bool)
if oauth.has_permission(req.user, "write") {
    save_document(doc)
}

// CORRECT — enforce permission (throws E9005_FORBIDDEN)
oauth.check_permission(req.user, "delete")?

// user must have a .role field
user = {id: 1, role: "editor"}
oauth.has_permission(user, "write")   // true
oauth.has_permission(user, "delete")  // false

// WRONG — has_permission requires a user map with .role field
oauth.has_permission("admin", "write")   // WRONG: string is not a user map
```

**Middleware pattern:**

```
// CORRECT — JWT auth middleware
server.use(fn(req, next) {
    auth = req.headers["Authorization"]
    if auth == null {
        return {status: 401, body: json.stringify({error: "unauthorized"})}
    }
    token = auth.replace("Bearer ", "")
    try {
        req.user = oauth.verify_jwt(token)?
    } catch e {
        return {status: 401, body: json.stringify({error: e.message})}
    }
    return next(req)
})

// WRONG — do not call verify_jwt without handling the error
server.use(fn(req, next) {
    req.user = oauth.verify_jwt(req.headers["Authorization"])   // WRONG
    return next(req)
})
```

---

## 20. Error Handling — Additional Rules

```
// CORRECT — break/continue work inside try/catch
for i in range(0, 10) {
    try {
        result = might_fail(i)?
    } catch e {
        if e.retry {
            continue    // retry is allowed: re-enter loop
        }
        break           // permanent error: exit loop
    }
}

// CORRECT — check retry field before retrying
try {
    result = db.query("INSERT INTO ...", [])?
} catch e {
    print(e.code)      // "E2002_QUERY_FAILED"
    print(e.retry)     // false (unique constraint: don't retry)
    print(e.message)
    print(e.fix)
}

// CORRECT — http errors have retry field too
try {
    resp = http.get("https://api.example.com/data")?
} catch e {
    if e.retry {
        // E3001_HTTP_TIMEOUT or 5xx: retryable
    } else {
        // E3003_HTTP_4XX: permanent (bad request, auth, not found)
    }
}

// WRONG — do not use ? outside of a function or try block at top level
// ? at top-level terminates the program with an error (no recovery)
data = db.find("users", {id: 999})?   // at top level: program exits on error
// CORRECT at top level: use try/catch
try {
    data = db.find("users", {id: 999})?
} catch e {
    print("not found: {e.code}")
}
```

---

## Quick Reference: Unique Syntax Rules

| Scenario | Codong Way | NOT Allowed |
|----------|-----------|-------------|
| Variable | `x = 10` | `var x`, `let x`, `x := 10` |
| Function | `fn add(a,b) {}` | `function`, `def` |
| Default param | `fn(a, b = 10)` | `fn(a, b: 10)` in definition |
| Named arg | `f(a, key: val)` | `f(a, key=val)` at call site |
| String | `"hello {x}"` | `'hello'`, `` `hello ${x}` ``, `f"hello {x}"` |
| Multi-line | `"""..."""` | backtick blocks, heredoc |
| Output | `print(x)` | `log(x)`, `console.log(x)` |
| Map access | `m.key`, `m["key"]` | only `m.get()` |
| List access | `l[0]`, `l[-1]` | `l.at(0)` |
| HTTP server | `web.serve(port: 8080)` | `express()`, `http.ListenAndServe()` |
| HTTP client | `http.get(url)` | `fetch(url)`, `requests.get(url)` |
| DB connect | `db.connect(url)` | `new Pool()`, `createConnection()` |
| File read | `fs.read(path)` | `os.ReadFile()`, `fs.readFileSync()` |
| Cache | `redis.cache(key, ttl: n, loader: fn)` | manual get/set |
| Image resize | `img.fit(w, h)` | `sharp().resize()`, `PIL.resize()` |
| JWT sign | `oauth.sign_jwt(payload)` | `jwt.sign(payload, secret)` |
| Rate limit | `redis.rate_limiter(key, requests: n, window: s)` | manual incr/expire |
| LLM call | `llm.ask(model, prompt)` | `openai.chat.completions.create()` |
| Error | `error.new(code, msg)` | `new Error()`, `raise Exception()` |
| Goroutine | `go fn() {}()` | `async`, `go func(){}()` |
| Channel send | `ch <- value` | `ch.send(value)` |
| Channel recv | `msg = <-ch` | `msg = <- ch`, `ch.receive()` |
| Arrow fn | `fn(x) => x * 2` | `x => x*2`, `lambda x: x*2` |
| Error prop | `expr?` | `expr?.field` (no optional chain) |
| Select arm | `<-ch { body }` | `<-ch => body` |
| Match arm | literals + `_` only | variable matching |
| Iterate map | `for k in m.keys()` | `for k in m` |
| Multiple returns | `return {a: 1, b: 2}` | `return a, b` |
| Length | `x.len()` | `len(x)` |

---

CODONG | codong.org | MIT License | AI Edition
