package interpreter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/codong-lang/codong/stdlib/codongerror"
)

// HttpModuleObject is the singleton `http` module.
type HttpModuleObject struct{}

func (h *HttpModuleObject) Type() string    { return "module" }
func (h *HttpModuleObject) Inspect() string { return "<module:http>" }

var httpModuleSingleton = &HttpModuleObject{}

// HttpResponseObject wraps an HTTP response with lazy body methods.
type HttpResponseObject struct {
	Status  int
	Ok      bool
	Headers *MapObject
	RawBody []byte
}

func (r *HttpResponseObject) Type() string    { return "http_response" }
func (r *HttpResponseObject) Inspect() string { return fmt.Sprintf("<http_response %d>", r.Status) }

// evalHttpModuleMethod dispatches http.xxx() calls.
func (interp *Interpreter) evalHttpModuleMethod(method string) Object {
	return &BuiltinFunction{
		Name: "http." + method,
		Fn: func(i *Interpreter, args ...Object) Object {
			switch method {
			case "get":
				return i.httpRequest("GET", args)
			case "post":
				return i.httpRequest("POST", args)
			case "put":
				return i.httpRequest("PUT", args)
			case "patch":
				return i.httpRequest("PATCH", args)
			case "delete":
				return i.httpRequest("DELETE", args)
			case "request":
				return i.httpGenericRequest(args)
			default:
				return newRuntimeError(codongerror.E3009_SERVER_ERROR,
					fmt.Sprintf("unknown http method: %s", method), "")
			}
		},
	}
}

// httpRequest performs an HTTP request with the given method.
// http.get(url) or http.get(url, {headers: {...}})
// http.post(url, body) or http.post(url, body, {headers: {...}})
func (i *Interpreter) httpRequest(method string, args []Object) Object {
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			fmt.Sprintf("http.%s requires a URL", strings.ToLower(method)),
			fmt.Sprintf("http.%s(\"https://api.example.com/data\")", strings.ToLower(method)))
	}
	url, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "URL must be a string", "")
	}

	var bodyReader io.Reader
	var contentType string
	customHeaders := map[string]string{}
	timeout := 30 * time.Second

	// Parse body for POST/PUT/PATCH
	if method != "GET" && method != "DELETE" && len(args) > 1 {
		body := args[1]
		switch b := body.(type) {
		case *StringObject:
			bodyReader = strings.NewReader(b.Value)
			contentType = "text/plain"
		case *MapObject, *ListObject:
			goVal := objectToGoValue(body)
			jsonBytes, err := json.Marshal(goVal)
			if err != nil {
				return newRuntimeError(codongerror.E3009_SERVER_ERROR,
					"failed to serialize body to JSON", "")
			}
			bodyReader = bytes.NewReader(jsonBytes)
			contentType = "application/json"
		}
	}

	// Parse options (last arg if MapObject with headers/timeout)
	lastArg := args[len(args)-1]
	if opts, ok := lastArg.(*MapObject); ok {
		if h, exists := opts.Entries["headers"]; exists {
			if hm, ok := h.(*MapObject); ok {
				for k, v := range hm.Entries {
					if s, ok := v.(*StringObject); ok {
						customHeaders[k] = s.Value
					}
				}
			}
		}
		if t, exists := opts.Entries["timeout"]; exists {
			switch tv := t.(type) {
			case *NumberObject:
				timeout = time.Duration(tv.Value) * time.Second
			case *StringObject:
				if d, err := time.ParseDuration(tv.Value); err == nil {
					timeout = d
				}
			}
			if ct, exists := opts.Entries["content_type"]; exists {
				if s, ok := ct.(*StringObject); ok {
					contentType = s.Value
				}
			}
		}
	}

	// Create request
	req, err := http.NewRequest(method, url.Value, bodyReader)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E3005_CONN_FAILED,
			fmt.Sprintf("invalid URL or request: %s", err.Error()),
			codongerror.WithFix("check URL format"),
		)}
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("User-Agent", "Codong/0.1")
	for k, v := range customHeaders {
		req.Header.Set(k, v)
	}

	// Execute request
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") || strings.Contains(errStr, "context deadline") {
			return &ErrorObject{IsRuntime: false, Error: codongerror.New(
				codongerror.E3001_TIMEOUT,
				fmt.Sprintf("request timed out: %s", errStr),
				codongerror.WithFix("increase timeout or check network"),
				codongerror.WithRetry(true),
			)}
		}
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E3005_CONN_FAILED,
			fmt.Sprintf("connection failed: %s", errStr),
			codongerror.WithFix("check URL and network connectivity"),
		)}
	}
	defer resp.Body.Close()

	// Read body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E3009_SERVER_ERROR,
			fmt.Sprintf("failed to read response body: %s", err.Error()),
		)}
	}

	// Build response headers
	headerEntries := map[string]Object{}
	headerOrder := []string{}
	for k, v := range resp.Header {
		lk := strings.ToLower(k)
		headerEntries[lk] = &StringObject{Value: v[0]}
		headerOrder = append(headerOrder, lk)
	}

	return &HttpResponseObject{
		Status:  resp.StatusCode,
		Ok:      resp.StatusCode >= 200 && resp.StatusCode < 300,
		Headers: &MapObject{Entries: headerEntries, Order: headerOrder},
		RawBody: respBody,
	}
}

// httpGenericRequest handles http.request(method, url, options)
func (i *Interpreter) httpGenericRequest(args []Object) Object {
	if len(args) < 2 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"http.request requires (method, url)",
			"http.request(\"GET\", \"https://api.example.com\")")
	}
	method, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "method must be a string", "")
	}
	// Forward remaining args to httpRequest
	return i.httpRequest(strings.ToUpper(method.Value), args[1:])
}

// evalHttpResponseMemberAccess handles response.status, response.body, response.json(), etc.
func (i *Interpreter) evalHttpResponseMemberAccess(resp *HttpResponseObject, prop string) Object {
	switch prop {
	case "status":
		return &NumberObject{Value: float64(resp.Status)}
	case "ok":
		return nativeBoolToObject(resp.Ok)
	case "headers":
		return resp.Headers
	case "body":
		return &StringObject{Value: string(resp.RawBody)}
	case "text":
		return &BuiltinFunction{
			Name: "response.text",
			Fn: func(interp *Interpreter, args ...Object) Object {
				return &StringObject{Value: string(resp.RawBody)}
			},
		}
	case "json":
		return &BuiltinFunction{
			Name: "response.json",
			Fn: func(interp *Interpreter, args ...Object) Object {
				var data interface{}
				if err := json.Unmarshal(resp.RawBody, &data); err != nil {
					return &ErrorObject{IsRuntime: false, Error: codongerror.New(
						codongerror.E3009_SERVER_ERROR,
						fmt.Sprintf("failed to parse JSON: %s", err.Error()),
						codongerror.WithFix("response body is not valid JSON, use .text() instead"),
					)}
				}
				return goValueToObject(data)
			},
		}
	case "bytes":
		return &BuiltinFunction{
			Name: "response.bytes",
			Fn: func(interp *Interpreter, args ...Object) Object {
				elements := make([]Object, len(resp.RawBody))
				for idx, b := range resp.RawBody {
					elements[idx] = &NumberObject{Value: float64(b)}
				}
				return &ListObject{Elements: elements}
			},
		}
	}
	return NULL_OBJ
}
