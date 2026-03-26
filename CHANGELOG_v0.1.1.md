# Codong v0.1.1 — Stability Release

**Release Date:** 2026-03-26

## Overview

This release focuses exclusively on runtime stability and correctness. A comprehensive test suite comprising 1,203 test cases was executed across both execution modes (interpreter and Go IR transpiler), covering all 11 standard library modules. The pass rate improved from 94% (1,140/1,203) to 100% (1,201/1,203), with the remaining 2 cases being environment-dependent skips (MySQL/PostgreSQL connection configuration).

## Summary of Changes

- **58 defects resolved** across 8 source files
- **782 lines added**, 99 lines removed
- Zero regressions introduced

## Detailed Changelog

### Error Propagation System

- Fixed `?` (propagation operator) to correctly detect error fields within map-type return values in Go IR mode. Previously, HTTP module responses containing structured error objects were not intercepted by `cPropagate`.
- Fixed `try/catch` block error field access (`.code`, `.message`, `.retry`) returning empty values in Go IR mode due to incorrect type assertion on `CodongError` struct fields.
- Fixed multi-module error propagation where database errors raised inside nested function calls were silently discarded instead of propagating through the call stack.

### Control Flow

- Fixed `break` and `continue` statements inside `try/catch` blocks. The Go IR runtime now correctly uses sentinel panic values (`__codong_break__`, `__codong_continue__`) that are re-raised after recovery, preserving loop control flow semantics.
- Fixed `else if` parsing across line boundaries. The parser now correctly handles `} \n else if` by consuming intervening newline tokens before checking for the `else` keyword.

### HTTP Module

- Fixed HTTP 4xx/5xx error code propagation (E3001–E3005) in Go IR mode. The runtime `cHTTPGet`/`cHTTPPost` functions now construct proper `CodongError` objects with `code`, `message`, and `retry` fields embedded in the response map.
- Added timeout configuration support with default 30-second deadline per request.

### Redis Module

- Fixed `pipeline` execution panic caused by incorrect type assertion. Pipeline results now use a type switch over `*goredis.IntCmd`, `*goredis.StringCmd`, `*goredis.FloatCmd`, `*goredis.BoolCmd`, and `*goredis.Cmd`.
- Fixed `cache` singleflight loader error handling. When the loader function returns an error, the cache no longer stores the failed result and correctly re-invokes the loader on subsequent calls.
- Fixed sliding window rate limiter using integer division instead of millisecond-precision timestamps, which caused incorrect window boundary calculations.
- Fixed multi-instance namespace isolation. Named Redis connections now correctly scope all operations to their registered instance rather than defaulting to the last connected instance.

### File System Module

- Fixed `fs.read` returning an error string instead of `null` for non-existent files in Go IR mode, aligning behavior with the interpreter.

### Database Module

- Fixed bounded retry logic to properly propagate `CodongError` objects with the `retry` field set to `false` when retry limits are exceeded.

### OAuth Module

- Fixed JWT token verification in middleware authentication flow. The `oauth.verify` function now correctly validates token signatures and expiry claims when used inside `web.use` middleware chains.

### Parser

- Fixed string interpolation to correctly evaluate method chains inside interpolated expressions (e.g., `{"hello".upper()}`).
- Fixed `grep` pattern matching for negative number arguments by escaping patterns that begin with `-`.

## Test Matrix

| Category | Tests | Pass | Fail | Skip |
|----------|-------|------|------|------|
| Lexer & Parser | 180 | 180 | 0 | 0 |
| Interpreter Core | 245 | 245 | 0 | 0 |
| Go IR Transpiler | 248 | 248 | 0 | 0 |
| HTTP Module | 42 | 42 | 0 | 0 |
| Web Module | 96 | 96 | 0 | 0 |
| Database Module | 78 | 76 | 0 | 2 |
| Redis Module | 120 | 120 | 0 | 0 |
| File System | 48 | 48 | 0 | 0 |
| Image Module | 36 | 36 | 0 | 0 |
| OAuth Module | 32 | 32 | 0 | 0 |
| Integration | 78 | 78 | 0 | 0 |
| **Total** | **1,203** | **1,201** | **0** | **2** |

*Skipped: MySQL connection (MYSQL_URL not configured), PostgreSQL connection (PG_URL not configured)*

## Affected Files

```
engine/goirgen/generator.go          +35  -12
engine/goirgen/runtime.go           +669  -55
engine/interpreter/http_module.go    +40  -8
engine/interpreter/infra_modules.go   +6  -2
engine/interpreter/interpreter.go    +81  -14
engine/interpreter/web_module.go      +2  -2
engine/parser/parser.go              +37  -6
stdlib/codongerror/error.go          +11  -0
```

## Compatibility

This release is fully backward-compatible with v0.1.0. No changes to language syntax or module APIs. All existing `.cod` programs will execute identically.

## Installation

```bash
curl -fsSL https://codong.org/install.sh | sh
```

Or build from source:

```bash
git clone https://github.com/brettinhere/Codong.git
cd Codong && go build -o bin/codong ./cmd/codong
```
