package interpreter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/codong-lang/codong/stdlib/codongerror"
)

// WebModuleObject is the singleton `web` module.
type WebModuleObject struct {
	mu          sync.Mutex
	routes      []webRoute
	middlewares []Object
}

type webRoute struct {
	method  string // GET, POST, PUT, DELETE, PATCH
	pattern string // e.g., "/users/{id}"
	handler Object // FunctionObject
}

func (w *WebModuleObject) Type() string    { return "module" }
func (w *WebModuleObject) Inspect() string { return "<module:web>" }

var webModuleSingleton = &WebModuleObject{}

// ServerObject wraps a running HTTP server.
type ServerObject struct {
	server *http.Server
	done   chan error
}

func (s *ServerObject) Type() string    { return "server" }
func (s *ServerObject) Inspect() string { return "<server>" }

// evalWebModuleMethod dispatches web.xxx() calls.
func (interp *Interpreter) evalWebModuleMethod(method string) Object {
	return &BuiltinFunction{
		Name: "web." + method,
		Fn: func(i *Interpreter, args ...Object) Object {
			switch method {
			case "get", "post", "put", "delete", "patch":
				return i.webRegisterRoute(strings.ToUpper(method), args)
			case "serve":
				return i.webServe(args)
			case "json":
				return i.webResponseBuilder("json", args)
			case "text":
				return i.webResponseBuilder("text", args)
			case "html":
				return i.webResponseBuilder("html", args)
			case "redirect":
				return i.webRedirect(args)
			case "file":
				return i.webFile(args)
			case "use":
				return i.webUse(args)
			case "response":
				return i.webCustomResponse(args)
			default:
				return newRuntimeError(codongerror.E3003_ROUTE_ERROR,
					fmt.Sprintf("unknown web method: %s", method), "")
			}
		},
	}
}

// webRegisterRoute registers a route handler.
func (i *Interpreter) webRegisterRoute(method string, args []Object) Object {
	if len(args) < 2 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			fmt.Sprintf("web.%s requires (path, handler)", strings.ToLower(method)),
			fmt.Sprintf("web.%s(\"/path\", fn(req) => web.json({ok: true}))", strings.ToLower(method)))
	}
	path, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "path must be a string", "")
	}
	handler := args[1]
	webModuleSingleton.mu.Lock()
	defer webModuleSingleton.mu.Unlock()
	webModuleSingleton.routes = append(webModuleSingleton.routes, webRoute{
		method:  method,
		pattern: path.Value,
		handler: handler,
	})
	return NULL_OBJ
}

// webServe starts the HTTP server.
func (i *Interpreter) webServe(args []Object) Object {
	port := 8080
	// Check positional arg
	if len(args) > 0 {
		if n, ok := args[0].(*NumberObject); ok {
			port = int(n.Value)
		}
		// Check named args (trailing MapObject)
		if m, ok := args[len(args)-1].(*MapObject); ok {
			if p, exists := m.Entries["port"]; exists {
				if n, ok := p.(*NumberObject); ok {
					port = int(n.Value)
				}
			}
		}
	}

	mux := http.NewServeMux()
	webModuleSingleton.mu.Lock()
	routes := make([]webRoute, len(webModuleSingleton.routes))
	copy(routes, webModuleSingleton.routes)
	webModuleSingleton.mu.Unlock()

	for _, r := range routes {
		pattern := fmt.Sprintf("%s %s", r.method, r.pattern)
		handler := r.handler
		routePattern := r.pattern
		mux.HandleFunc(pattern, i.codongHandlerToHTTP(handler, routePattern))
	}

	addr := fmt.Sprintf(":%d", port)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	srvObj := &ServerObject{
		server: server,
		done:   make(chan error, 1),
	}

	// Start server in goroutine
	go func() {
		fmt.Fprintf(os.Stderr, "Codong server listening on http://localhost%s\n", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvObj.done <- err
		}
		close(srvObj.done)
	}()

	i.mu.Lock()
	i.servers = append(i.servers, srvObj)
	i.mu.Unlock()

	return srvObj
}

// codongHandlerToHTTP adapts a Codong function to an http.HandlerFunc.
func (i *Interpreter) codongHandlerToHTTP(handler Object, pattern string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Build request MapObject
		reqMap := i.buildRequestObject(r, pattern)

		// Serialize interpreter access
		i.mu.Lock()
		env := NewEnvironment()
		result := i.applyFunction(handler, []Object{reqMap})
		i.mu.Unlock()
		_ = env

		// Write response
		i.writeHTTPResponse(w, result)
	}
}

// buildRequestObject creates a Codong MapObject from an HTTP request.
func (i *Interpreter) buildRequestObject(r *http.Request, pattern string) *MapObject {
	entries := map[string]Object{}
	order := []string{}

	add := func(key string, val Object) {
		entries[key] = val
		order = append(order, key)
	}

	add("method", &StringObject{Value: r.Method})
	add("path", &StringObject{Value: r.URL.Path})
	add("url", &StringObject{Value: r.URL.String()})

	// Query parameters
	queryEntries := map[string]Object{}
	queryOrder := []string{}
	for k, v := range r.URL.Query() {
		queryEntries[k] = &StringObject{Value: v[0]}
		queryOrder = append(queryOrder, k)
	}
	add("query", &MapObject{Entries: queryEntries, Order: queryOrder})

	// Headers
	headerEntries := map[string]Object{}
	headerOrder := []string{}
	for k, v := range r.Header {
		headerEntries[strings.ToLower(k)] = &StringObject{Value: v[0]}
		headerOrder = append(headerOrder, strings.ToLower(k))
	}
	add("headers", &MapObject{Entries: headerEntries, Order: headerOrder})

	// Path parameters (Go 1.22 PathValue)
	paramEntries := map[string]Object{}
	paramOrder := []string{}
	// Extract param names from pattern like /users/{id}
	parts := strings.Split(pattern, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := part[1 : len(part)-1]
			val := r.PathValue(name)
			paramEntries[name] = &StringObject{Value: val}
			paramOrder = append(paramOrder, name)
		}
	}
	add("param", &MapObject{Entries: paramEntries, Order: paramOrder})

	// Body
	if r.Body != nil {
		bodyBytes, err := io.ReadAll(r.Body)
		if err == nil && len(bodyBytes) > 0 {
			bodyStr := string(bodyBytes)
			// Try to parse as JSON
			var jsonData interface{}
			if json.Unmarshal(bodyBytes, &jsonData) == nil {
				add("body", goValueToObject(jsonData))
			} else {
				add("body", &StringObject{Value: bodyStr})
			}
		} else {
			add("body", NULL_OBJ)
		}
	} else {
		add("body", NULL_OBJ)
	}

	return &MapObject{Entries: entries, Order: order}
}

// writeHTTPResponse writes an HTTP response based on the Codong return value.
func (i *Interpreter) writeHTTPResponse(w http.ResponseWriter, result Object) {
	switch r := result.(type) {
	case *MapObject:
		respType := ""
		if t, ok := r.Entries["_type"]; ok {
			if s, ok := t.(*StringObject); ok {
				respType = s.Value
			}
		}
		status := 200
		if s, ok := r.Entries["status"]; ok {
			if n, ok := s.(*NumberObject); ok {
				status = int(n.Value)
			}
		}
		// Set custom headers if present
		if h, ok := r.Entries["headers"]; ok {
			if hm, ok := h.(*MapObject); ok {
				for k, v := range hm.Entries {
					if s, ok := v.(*StringObject); ok {
						w.Header().Set(k, s.Value)
					}
				}
			}
		}

		switch respType {
		case "json":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			data := r.Entries["data"]
			if data != nil {
				goVal := objectToGoValue(data)
				jsonBytes, _ := json.Marshal(goVal)
				w.Write(jsonBytes)
			}
		case "text":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(status)
			if body, ok := r.Entries["body"]; ok {
				fmt.Fprint(w, body.Inspect())
			}
		case "html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(status)
			if body, ok := r.Entries["body"]; ok {
				if s, ok := body.(*StringObject); ok {
					fmt.Fprint(w, s.Value)
				}
			}
		case "redirect":
			url := ""
			if u, ok := r.Entries["url"]; ok {
				if s, ok := u.(*StringObject); ok {
					url = s.Value
				}
			}
			http.Redirect(w, nil, url, status)
		case "file":
			path := ""
			if p, ok := r.Entries["path"]; ok {
				if s, ok := p.(*StringObject); ok {
					path = s.Value
				}
			}
			http.ServeFile(w, nil, path)
		default:
			// Plain map → JSON response
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			goVal := objectToGoValue(r)
			jsonBytes, _ := json.Marshal(goVal)
			w.Write(jsonBytes)
		}
	case *StringObject:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(r.Value))
	case *ErrorObject:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		fmt.Fprintf(w, `{"error":"%s","message":"%s"}`, r.Error.Code, r.Error.Message)
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if result != nil {
			w.Write([]byte(result.Inspect()))
		}
	}
}

// Response builder helpers
func (i *Interpreter) webResponseBuilder(respType string, args []Object) Object {
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			fmt.Sprintf("web.%s requires at least 1 argument", respType), "")
	}

	entries := map[string]Object{
		"_type": &StringObject{Value: respType},
	}
	order := []string{"_type"}

	switch respType {
	case "json":
		entries["data"] = args[0]
		order = append(order, "data")
	case "text", "html":
		entries["body"] = args[0]
		order = append(order, "body")
	}

	// Optional status code from named args (trailing map)
	entries["status"] = &NumberObject{Value: 200}
	order = append(order, "status")
	if len(args) > 1 {
		if m, ok := args[len(args)-1].(*MapObject); ok {
			if s, exists := m.Entries["status"]; exists {
				entries["status"] = s
			}
			if h, exists := m.Entries["headers"]; exists {
				entries["headers"] = h
				order = append(order, "headers")
			}
		}
	}

	return &MapObject{Entries: entries, Order: order}
}

func (i *Interpreter) webRedirect(args []Object) Object {
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT, "web.redirect requires a URL", "")
	}
	url, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "redirect URL must be a string", "")
	}
	status := 302
	if len(args) > 1 {
		if n, ok := args[1].(*NumberObject); ok {
			status = int(n.Value)
		}
	}
	return &MapObject{
		Entries: map[string]Object{
			"_type":  &StringObject{Value: "redirect"},
			"url":    url,
			"status": &NumberObject{Value: float64(status)},
		},
		Order: []string{"_type", "url", "status"},
	}
}

func (i *Interpreter) webFile(args []Object) Object {
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT, "web.file requires a path", "")
	}
	path, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "file path must be a string", "")
	}
	return &MapObject{
		Entries: map[string]Object{
			"_type":  &StringObject{Value: "file"},
			"path":   path,
			"status": &NumberObject{Value: 200},
		},
		Order: []string{"_type", "path", "status"},
	}
}

func (i *Interpreter) webUse(args []Object) Object {
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT, "web.use requires a middleware", "")
	}
	webModuleSingleton.mu.Lock()
	defer webModuleSingleton.mu.Unlock()
	webModuleSingleton.middlewares = append(webModuleSingleton.middlewares, args[0])
	return NULL_OBJ
}

func (i *Interpreter) webCustomResponse(args []Object) Object {
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT, "web.response requires (status, body)", "")
	}
	status := 200
	if n, ok := args[0].(*NumberObject); ok {
		status = int(n.Value)
	}
	var body Object = NULL_OBJ
	if len(args) > 1 {
		body = args[1]
	}
	return &MapObject{
		Entries: map[string]Object{
			"_type":  &StringObject{Value: "text"},
			"status": &NumberObject{Value: float64(status)},
			"body":   body,
		},
		Order: []string{"_type", "status", "body"},
	}
}

// WaitForServers blocks until all web servers are shut down or SIGINT is received.
func (i *Interpreter) WaitForServers() {
	i.mu.Lock()
	servers := make([]*ServerObject, len(i.servers))
	copy(servers, i.servers)
	i.mu.Unlock()

	if len(servers) == 0 {
		return
	}

	// Wait for SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		fmt.Fprintln(os.Stderr, "\nShutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, srv := range servers {
			srv.server.Shutdown(ctx)
		}
	case err := <-servers[0].done:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		}
	}
}
