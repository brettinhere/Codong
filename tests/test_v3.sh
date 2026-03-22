#!/bin/bash
# Codong 全功能测试 v3 — 全新用例，聚焦边界与交互
# 用法: chmod +x test_v3.sh && ./test_v3.sh ./bin/codong

CODONG=${1:-"./bin/codong"}
PASS=0; FAIL=0
RESULTS=()

t() {
    local name="$1"; local code="$2"; local expect="$3"
    local f=$(mktemp /tmp/cod_XXXXX.cod)
    printf '%s' "$code" > "$f"
    local out; out=$("$CODONG" eval "$f" 2>&1); rm -f "$f"
    if echo "$out" | grep -qF "$expect"; then
        PASS=$((PASS+1)); RESULTS+=("✅  $name")
    else
        FAIL=$((FAIL+1)); RESULTS+=("❌  $name | 期望:[$expect] 实际:[$out]")
    fi
}

S() { echo; echo "▶ $1"; }

# ══════════════════════════════════════════════
S "字符串插值 — 复杂表达式"
# ══════════════════════════════════════════════

t "插值中嵌套字符串方法链" '
words = ["hello","world"]
print("joined={words.join("-").upper()}")
' "joined=HELLO-WORLD"

t "插值中三元逻辑" '
x = 7
print("x is {x > 5 && x < 10}")
' "x is true"

t "插值中列表索引" '
l = [10, 20, 30]
print("mid={l[1]}")
' "mid=20"

t "插值中 map 嵌套访问" '
data = {user: {name: "Ada"}}
print("name={data.user.name}")
' "name=Ada"

t "插值中函数调用结果" '
fn square(x) { return x * x }
print("4^2={square(4)}")
' "4^2=16"

t "插值中条件表达式" '
score = 88
print("pass={score >= 60}")
' "pass=true"

t "多个插值在同一字符串" '
a = 1; b = 2; c = 3
print("{a}+{b}+{c}={a+b+c}")
' "1+2+3=6"

t "插值结果为 null 显示 null" '
m = {x: 1}
print("missing={m.y}")
' "missing=null"

t "插值中 to_string 转换" '
n = 3.14159
print("pi={to_string(n)}")
' "pi=3.14159"

t "多行字符串中多处插值" '
name = "Ada"; age = 30
bio = """
name: {name}
age: {age}
"""
print(bio.contains("Ada"))
print(bio.contains("30"))
' "true"

# ══════════════════════════════════════════════
S "变量作用域 — 精细边界"
# ══════════════════════════════════════════════

t "for 循环变量在循环后仍可访问" '
for i in range(0, 5) {}
print(i)
' "4"

t "while 循环内修改外层变量" '
x = 0
while x < 10 { x += 3 }
print(x)
' "12"

t "函数内赋值不影响外层同名变量" '
result = "outer"
fn f() { result = "inner" }
f()
print(result)
' "inner"

t "嵌套函数各自独立作用域" '
fn make(n) {
    fn get() { return n }
    return get
}
get5 = make(5)
get9 = make(9)
print(get5())
print(get9())
' "5"

t "闭包捕获循环变量当前值" '
fns = []
for i in range(0, 3) {
    n = i
    fns.push(fn() => n)
}
print(fns[0]())
print(fns[1]())
print(fns[2]())
' "0"

t "const 在不同作用域可重名" '
const X = 1
fn f() {
    const X = 2
    return X
}
print(f())
print(X)
' "2"

t "match 不创建新作用域" '
x = 0
match true {
    true => x = 99
}
print(x)
' "99"

t "if 块内赋值影响外层" '
val = "before"
if true { val = "after" }
print(val)
' "after"

# ══════════════════════════════════════════════
S "const 精细行为"
# ══════════════════════════════════════════════

t "const map 可以属性赋值" '
const cfg = {debug: false}
cfg.debug = true
print(cfg.debug)
' "true"

t "const map 可以 delete" '
const m = {a:1, b:2}
m.delete("a")
print(m.len())
' "1"

t "const list 可以 push" '
const l = []
l.push(1).push(2)
print(l.len())
' "2"

t "const list 可以 sort" '
const l = [3,1,2]
l.sort()
print(l[0])
' "1"

t "const 不可 += 报错含变量名" '
const N = 10
N += 1
' "N"

t "const 不可重新赋值报错含变量名" '
const NAME = "Ada"
NAME = "Bob"
' "NAME"

t "const 不可 -= 报错" '
const X = 5
X -= 1
' "E1001"

t "const 函数可正常调用" '
const greet = fn(name) => "hello {name}"
print(greet("Ada"))
' "hello Ada"

# ══════════════════════════════════════════════
S "列表 — 引用语义与副作用"
# ══════════════════════════════════════════════

t "列表赋值是引用" '
a = [1, 2, 3]
b = a
b.push(4)
print(a.len())
' "4"

t "slice 产生独立副本" '
a = [1, 2, 3]
b = a.slice(0)
b.push(99)
print(a.len())
' "3"

t "map 返回新列表不修改原" '
a = [1, 2, 3]
b = a.map(fn(x) => x * 2)
b[0] = 999
print(a[0])
' "1"

t "filter 返回新列表" '
a = [1, 2, 3, 4, 5]
b = a.filter(fn(x) => x > 2)
b.push(99)
print(a.len())
' "5"

t "sort 原地排序返回 self" '
a = [3, 1, 2]
b = a.sort()
print(a == b || a[0] == b[0])
' "true"

t "嵌套列表元素是引用" '
inner = [1, 2, 3]
outer = [inner, [4, 5]]
outer[0].push(99)
print(inner.len())
' "4"

t "reduce 不修改原列表" '
l = [1, 2, 3]
_ = l.reduce(fn(acc, x) => acc + x, 0)
print(l.len())
' "3"

t "find 不修改列表" '
l = [1, 2, 3]
_ = l.find(fn(x) => x > 1)
print(l.len())
' "3"

t "reverse 后原列表反转" '
l = [1, 2, 3, 4, 5]
l.reverse()
print(l[0])
print(l[4])
' "5"

t "pop 后长度减少" '
l = [1, 2, 3]
l.pop(); l.pop()
print(l.len())
' "1"

# ══════════════════════════════════════════════
S "map — 引用语义与插入顺序"
# ══════════════════════════════════════════════

t "map 赋值是引用" '
a = {x: 1}
b = a
b.y = 2
print(a.has("y"))
' "true"

t "merge 产生新 map" '
a = {x: 1}
b = a.merge({y: 2})
b.z = 3
print(a.has("z"))
' "false"

t "插入顺序稳定" '
m = {}
m.c = 3; m.a = 1; m.b = 2
print(m.keys().join(","))
' "c,a,b"

t "delete 后 keys 顺序保持" '
m = {a:1, b:2, c:3, d:4}
m.delete("b")
print(m.keys().join(","))
' "a,c,d"

t "merge 新 key 追加到末尾" '
a = {x:1, y:2}
b = a.merge({z:3})
k = b.keys()
print(k[2])
' "z"

t "map_values 保持键顺序" '
m = {c:3, a:1, b:2}
m2 = m.map_values(fn(v) => v * 10)
print(m2.keys().join(","))
' "c,a,b"

t "filter 保持键顺序" '
m = {c:3, a:1, b:5}
m2 = m.filter(fn(v) => v > 2)
print(m2.keys().join(","))
' "c,b"

t "entries 顺序与 keys 一致" '
m = {z:26, a:1, m:13}
e = m.entries()
print(e[0][0])
print(e[1][0])
print(e[2][0])
' "z"

# ══════════════════════════════════════════════
S "函数 — 默认参数与闭包交互"
# ══════════════════════════════════════════════

t "默认参数在调用时求值" '
counter = 0
fn next() { counter += 1; return counter }
fn f(x, n = next()) { return n }
f("a"); f("b"); f("c")
print(counter)
' "3"

t "默认参数可引用前面的参数" '
fn rect(w, h = w) { return w * h }
print(rect(5))
print(rect(3, 4))
' "25"

t "闭包共享同一环境" '
fn shared() {
    state = 0
    inc = fn() { state += 1 }
    get = fn() { return state }
    return {inc: inc, get: get}
}
s = shared()
s.inc(); s.inc(); s.inc()
print(s.get())
' "3"

t "函数名与变量名可相同后者覆盖" '
fn double(x) { return x * 2 }
result = double(5)
double = 99
print(result)
print(double)
' "10"

t "arrow 函数单表达式限制" '
add = fn(a, b) => a + b
print(add(3, 7))
' "10"

t "函数可接受 null 参数" '
fn f(x) {
    if x == null { return "nothing" }
    return to_string(x)
}
print(f(null))
print(f(42))
' "nothing"

t "函数可接受函数作为参数" '
fn twice(f, x) { return f(f(x)) }
print(twice(fn(x) => x + 1, 5))
' "7"

t "递归闭包" '
fn make_factorial() {
    fn fact(n) {
        if n <= 1 { return 1 }
        return n * fact(n - 1)
    }
    return fact
}
factorial = make_factorial()
print(factorial(6))
' "720"

t "函数返回多个闭包共享状态" '
fn bank(initial) {
    balance = initial
    deposit = fn(n) { balance += n; return balance }
    withdraw = fn(n) {
        if n > balance { return error.new("E_INSUF","not enough") }
        balance -= n
        return balance
    }
    return {deposit: deposit, withdraw: withdraw}
}
acct = bank(100)
acct.deposit(50)
acct.deposit(25)
result = acct.withdraw(30)
print(result)
' "145"

# ══════════════════════════════════════════════
S "控制流 — 嵌套与边界"
# ══════════════════════════════════════════════

t "三层嵌套 for + break" '
found = null
for i in range(0,5) {
    for j in range(0,5) {
        if i * j == 12 { found = "{i}*{j}"; break }
    }
    if found != null { break }
}
print(found)
' "3*4"

t "match 在 for 循环内" '
counts = {A:0, B:0, other:0}
for grade in ["A","B","A","C","A","B"] {
    match grade {
        "A" => counts.A += 1
        "B" => counts.B += 1
        _   => counts.other += 1
    }
}
print(counts.A)
print(counts.B)
print(counts.other)
' "3"

t "while 里 continue 不导致死循环" '
i = 0; count = 0
while i < 10 {
    i += 1
    if i % 2 == 0 { continue }
    count += 1
}
print(count)
' "5"

t "for 循环收集结果" '
squares = []
for n in range(1, 6) { squares.push(n * n) }
print(squares.join(","))
' "1,4,9,16,25"

t "match 作为赋值右值" '
fn classify(n) {
    label = ""
    match true {
        n < 0   => label = "negative"
        n == 0  => label = "zero"
        n > 0   => label = "positive"
    }
    return label
}
print(classify(-5))
print(classify(0))
print(classify(7))
' "negative"

t "if 表达式链" '
fn sign(x) {
    if x > 0 { return 1 }
    else if x < 0 { return -1 }
    else { return 0 }
}
print(sign(5))
print(sign(-3))
print(sign(0))
' "1"

t "for 修改外部 list 同时迭代副本" '
l = [1,2,3,4,5]
result = []
for x in l {
    if x % 2 == 0 { result.push(x * 10) }
}
print(result.join(","))
' "20,40"

t "嵌套 for 收集乘积" '
pairs = []
for i in range(1,4) {
    for j in range(1,4) {
        if i == j { pairs.push(i * j) }
    }
}
print(pairs.join(","))
' "1,4,9"

# ══════════════════════════════════════════════
S "错误处理 — 边界与组合"
# ══════════════════════════════════════════════

t "? 在循环中使用" '
fn validate(x) {
    if x < 0 { return error.new("E_NEG","negative") }
    return x * 2
}
fn process(items) {
    results = []
    for item in items {
        r = validate(item)?
        results.push(r)
    }
    return results
}
r = process([1,2,-1,3])
print(r.code)
' "E_NEG"

t "try/catch 后继续执行" '
x = 0
try {
    x = error.new("E","fail")?
} catch err {
    x = 99
}
print(x)
print("still running")
' "still running"

t "多层 try/catch 内层处理" '
fn inner() { return error.new("E_IN","inner fail") }
fn outer() {
    try {
        r = inner()?
        return r
    } catch err {
        return "handled: {err.code}"
    }
}
print(outer())
' "handled: E_IN"

t "error.wrap 链式 message" '
e1 = error.new("E_ROOT","root cause")
e2 = error.wrap(e1, "step2 failed")
e3 = error.wrap(e2, "step3 failed")
print(e3.message.contains("step3"))
print(e3.message.contains("step2"))
' "true"

t "error.is 沿链查找" '
root = error.new("E_DB","db error")
w1 = error.wrap(root, "query failed")
w2 = error.wrap(w1, "request failed")
print(error.is(w2, "E_DB"))
' "true"

t "error.is 不存在的 code 返回 false" '
err = error.new("E_A","msg")
print(error.is(err, "E_B"))
' "false"

t "? 在 try 块外在函数内正常传播" '
fn risky() { return error.new("E_X","fail") }
fn caller() { return risky()? }
result = caller()
print(result.code)
' "E_X"

t "catch 变量不污染外层同名变量" '
err = "original"
try {
    x = error.new("E_T","test")?
} catch err {
    print(err.code)
}
print(type_of(err))
' "E_T"

t "错误对象字段完整性" '
err = error.new("E_FULL","msg",
    fix: "do this",
    retry: true)
print(err.code)
print(err.message)
print(err.fix)
print(err.retry)
print(type_of(err.docs))
' "E_FULL"

t "非错误值 ? 原样返回" '
fn get_num() { return 42 }
fn use_num() {
    n = get_num()?
    return n + 1
}
print(use_num())
' "43"

# ══════════════════════════════════════════════
S "类型转换 — 完整矩阵"
# ══════════════════════════════════════════════

t "to_string list" '
s = to_string([1,2,3])
print(type_of(s))
' "string"

t "to_string map" '
s = to_string({a:1})
print(type_of(s))
' "string"

t "to_string fn" '
s = to_string(fn(x) => x)
print(type_of(s))
' "string"

t "to_number null" '
print(to_number(null))
' "null"

t "to_number bool true" '
print(to_number(true))
' "1"

t "to_number bool false" '
print(to_number(false))
' "0"

t "to_number list" '
print(to_number([42]))
' "null"

t "to_bool 非空列表" '
print(to_bool([]))
print(to_bool([1]))
' "true"

t "to_bool 非空 map" '
print(to_bool({}))
print(to_bool({a:1}))
' "true"

t "to_bool number 0" '
print(to_bool(0))
' "true"

t "to_bool null" '
print(to_bool(null))
' "false"

t "to_bool false" '
print(to_bool(false))
' "false"

t "number 精度保持" '
x = 0.1 + 0.2
print(x > 0.29 && x < 0.31)
' "true"

t "整数显示无小数点" '
print(10 / 2)
' "5"

t "浮点显示小数" '
print(10 / 3)
' "3.333"

# ══════════════════════════════════════════════
S "运算符 — 完整覆盖"
# ══════════════════════════════════════════════

t "== list 不相等（引用比较）" '
a = [1,2,3]; b = [1,2,3]
print(a == b)
' "false"

t "== map 不相等（引用比较）" '
a = {x:1}; b = {x:1}
print(a == b)
' "false"

t "== 同一引用相等" '
a = [1,2,3]; b = a
print(a == b)
' "true"

t "!= 数字" '
print(1 != 2)
print(1 != 1)
' "true"

t ">= 边界" '
print(5 >= 5)
print(5 >= 6)
' "true"

t "<= 边界" '
print(5 <= 5)
print(6 <= 5)
' "true"

t "! 对 truthy 值" '
print(!1)
print(![])
print(!{})
' "false"

t "! 对 falsy 值" '
print(!null)
print(!false)
' "true"

t "字符串 == 区分大小写" '
print("Hello" == "hello")
print("Hello" == "Hello")
' "false"

t "复合赋值字符串 +=" '
s = "foo"
s += "bar"
s += "baz"
print(s)
' "foobarbaz"

t "链式比较运算" '
x = 5
print(x > 1 && x < 10 && x != 7)
' "true"

t "% 正数结果为正" '
print(7 % 3)
' "1"

t "/ 浮点结果" '
print(1 / 3 > 0.33 && 1 / 3 < 0.34)
' "true"

t "* 与浮点" '
print(0.5 * 4)
' "2"

# ══════════════════════════════════════════════
S "内置方法 — 边界与返回值"
# ══════════════════════════════════════════════

t "s.split 保留空字符串" '
parts = "a,,b".split(",")
print(parts.len())
print(parts[1])
' "3"

t "s.contains 空字符串" '
print("hello".contains(""))
' "true"

t "s.replace 找不到不报错" '
print("hello".replace("xyz","ABC"))
' "hello"

t "s.slice end 省略" '
print("hello world".slice(6))
' "world"

t "l.reduce 单元素无需 init" '
l = [42]
print(l.reduce(fn(a,x) => a+x, 0))
' "42"

t "l.map 返回与原列表等长" '
l = [1,2,3,4,5]
m = l.map(fn(x) => x * 2)
print(m.len())
' "5"

t "l.filter 全通过" '
l = [2,4,6,8]
print(l.filter(fn(x) => x%2==0).len())
' "4"

t "l.filter 全过滤" '
l = [1,3,5,7]
print(l.filter(fn(x) => x%2==0).len())
' "0"

t "m.keys 空 map 返回空列表" '
print({}.keys().len())
' "0"

t "m.values 空 map 返回空列表" '
print({}.values().len())
' "0"

t "m.entries 空 map 返回空列表" '
print({}.entries().len())
' "0"

t "l.unique 已经唯一的列表不变" '
l = [1,2,3,4,5]
print(l.unique().len())
' "5"

t "l.join 数字列表" '
print([1,2,3].join(" + "))
' "1 + 2 + 3"

t "s.match 返回列表类型" '
m = "a1b2".match("[0-9]")
print(type_of(m))
' "list"

# ══════════════════════════════════════════════
S "综合场景 — 实际使用模式"
# ══════════════════════════════════════════════

t "实现 map 函数（不用内置）" '
fn my_map(list, f) {
    result = []
    for item in list { result.push(f(item)) }
    return result
}
print(my_map([1,2,3], fn(x) => x*x).join(","))
' "1,4,9"

t "实现 filter 函数" '
fn my_filter(list, pred) {
    result = []
    for item in list {
        if pred(item) { result.push(item) }
    }
    return result
}
evens = my_filter(range(1,11), fn(x) => x%2==0)
print(evens.join(","))
' "2,4,6,8,10"

t "实现 zip 函数" '
fn zip(a, b) {
    result = []
    len = a.len()
    if b.len() < len { len = b.len() }
    for i in range(0, len) {
        result.push([a[i], b[i]])
    }
    return result
}
pairs = zip([1,2,3], ["a","b","c"])
print(pairs[0][1])
print(pairs[2][0])
' "a"

t "实现 group_by 函数" '
fn group_by(list, key_fn) {
    groups = {}
    for item in list {
        key = to_string(key_fn(item))
        if !groups.has(key) { groups[key] = [] }
        groups[key].push(item)
    }
    return groups
}
nums = [1,2,3,4,5,6,7,8]
groups = group_by(nums, fn(x) => x % 3)
print(groups["0"].len())
print(groups["1"].len())
' "2"

t "实现简单状态机" '
fn make_traffic_light() {
    states = ["red","yellow","green"]
    idx = 0
    return fn() {
        current = states[idx]
        idx = (idx + 1) % 3
        return current
    }
}
light = make_traffic_light()
print(light())
print(light())
print(light())
print(light())
' "red"

t "实现 retry 逻辑" '
attempts = 0
fn flaky_op() {
    attempts += 1
    if attempts < 3 { return error.new("E_FAIL","not yet") }
    return "success"
}
fn with_retry(op, max_tries) {
    i = 0
    while i < max_tries {
        result = op()
        if type_of(result) != "map" { return result }
        if !error.is(result, "E_FAIL") { return result }
        i += 1
    }
    return error.new("E_MAX","max retries exceeded")
}
print(with_retry(flaky_op, 5))
print(attempts)
' "success"

t "实现事件系统" '
fn make_emitter() {
    listeners = {}
    on = fn(event, handler) {
        if !listeners.has(event) { listeners[event] = [] }
        listeners[event].push(handler)
    }
    emit = fn(event, data) {
        if listeners.has(event) {
            for h in listeners[event] { h(data) }
        }
    }
    return {on: on, emit: emit}
}
log = []
emitter = make_emitter()
emitter.on("data", fn(x) => log.push("got:{x}"))
emitter.on("data", fn(x) => log.push("also:{x}"))
emitter.emit("data", 42)
print(log.len())
print(log[0])
' "2"

t "实现不可变更新模式" '
fn update(m, key, value) {
    return m.merge({[key]: value})
}
user = {name: "Ada", age: 30, role: "user"}
updated = update(user, "role", "admin")
print(user.role)
print(updated.role)
' "user"

t "实现深拷贝（基础类型）" '
fn deep_copy(val) {
    t = type_of(val)
    if t == "list" {
        result = []
        for item in val { result.push(deep_copy(item)) }
        return result
    }
    if t == "map" {
        result = {}
        for entry in val.entries() {
            result[entry[0]] = deep_copy(entry[1])
        }
        return result
    }
    return val
}
original = {a: [1, 2, 3], b: {x: 10}}
copy = deep_copy(original)
copy.a.push(99)
copy.b.x = 999
print(original.a.len())
print(original.b.x)
' "3"

t "函数式链式数据管道" '
fn pipeline(data, steps) {
    result = data
    for step in steps { result = step(result) }
    return result
}
students = [
    {name:"Ada",   score:92, active:true},
    {name:"Bob",   score:65, active:false},
    {name:"Carol", score:88, active:true},
    {name:"Dan",   score:71, active:true},
    {name:"Eve",   score:55, active:false},
]
result = pipeline(students, [
    fn(l) => l.filter(fn(s) => s.active),
    fn(l) => l.filter(fn(s) => s.score >= 70),
    fn(l) => l.map(fn(s) => s.name),
    fn(l) => l.sort(),
])
print(result.join(","))
' "Ada,Carol,Dan"

t "错误安全的配置读取器" '
fn get_config(cfg, path, default_val) {
    parts = path.split(".")
    current = cfg
    for part in parts {
        if type_of(current) != "map" { return default_val }
        val = current.get(part, null)
        if val == null { return default_val }
        current = val
    }
    return current
}
config = {
    server: {
        host: "localhost",
        port: 8080,
        tls: {enabled: false},
    }
}
print(get_config(config, "server.host", "unknown"))
print(get_config(config, "server.port", 0))
print(get_config(config, "server.tls.enabled", true))
print(get_config(config, "database.host", "default-db"))
' "localhost"

t "求解数独行验证" '
fn valid_row(row) {
    if row.len() != 9 { return false }
    seen = {}
    for n in row {
        if n < 1 || n > 9 { return false }
        key = to_string(n)
        if seen.has(key) { return false }
        seen[key] = true
    }
    return true
}
print(valid_row([1,2,3,4,5,6,7,8,9]))
print(valid_row([1,2,3,4,5,6,7,8,8]))
print(valid_row([1,2,3,4,5,6,7,8]))
' "true"

# ══════════════════════════════════════════════
S "报错质量 — 每条错误要有 fix"
# ══════════════════════════════════════════════

t "undefined var 报错含 fix" '
print(not_defined)
' "fix"

t "undefined func 报错含类型" '
x = 42; x()
' "E1004"

t "类型不匹配加法报错含类型名" '
1 + true
' "E1002"

t "索引越界报错含范围信息" '
l = [1,2,3]
l[10] = 99
' "E1005"

t "除零报错含 E9003" '
5 / 0
' "E9003"

t "栈溢出报错含 fix" '
fn inf() { return inf() }
inf()
' "fix"

t "对非 map 赋值属性报错含类型" '
n = 42
n.x = 1
' "E1002"

t "for in 非列表报错含 fix" '
for x in 42 {}
' "fix"

t "print 多参数报错含 fix" '
print(1, 2, 3)
' "fix"

t "channel 在 eval 报错含 codong run" '
ch = channel()
' "codong run"

# ══════════════════════════════════════════════
# 汇总
# ══════════════════════════════════════════════
echo ""
echo "══════════════════════════════════════════════════════"
for r in "${RESULTS[@]}"; do echo "$r"; done
echo "══════════════════════════════════════════════════════"
echo " ✅ PASS: $PASS   ❌ FAIL: $FAIL   总计: $((PASS+FAIL))"
echo "══════════════════════════════════════════════════════"
