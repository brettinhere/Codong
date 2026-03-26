package codongerror

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Stage 1 error codes
const (
	// E1xxx - Syntax/Type errors
	E1001_SYNTAX_ERROR     = "E1001_SYNTAX_ERROR"
	E1002_TYPE_MISMATCH    = "E1002_TYPE_MISMATCH"
	E1003_UNDEFINED_VAR    = "E1003_UNDEFINED_VAR"
	E1004_UNDEFINED_FUNC   = "E1004_UNDEFINED_FUNC"
	E1005_INVALID_ARGUMENT = "E1005_INVALID_ARGUMENT"

	// E2xxx - Database errors
	E2001_NOT_FOUND              = "E2001_NOT_FOUND"
	E2002_CONNECTION_FAILED      = "E2002_CONNECTION_FAILED"
	E2003_QUERY_ERROR            = "E2003_QUERY_ERROR"
	E2004_DUPLICATE_KEY          = "E2004_DUPLICATE_KEY"
	E2005_MIGRATION_FAILED       = "E2005_MIGRATION_FAILED"
	E2006_TRANSACTION_FAILED     = "E2006_TRANSACTION_FAILED"
	E2007_TIMEOUT                = "E2007_TIMEOUT"
	E2008_TOO_MANY_CONNECTIONS   = "E2008_TOO_MANY_CONNECTIONS"
	E2009_FOREIGN_KEY_VIOLATION  = "E2009_FOREIGN_KEY_VIOLATION"
	E2010_CHECK_VIOLATION        = "E2010_CHECK_VIOLATION"

	// E3xxx - HTTP/Web errors
	E3001_TIMEOUT         = "E3001_TIMEOUT"
	E3002_BIND_FAILED     = "E3002_BIND_FAILED"
	E3003_HTTP_4XX        = "E3003_HTTP_4XX"
	E3004_HTTP_5XX        = "E3004_HTTP_5XX"
	E3005_CONN_FAILED     = "E3005_CONN_FAILED"
	E3006_INVALID_URL     = "E3006_INVALID_URL"
	E3007_ROUTE_ERROR     = "E3007_ROUTE_ERROR"
	E3008_HANDLER_ERROR   = "E3008_HANDLER_ERROR"
	E3009_SERVER_ERROR    = "E3009_SERVER_ERROR"

	// E4xxx - LLM errors
	E4001_LLM_ERROR       = "E4001_LLM_ERROR"
	E4002_RATE_LIMITED     = "E4002_RATE_LIMITED"
	E4003_TOKEN_EXCEEDED   = "E4003_TOKEN_EXCEEDED"
	E4004_MODEL_NOT_FOUND  = "E4004_MODEL_NOT_FOUND"
	E4005_API_KEY_MISSING  = "E4005_API_KEY_MISSING"

	// E5xxx - File/IO errors
	E5001_FILE_NOT_FOUND    = "E5001_FILE_NOT_FOUND"
	E5002_PERMISSION_DENIED = "E5002_PERMISSION_DENIED"
	E5003_FILE_EXISTS       = "E5003_FILE_EXISTS"
	E5004_IS_DIRECTORY      = "E5004_IS_DIRECTORY"
	E5005_NOT_DIRECTORY     = "E5005_NOT_DIRECTORY"
	E5006_DIR_NOT_EMPTY     = "E5006_DIR_NOT_EMPTY"
	E5007_INVALID_PATH      = "E5007_INVALID_PATH"
	E5008_IO_ERROR          = "E5008_IO_ERROR"
	E5009_FILE_TOO_LARGE    = "E5009_FILE_TOO_LARGE"

	// E6xxx - JSON errors
	E6001_PARSE_ERROR     = "E6001_PARSE_ERROR"
	E6002_STRINGIFY_ERROR = "E6002_STRINGIFY_ERROR"
	E6003_INVALID_PATH    = "E6003_INVALID_PATH"

	// E7xxx - Environment errors
	E7001_ENV_NOT_SET        = "E7001_ENV_NOT_SET"
	E7002_ENV_FILE_NOT_FOUND = "E7002_ENV_FILE_NOT_FOUND"
	E7003_ENV_PARSE_ERROR    = "E7003_ENV_PARSE_ERROR"

	// E9xxx - System/Runtime errors
	E9001_OUT_OF_MEMORY  = "E9001_OUT_OF_MEMORY"
	E9002_STACK_OVERFLOW = "E9002_STACK_OVERFLOW"
	E9003_PANIC          = "E9003_PANIC"
	E9004_GOROUTINE_LEAK = "E9004_GOROUTINE_LEAK"

	// E10xxx - Redis errors
	E10001_CONN_FAILED      = "E10001_CONN_FAILED"
	E10002_AUTH_FAILED      = "E10002_AUTH_FAILED"
	E10003_KEY_NOT_FOUND    = "E10003_KEY_NOT_FOUND"
	E10004_LOCK_TIMEOUT     = "E10004_LOCK_TIMEOUT"
	E10005_LOCK_LOST        = "E10005_LOCK_LOST"
	E10006_CLUSTER_ERROR    = "E10006_CLUSTER_ERROR"
	E10007_PIPELINE_FAILED  = "E10007_PIPELINE_FAILED"
	E10008_SCRIPT_ERROR     = "E10008_SCRIPT_ERROR"

	// E12xxx - Image errors
	E12001_UNSUPPORTED_FORMAT = "E12001_UNSUPPORTED_FORMAT"
	E12002_CORRUPT_IMAGE      = "E12002_CORRUPT_IMAGE"
	E12003_TOO_LARGE          = "E12003_TOO_LARGE"
	E12004_INVALID_DIMENSIONS = "E12004_INVALID_DIMENSIONS"
	E12005_FONT_NOT_FOUND     = "E12005_FONT_NOT_FOUND"
	E12006_SAVE_FAILED        = "E12006_SAVE_FAILED"
	E12007_PROCESSING_FAILED  = "E12007_PROCESSING_FAILED"

	// E14xxx - OAuth errors
	E14001_INVALID_STATE        = "E14001_INVALID_STATE"
	E14002_CODE_EXCHANGE_FAILED = "E14002_CODE_EXCHANGE_FAILED"
	E14003_INVALID_TOKEN        = "E14003_INVALID_TOKEN"
	E14004_TOKEN_EXPIRED        = "E14004_TOKEN_EXPIRED"
	E14005_TOKEN_REVOKED        = "E14005_TOKEN_REVOKED"
	E14006_INSUFFICIENT_SCOPE   = "E14006_INSUFFICIENT_SCOPE"
	E14007_PROVIDER_ERROR       = "E14007_PROVIDER_ERROR"
	E14008_PROFILE_FETCH_FAILED = "E14008_PROFILE_FETCH_FAILED"
	E14009_FORBIDDEN            = "E14009_FORBIDDEN"
	E14010_PKCE_FAILED          = "E14010_PKCE_FAILED"
)

// OutputFormat controls the global error output format ("json" or "compact").
var OutputFormat = "json"

// CodongError is the standard error type for all Codong errors.
type CodongError struct {
	Source   string         `json:"error"`
	Code     string         `json:"code"`
	Message  string         `json:"message"`
	Fix      string         `json:"fix"`
	Retry    bool           `json:"retry"`
	Docs     string         `json:"docs"`
	Context  map[string]any `json:"context,omitempty"`
	Stack    []string       `json:"stack,omitempty"`
	CausedBy *CodongError   `json:"caused_by,omitempty"`
}

// Error implements the Go error interface.
func (e *CodongError) Error() string {
	s := fmt.Sprintf("[%s] %s", e.Code, e.Message)
	if e.Fix != "" {
		s += fmt.Sprintf("\n  fix: %s", e.Fix)
	}
	return s
}

// IsError returns true, identifying this as a Codong error object (not a regular map).
func (e *CodongError) IsError() bool { return true }

// Option is a functional option for New().
type Option func(*CodongError)

func WithFix(fix string) Option       { return func(e *CodongError) { e.Fix = fix } }
func WithRetry(retry bool) Option     { return func(e *CodongError) { e.Retry = retry } }
func WithContext(ctx map[string]any) Option { return func(e *CodongError) { e.Context = ctx } }
func WithDocs(docs string) Option     { return func(e *CodongError) { e.Docs = docs } }

// New creates a new CodongError.
func New(code, message string, opts ...Option) *CodongError {
	e := &CodongError{
		Source:  sourceFromCode(code),
		Code:    code,
		Message: message,
		Docs:    fmt.Sprintf("codong.org/errors#%s", code),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Wrap wraps an error with additional context, forming an error chain.
func Wrap(err *CodongError, context string) *CodongError {
	return &CodongError{
		Source:   err.Source,
		Code:     err.Code,
		Message:  fmt.Sprintf("%s: %s", context, err.Message),
		Fix:      err.Fix,
		Retry:    err.Retry,
		Docs:     err.Docs,
		CausedBy: err,
	}
}

// Is checks if an error matches a code, walking the error chain.
func Is(err *CodongError, code string) bool {
	for e := err; e != nil; e = e.CausedBy {
		if e.Code == code {
			return true
		}
	}
	return false
}

// Unwrap returns the caused_by error, or nil.
func Unwrap(err *CodongError) *CodongError {
	if err == nil {
		return nil
	}
	return err.CausedBy
}

// ToJSON serializes the error to JSON.
func ToJSON(err *CodongError) string {
	b, _ := json.Marshal(err)
	return string(b)
}

// ToCompact serializes to compact pipe-delimited format.
func ToCompact(err *CodongError) string {
	parts := []string{
		fmt.Sprintf("err_code:%s", err.Code),
		fmt.Sprintf("src:%s", err.Source),
		fmt.Sprintf("msg:%s", err.Message),
		fmt.Sprintf("fix:%s", err.Fix),
		fmt.Sprintf("retry:%t", err.Retry),
	}
	if len(err.Context) > 0 {
		ctxParts := []string{}
		for k, v := range err.Context {
			ctxParts = append(ctxParts, fmt.Sprintf("%s=%v", k, v))
		}
		parts = append(parts, fmt.Sprintf("ctx:%s", strings.Join(ctxParts, ",")))
	}
	return strings.Join(parts, "|")
}

// FromJSON deserializes from JSON.
func FromJSON(data string) (*CodongError, error) {
	var e CodongError
	if err := json.Unmarshal([]byte(data), &e); err != nil {
		return nil, fmt.Errorf("invalid error JSON: %w", err)
	}
	return &e, nil
}

// FromCompact deserializes from compact format.
func FromCompact(str string) (*CodongError, error) {
	e := &CodongError{}
	parts := strings.Split(str, "|")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		idx := strings.Index(part, ":")
		if idx < 0 {
			continue
		}
		key := part[:idx]
		val := part[idx+1:]
		switch key {
		case "err_code":
			e.Code = val
		case "src":
			e.Source = val
		case "msg":
			e.Message = val
		case "fix":
			e.Fix = val
		case "retry":
			e.Retry = val == "true"
		}
	}
	if e.Code == "" {
		return nil, fmt.Errorf("invalid compact format: missing err_code")
	}
	e.Docs = fmt.Sprintf("codong.org/errors#%s", e.Code)
	return e, nil
}

// SetFormat sets the global error output format.
func SetFormat(format string) {
	if format == "json" || format == "compact" {
		OutputFormat = format
	}
}

// FormatError formats an error using the current global format.
func FormatError(err *CodongError) string {
	if OutputFormat == "compact" {
		return ToCompact(err)
	}
	return ToJSON(err)
}

// sourceFromCode extracts the error source category from a code prefix.
func sourceFromCode(code string) string {
	if len(code) < 2 {
		return "unknown"
	}
	// Handle multi-digit prefixes (E10xxx, E12xxx, E14xxx)
	if strings.HasPrefix(code, "E10") {
		return "redis"
	}
	if strings.HasPrefix(code, "E12") {
		return "image"
	}
	if strings.HasPrefix(code, "E14") {
		return "oauth"
	}
	switch code[1] {
	case '1':
		return "syntax"
	case '2':
		return "db"
	case '3':
		return "http"
	case '4':
		return "llm"
	case '5':
		return "fs"
	case '6':
		return "json"
	case '7':
		return "env"
	case '9':
		return "runtime"
	default:
		return "unknown"
	}
}
