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
	mux         *http.ServeMux // shared mux for dynamic route adding
	interp      *Interpreter
}

type webRoute struct {
	method  string // GET, POST, PUT, DELETE, PATCH
	pattern string // e.g., "/users/{id}"
	handler Object // FunctionObject
}

func (w *WebModuleObject) Type() string    { return "module" }
func (w *WebModuleObject) Inspect() string { return "<module:web>" }

var webModuleSingleton = &WebModuleObject{}

// ServerObject wraps an HTTP server (starts on WaitForServers).
type ServerObject struct {
	server *http.Server
	port   int
	done   chan error
}

func (s *ServerObject) Type() string    { return "server" }
func (s *ServerObject) Inspect() string { return "<server>" }

// GroupObject represents a route group with a URL prefix.
type GroupObject struct {
	server *ServerObject
	prefix string
}

func (g *GroupObject) Type() string    { return "group" }
func (g *GroupObject) Inspect() string { return "<group:" + g.prefix + ">" }

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
			case "middleware":
				// Return a namespace map with _type marker
				return &MapObject{
					Entries: map[string]Object{
						"_type": &StringObject{Value: "web_middleware_ns"},
					},
					Order: []string{"_type"},
				}
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
	// Convert :param to {param} for Go 1.22 ServeMux
	routePath := path.Value
	parts := strings.Split(routePath, "/")
	for idx, p := range parts {
		if strings.HasPrefix(p, ":") {
			parts[idx] = "{" + p[1:] + "}"
		}
	}
	routePath = strings.Join(parts, "/")

	handler := args[1]
	webModuleSingleton.mu.Lock()
	defer webModuleSingleton.mu.Unlock()
	webModuleSingleton.routes = append(webModuleSingleton.routes, webRoute{
		method:  method,
		pattern: routePath,
		handler: handler,
	})
	return NULL_OBJ
}

// webServe creates a server object. The server starts listening in WaitForServers()
// so routes can be registered after web.serve() returns.
func (i *Interpreter) webServe(args []Object) Object {
	port := 8080
	if len(args) > 0 {
		if n, ok := args[0].(*NumberObject); ok {
			port = int(n.Value)
		}
		if m, ok := args[len(args)-1].(*MapObject); ok {
			if p, exists := m.Entries["port"]; exists {
				if n, ok := p.(*NumberObject); ok {
					port = int(n.Value)
				}
			}
		}
	}

	srvObj := &ServerObject{
		port: port,
		done: make(chan error, 1),
	}

	webModuleSingleton.mu.Lock()
	webModuleSingleton.interp = i
	webModuleSingleton.mu.Unlock()

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
	queryMap := &MapObject{Entries: queryEntries, Order: queryOrder}

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
	paramMap := &MapObject{Entries: paramEntries, Order: paramOrder}
	// Support both req.param.id (map) and req.param("id") (function)
	// Store as map — user-defined function entries take priority in member access
	paramEntries["__map"] = paramMap // self-reference for debugging
	add("param", paramMap)

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

	// Add req.header(name) as a callable function
	add("header", &BuiltinFunction{Name: "req.header", Fn: func(interp *Interpreter, args ...Object) Object {
		if len(args) == 0 {
			return entries["headers"]
		}
		if name, ok := args[0].(*StringObject); ok {
			hm := entries["headers"].(*MapObject)
			key := strings.ToLower(name.Value)
			if v, exists := hm.Entries[key]; exists {
				return v
			}
		}
		return NULL_OBJ
	}})

	// Add req.query(name) as a callable function
	add("query", &BuiltinFunction{Name: "req.query", Fn: func(interp *Interpreter, args ...Object) Object {
		qm := queryMap
		if len(args) == 0 {
			return qm
		}
		if name, ok := args[0].(*StringObject); ok {
			if v, exists := qm.Entries[name.Value]; exists {
				return v
			}
		}
		return NULL_OBJ
	}})

	// Add req.query_all() — returns all query params as map
	add("query_all", &BuiltinFunction{Name: "req.query_all", Fn: func(interp *Interpreter, args ...Object) Object {
		return queryMap
	}})

	// Add req.context (for middleware like auth_bearer)
	ctxEntries := map[string]Object{}
	ctxOrder := []string{}
	for k, vs := range r.Header {
		if strings.HasPrefix(k, "X-Codong-Auth-") {
			field := strings.ToLower(k[len("X-Codong-Auth-"):])
			ctxEntries[field] = &StringObject{Value: vs[0]}
			ctxOrder = append(ctxOrder, field)
		}
	}
	add("context", &MapObject{Entries: ctxEntries, Order: ctxOrder})

	// Client IP
	ip := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ip = fwd
	}
	add("ip", &StringObject{Value: ip})

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

	// Optional status code: web.json(data, 201) or web.json(data, status: 201)
	entries["status"] = &NumberObject{Value: 200}
	order = append(order, "status")
	if len(args) > 1 {
		// Second positional arg is status number
		if n, ok := args[1].(*NumberObject); ok {
			entries["status"] = n
		}
		// Named args in trailing map
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
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT, "web.response requires (data, status, headers)", "")
	}
	// web.response(data, status?, headers?)
	data := args[0]
	status := 200
	entries := map[string]Object{
		"_type": &StringObject{Value: "json"},
		"data":  data,
	}
	order := []string{"_type", "data", "status"}
	if len(args) > 1 {
		if n, ok := args[1].(*NumberObject); ok {
			status = int(n.Value)
		}
	}
	entries["status"] = &NumberObject{Value: float64(status)}
	if len(args) > 2 {
		if h, ok := args[2].(*MapObject); ok {
			entries["headers"] = h
			order = append(order, "headers")
		}
	}
	return &MapObject{Entries: entries, Order: order}
}

// getMiddlewareFactory returns a middleware constructor function.
func (i *Interpreter) getMiddlewareFactory(name string) Object {
	switch name {
	case "cors":
		return &BuiltinFunction{Name: "web.middleware.cors", Fn: func(interp *Interpreter, args ...Object) Object {
			return &MapObject{
				Entries: map[string]Object{"_mw_type": &StringObject{Value: "cors"}},
				Order:   []string{"_mw_type"},
			}
		}}
	case "logger":
		return &BuiltinFunction{Name: "web.middleware.logger", Fn: func(interp *Interpreter, args ...Object) Object {
			return &MapObject{
				Entries: map[string]Object{"_mw_type": &StringObject{Value: "logger"}},
				Order:   []string{"_mw_type"},
			}
		}}
	case "recover":
		return &BuiltinFunction{Name: "web.middleware.recover", Fn: func(interp *Interpreter, args ...Object) Object {
			return &MapObject{
				Entries: map[string]Object{"_mw_type": &StringObject{Value: "recover"}},
				Order:   []string{"_mw_type"},
			}
		}}
	case "auth_bearer":
		return &BuiltinFunction{Name: "web.middleware.auth_bearer", Fn: func(interp *Interpreter, args ...Object) Object {
			var validator Object
			if len(args) > 0 {
				validator = args[0]
			}
			return &MapObject{
				Entries: map[string]Object{
					"_mw_type":  &StringObject{Value: "auth_bearer"},
					"validator": validator,
				},
				Order: []string{"_mw_type", "validator"},
			}
		}}
	}
	return NULL_OBJ
}

// applyMiddlewares wraps an http.Handler with registered middlewares.
func (i *Interpreter) applyMiddlewares(handler http.Handler) http.Handler {
	webModuleSingleton.mu.Lock()
	middlewares := make([]Object, len(webModuleSingleton.middlewares))
	copy(middlewares, webModuleSingleton.middlewares)
	webModuleSingleton.mu.Unlock()

	h := handler
	for idx := len(middlewares) - 1; idx >= 0; idx-- {
		mw := middlewares[idx]
		if m, ok := mw.(*MapObject); ok {
			if t, exists := m.Entries["_mw_type"]; exists {
				if ts, ok := t.(*StringObject); ok {
					switch ts.Value {
					case "cors":
						prev := h
						h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							w.Header().Set("Access-Control-Allow-Origin", "*")
							w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
							w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
							if r.Method == "OPTIONS" {
								w.WriteHeader(204)
								return
							}
							prev.ServeHTTP(w, r)
						})
					case "logger":
						prev := h
						h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							fmt.Fprintf(os.Stderr, "%s %s\n", r.Method, r.URL.Path)
							prev.ServeHTTP(w, r)
						})
					case "recover":
						prev := h
						h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							defer func() {
								if err := recover(); err != nil {
									w.Header().Set("Content-Type", "application/json")
									w.WriteHeader(500)
									fmt.Fprintf(w, `{"error":"internal server error"}`)
								}
							}()
							prev.ServeHTTP(w, r)
						})
					case "auth_bearer":
						validator := m.Entries["validator"]
						prev := h
						h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							auth := r.Header.Get("Authorization")
							if !strings.HasPrefix(auth, "Bearer ") {
								w.WriteHeader(401)
								fmt.Fprint(w, `{"error":"unauthorized"}`)
								return
							}
							token := auth[7:]
							// Call validator function
							i.mu.Lock()
							result := i.applyFunction(validator, []Object{&StringObject{Value: token}})
							i.mu.Unlock()
							if result == nil || result == NULL_OBJ || result == FALSE_OBJ {
								w.WriteHeader(401)
								fmt.Fprint(w, `{"error":"unauthorized"}`)
								return
							}
							// Store auth result in request context header
							if rm, ok := result.(*MapObject); ok {
								for k, v := range rm.Entries {
									if s, ok := v.(*StringObject); ok {
										r.Header.Set("X-Codong-Auth-"+k, s.Value)
									}
								}
							}
							prev.ServeHTTP(w, r)
						})
					}
				}
			}
		}
	}
	return h
}

// WaitForServers builds muxes, registers all routes, starts listening, then blocks.
func (i *Interpreter) WaitForServers() {
	i.mu.Lock()
	servers := make([]*ServerObject, len(i.servers))
	copy(servers, i.servers)
	i.mu.Unlock()

	if len(servers) == 0 {
		return
	}

	// Build mux with all registered routes
	webModuleSingleton.mu.Lock()
	routes := make([]webRoute, len(webModuleSingleton.routes))
	copy(routes, webModuleSingleton.routes)
	webModuleSingleton.mu.Unlock()

	for _, srv := range servers {
		mux := http.NewServeMux()
		for _, r := range routes {
			pattern := fmt.Sprintf("%s %s", r.method, r.pattern)
			handler := r.handler
			routePattern := r.pattern
			mux.HandleFunc(pattern, i.codongHandlerToHTTP(handler, routePattern))
		}

		addr := fmt.Sprintf(":%d", srv.port)
		finalHandler := i.applyMiddlewares(mux)
		srv.server = &http.Server{Addr: addr, Handler: finalHandler}

		go func(s *ServerObject, a string) {
			fmt.Fprintf(os.Stderr, "Codong server listening on http://localhost%s\n", a)
			if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				s.done <- err
			}
			close(s.done)
		}(srv, addr)
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
			if srv.server != nil {
				srv.server.Shutdown(ctx)
			}
		}
	case err := <-servers[0].done:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		}
	}
}
