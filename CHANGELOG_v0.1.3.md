# Codong v0.1.3 — Language Completeness Release

**Release Date:** 2026-03-28

## Overview

This release completes the Codong language specification across all standard library modules and core language features. Since v0.1.1, a total of **141 defects** were resolved across **11 source files**, with **1,847 lines added**. Two new test suites (`test_devlog.sh`, `test_v21.sh`) were introduced, bringing total coverage to **1,427 test cases** at **100% pass rate**.

A compilation cache was also introduced in this release, reducing repeated execution time by approximately **170×**.

---

## v0.1.3 — Stability & Performance (2026-03-28)

### Compilation Cache

- `codong run` now caches compiled binaries at `~/.codong/cache/<sha256>/main`
- First run: compiles and caches (~2–3 seconds)
- Subsequent runs with identical source: direct binary execution via `syscall.Exec` (~17ms)
- Cache invalidation is automatic: any source change produces a new hash and triggers recompilation
- No manual cache management required

### Bug Fixes

- `db.query(sql, [params])` — string parameters were truncated to first character when using `?` placeholders. Fixed by ensuring parameter binding passes the full string value rather than treating it as an indexable sequence.
- Function hoisting in Go IR: functions defined after their call site within the same file now resolve correctly. The Go IR generator was refactored to emit all function definitions before executable statements (two-pass generation), eliminating `nil pointer dereference` panics on forward references.
- `json.flat` aliased to `json.flatten` for consistency with list method naming.

---

## v0.1.2 — Module Completeness (2026-03-27)

### Language Core

| Feature | Before | After |
|---------|--------|-------|
| Power operator `**` | Not supported | `2**10 → 1024` |
| `int()`, `float()`, `str()`, `bool()` | Undefined | Type conversion functions |
| `len(x)` | Method only (`.len()`) | Also works as global function |
| `sort(list)` | Global not available | Works as `sort([3,1,2])` |
| `list.flat()` | Shallow (1 level) | Deep recursive flattening |
| `list.count(x)` | Counted string chars | Counts list element occurrences |
| `map.from_entries(list)` | Not implemented | `[{k:"a",v:1}]` → `{a:1}` |
| `map == map` | Always false | Value-based equality |
| `str.index(sub)` | Not implemented | Returns first position of substring |
| `str.reverse()` | Not implemented | Reverses string characters |
| `str.format(args)` | Not implemented | `"1+2={0}".format(3)` |

### Go IR Runtime — New Module Methods

**fs module**
- `fs.mkdir(path)` — create directory (recursive)
- `fs.is_dir(path)` — check if path is a directory
- `fs.ls(path)` — list directory contents as array

**json module**
- `json.pretty(data)` — pretty-print JSON with indentation

**env module**
- `env.set(key, value)` — set environment variable at runtime

**time module**
- `time.unix()` — current Unix timestamp (integer seconds)
- `time.add(t, duration, unit)` — add duration to time map
- `time.diff(t1, t2, unit)` — difference between two time maps
- `time.before(t1, t2)` / `time.after(t1, t2)` — time comparison
- `time.quarter(t)` — fiscal quarter (1–4) for a time map

**db module**
- `db.sum(table, col, where)` / `db.avg` / `db.min` / `db.max` — aggregate queries
- `db.batch_insert(table, rows)` — bulk insert via prepared statement

**redis module (Go IR)**
- `redis.hset` / `redis.hget` / `redis.hdel` / `redis.hgetall` — Hash operations
- `redis.lpush` / `redis.rpush` / `redis.lpop` / `redis.rpop` — List operations
- `redis.lrange` / `redis.llen` — List range and length
- `redis.zcount` / `redis.zrem` — Sorted set count and remove

**image module (Go IR)**
- `image.create(w, h, color)` — create blank canvas
- `image.watermark_tile(img, text)` — repeating watermark
- `image.smart_crop(img, w, h)` — content-aware crop
- `image.to_rgb(img)` — convert to RGB color space

**oauth module (Go IR)**
- `oauth.pkce.verifier()` / `oauth.pkce.challenge(v)` — PKCE flow
- `oauth.rbac.define(role, perms)` / `oauth.rbac.check(user, perm)` / `oauth.rbac.assign(user, role)` — Role-based access control

**web module (Go IR)**
- Server-Sent Events (`web.sse`) now compiles and runs correctly
- Query string parameters (`req.query(key)`) fixed
- 404 handler returns correct status code

---

## Test Coverage

| Suite | Tests | Pass | Rate |
|-------|-------|------|------|
| `test_full.sh` | 1,203 | 1,201 | 99.8% |
| `test_devlog.sh` | 122 | 122 | 100% |
| `test_v21.sh` | 224 | 224 | 100% |
| **Combined** | **1,427** | **1,425** | **99.9%** |

*Skipped: MySQL (MYSQL_URL not configured), PostgreSQL (PG_URL not configured)*

---

## Files Changed (v0.1.1 → v0.1.3)

```
engine/interpreter/interpreter.go    +433  -14   core language, type funcs, operators
engine/goirgen/runtime.go           +766  -55   all module methods in Go IR
engine/goirgen/generator.go         +248  -12   function hoisting, two-pass codegen
engine/parser/parser.go              +87  -6    ** operator, else-if across lines
engine/interpreter/infra_modules.go  +39  -2    fs/json/env/time new methods
engine/runner/runner.go              +75  -3    compilation cache
engine/lexer/lexer.go                 +5  -0    ** token
engine/lexer/token.go                 +2  -0    POWER token type
engine/parser/ast.go                  +1  -0    power expression node
cmd/codong/main.go                    +2  -1    version bump 0.1.0 → 0.1.3
DEVLOG.md                           +257  -0    developer log
```

---

## Compatibility

Fully backward-compatible with v0.1.0 and v0.1.1. No syntax changes. All existing `.cod` programs run identically or faster (cache benefit).

---

## Installation

```bash
curl -fsSL https://codong.org/install.sh | sh
codong version  # v0.1.3
```

Build from source:

```bash
git clone https://github.com/brettinhere/Codong.git
cd Codong && go build -o bin/codong ./cmd/codong
```
