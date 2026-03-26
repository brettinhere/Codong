package interpreter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/codong-lang/codong/stdlib/codongerror"
)

// --- MIME type table for static file serving ---
var defaultMIMETypes = map[string]string{
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
			case "static":
				return i.webStatic(args)
			case "set_cookie":
				return i.webSetCookie(args)
			case "delete_cookie":
				return i.webDeleteCookie(args)
			case "middleware":
				// Return a namespace map with _type marker
				return &MapObject{
					Entries: map[string]Object{
						"_type": &StringObject{Value: "web_middleware_ns"},
					},
					Order: []string{"_type"},
				}
			default:
				return newRuntimeError(codongerror.E3007_ROUTE_ERROR,
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

	fmt.Fprintf(os.Stderr, "web.serve(port:%d) — eval mode: server will start, use `codong run` for production\n", port)

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

	// Cookies — req.cookie("name") and req.cookies
	cookieEntries := map[string]Object{}
	cookieOrder := []string{}
	for _, c := range r.Cookies() {
		cookieEntries[c.Name] = &StringObject{Value: c.Value}
		cookieOrder = append(cookieOrder, c.Name)
	}
	add("cookies", &MapObject{Entries: cookieEntries, Order: cookieOrder})

	add("cookie", &BuiltinFunction{Name: "req.cookie", Fn: func(interp *Interpreter, args ...Object) Object {
		if len(args) > 0 {
			if name, ok := args[0].(*StringObject); ok {
				if v, exists := cookieEntries[name.Value]; exists {
					return v
				}
			}
		}
		return NULL_OBJ
	}})

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

	// Body — handle multipart/form-data and regular body
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Multipart file upload: stream files to temp, parse text fields
		opts := multipartOptions{
			maxFileSize:  10 * 1024 * 1024, // 10MB default
			maxTotalSize: 200 * 1024 * 1024,
		}
		files, formFields, err := parseMultipartRequest(r, opts)
		if err != nil {
			add("body", NULL_OBJ)
			add("files", &MapObject{Entries: map[string]Object{}, Order: []string{}})
		} else {
			// Set body from text fields
			if len(formFields) > 0 {
				bodyEntries := map[string]Object{}
				bodyOrder := []string{}
				for k, v := range formFields {
					bodyEntries[k] = v
					bodyOrder = append(bodyOrder, k)
				}
				add("body", &MapObject{Entries: bodyEntries, Order: bodyOrder})
			} else {
				add("body", NULL_OBJ)
			}

			// Build files map with .get() method
			filesEntries := map[string]Object{}
			filesOrder := []string{}
			for field, uploadedFiles := range files {
				if len(uploadedFiles) > 0 {
					filesEntries[field] = uploadedFileToObject(uploadedFiles[0])
					filesOrder = append(filesOrder, field)
				}
			}
			// Add get() function
			filesEntries["get"] = &BuiltinFunction{Name: "req.files.get", Fn: func(interp *Interpreter, args ...Object) Object {
				if len(args) > 0 {
					if name, ok := args[0].(*StringObject); ok {
						if v, exists := filesEntries[name.Value]; exists {
							return v
						}
					}
				}
				return NULL_OBJ
			}}
			filesOrder = append(filesOrder, "get")
			add("files", &MapObject{Entries: filesEntries, Order: filesOrder})

			// files_list(field) — returns list of files for a field
			add("files_list", &BuiltinFunction{Name: "req.files_list", Fn: func(interp *Interpreter, args ...Object) Object {
				if len(args) > 0 {
					if name, ok := args[0].(*StringObject); ok {
						if ufs, exists := files[name.Value]; exists {
							elems := make([]Object, len(ufs))
							for idx, uf := range ufs {
								elems[idx] = uploadedFileToObject(uf)
							}
							return &ListObject{Elements: elems}
						}
					}
				}
				return &ListObject{Elements: []Object{}}
			}})

			// files_all() — returns all uploaded files as a list
			add("files_all", &BuiltinFunction{Name: "req.files_all", Fn: func(interp *Interpreter, args ...Object) Object {
				var elems []Object
				for _, ufs := range files {
					for _, uf := range ufs {
						elems = append(elems, uploadedFileToObject(uf))
					}
				}
				if elems == nil {
					elems = []Object{}
				}
				return &ListObject{Elements: elems}
			}})

			// Schedule temp file cleanup after request
			// (files not moved by fs.move() will be cleaned up)
			defer func() {
				for _, ufs := range files {
					for _, uf := range ufs {
						os.Remove(uf.TmpPath) // best-effort cleanup
					}
				}
			}()
		}
	} else if r.Body != nil {
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
		// Third positional arg is headers map (e.g., from web.set_cookie)
		if len(args) > 2 {
			if h, ok := args[2].(*MapObject); ok {
				entries["headers"] = h
				order = append(order, "headers")
			}
		}
		// Named args in trailing map (for status: and headers: named params)
		if m, ok := args[len(args)-1].(*MapObject); ok {
			if s, exists := m.Entries["status"]; exists {
				entries["status"] = s
			}
			if h, exists := m.Entries["headers"]; exists {
				entries["headers"] = h
				if _, alreadyHas := entries["headers"]; !alreadyHas {
					order = append(order, "headers")
				}
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
	// Support server.use("/prefix", middleware)
	if len(args) >= 2 {
		if prefix, ok := args[0].(*StringObject); ok {
			mw := args[1]
			// If it's a static middleware, set its prefix
			if m, ok := mw.(*MapObject); ok {
				if t, exists := m.Entries["_mw_type"]; exists {
					if ts, ok := t.(*StringObject); ok && ts.Value == "static" {
						m.Entries["prefix"] = prefix
						if _, exists := m.Entries["prefix"]; !exists {
							m.Order = append(m.Order, "prefix")
						}
					}
				}
			}
			webModuleSingleton.middlewares = append(webModuleSingleton.middlewares, mw)
			return NULL_OBJ
		}
	}
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
	case "multipart":
		return &BuiltinFunction{Name: "web.middleware.multipart", Fn: func(interp *Interpreter, args ...Object) Object {
			return &MapObject{
				Entries: map[string]Object{"_mw_type": &StringObject{Value: "multipart"}},
				Order:   []string{"_mw_type"},
			}
		}}
	case "session":
		return &BuiltinFunction{Name: "web.middleware.session", Fn: func(interp *Interpreter, args ...Object) Object {
			return &MapObject{
				Entries: map[string]Object{"_mw_type": &StringObject{Value: "session"}},
				Order:   []string{"_mw_type"},
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
					case "static":
					h = i.staticMiddleware(m, h)
				case "multipart":
					// multipart config is applied at request parse time
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

		// Register WebSocket routes
		for _, ws := range wsRoutes {
			wsHandler := ws.handler
			mux.HandleFunc(ws.pattern, i.wsUpgradeHandler(wsHandler))
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

// ============================================================
// P1-1: web.static() — Static file serving
// ============================================================

// webStatic creates a static file middleware object.
func (i *Interpreter) webStatic(args []Object) Object {
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"web.static requires a directory path", "web.static(\"./public\")")
	}
	rootDir, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "path must be a string", "")
	}

	entries := map[string]Object{
		"_mw_type": &StringObject{Value: "static"},
		"root_dir": rootDir,
	}
	order := []string{"_mw_type", "root_dir"}

	// Default options
	entries["spa"] = FALSE_OBJ
	entries["index"] = &StringObject{Value: "index.html"}
	entries["max_age"] = &NumberObject{Value: 0}
	entries["dotfiles"] = &StringObject{Value: "deny"}
	entries["etag"] = TRUE_OBJ
	order = append(order, "spa", "index", "max_age", "dotfiles", "etag")

	// Parse options from second arg
	if len(args) > 1 {
		if opts, ok := args[1].(*MapObject); ok {
			if v, exists := opts.Entries["spa"]; exists {
				entries["spa"] = v
			}
			if v, exists := opts.Entries["index"]; exists {
				entries["index"] = v
			}
			if v, exists := opts.Entries["max_age"]; exists {
				entries["max_age"] = v
			}
			if v, exists := opts.Entries["dotfiles"]; exists {
				entries["dotfiles"] = v
			}
			if v, exists := opts.Entries["etag"]; exists {
				entries["etag"] = v
			}
		}
	}

	return &MapObject{Entries: entries, Order: order}
}

// staticMiddleware creates an http.Handler for serving static files.
func (i *Interpreter) staticMiddleware(m *MapObject, next http.Handler) http.Handler {
	rootDirStr := ""
	if rd, ok := m.Entries["root_dir"].(*StringObject); ok {
		rootDirStr = rd.Value
	}
	prefix := ""
	if p, ok := m.Entries["prefix"].(*StringObject); ok {
		prefix = p.Value
	}
	spa := false
	if s, ok := m.Entries["spa"].(*BoolObject); ok {
		spa = s.Value
	}
	indexFile := "index.html"
	if idx, ok := m.Entries["index"].(*StringObject); ok {
		indexFile = idx.Value
	}
	maxAge := 0
	if ma, ok := m.Entries["max_age"].(*NumberObject); ok {
		maxAge = int(ma.Value)
	}
	dotfiles := "deny"
	if df, ok := m.Entries["dotfiles"].(*StringObject); ok {
		dotfiles = df.Value
	}
	useETag := true
	if et, ok := m.Entries["etag"].(*BoolObject); ok {
		useETag = et.Value
	}

	// Resolve root directory
	root := rootDirStr
	if !filepath.IsAbs(root) {
		root = filepath.Join(i.fsWorkDir(), root)
	}
	root = filepath.Clean(root)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only handle GET and HEAD
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}

		// Try API routes first — if the mux has a handler (non-404), use it
		rec := &responseRecorder{code: 200, header: make(http.Header)}
		next.ServeHTTP(rec, r)
		if rec.code != 404 {
			// API route matched — write recorded response
			for k, vals := range rec.header {
				for _, v := range vals { w.Header().Add(k, v) }
			}
			w.WriteHeader(rec.code)
			w.Write(rec.body)
			return
		}

		urlPath := r.URL.Path

		// Strip prefix
		if prefix != "" {
			if !strings.HasPrefix(urlPath, prefix) {
				next.ServeHTTP(w, r)
				return
			}
			urlPath = strings.TrimPrefix(urlPath, prefix)
			if urlPath == "" {
				urlPath = "/"
			}
		}

		// Resolve file path
		fsPath := filepath.Join(root, filepath.FromSlash(urlPath))
		fsPath = filepath.Clean(fsPath)

		// Path traversal protection
		if !strings.HasPrefix(fsPath, root) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		// Dotfiles handling
		if dotfiles != "allow" {
			base := filepath.Base(fsPath)
			if strings.HasPrefix(base, ".") && base != "." {
				if dotfiles == "deny" {
					http.Error(w, "forbidden", http.StatusForbidden)
				} else {
					next.ServeHTTP(w, r)
				}
				return
			}
		}

		// Check if file exists
		info, err := os.Stat(fsPath)
		if err != nil {
			if os.IsNotExist(err) {
				// SPA fallback
				if spa {
					indexPath := filepath.Join(root, indexFile)
					if indexInfo, err2 := os.Stat(indexPath); err2 == nil && !indexInfo.IsDir() {
						serveStaticFile(w, r, indexPath, indexInfo, maxAge, useETag)
						return
					}
				}
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Directory → try index file
		if info.IsDir() {
			indexPath := filepath.Join(fsPath, indexFile)
			if indexInfo, err2 := os.Stat(indexPath); err2 == nil && !indexInfo.IsDir() {
				serveStaticFile(w, r, indexPath, indexInfo, maxAge, useETag)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		serveStaticFile(w, r, fsPath, info, maxAge, useETag)
	})
}

// responseRecorder captures the response from an http.Handler to check status code
type responseRecorder struct {
	code   int
	header http.Header
	body   []byte
}

func (r *responseRecorder) Header() http.Header         { return r.header }
func (r *responseRecorder) WriteHeader(code int)         { r.code = code }
func (r *responseRecorder) Write(b []byte) (int, error)  { r.body = append(r.body, b...); return len(b), nil }

func serveStaticFile(w http.ResponseWriter, r *http.Request, path string, info os.FileInfo, maxAge int, useETag bool) {
	ext := strings.ToLower(filepath.Ext(path))
	ct := "application/octet-stream"
	if mime, ok := defaultMIMETypes[ext]; ok {
		ct = mime
	}
	w.Header().Set("Content-Type", ct)

	if maxAge > 0 {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", maxAge))
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	if useETag {
		etag := fmt.Sprintf(`"%x-%x"`, info.ModTime().UnixNano(), info.Size())
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	// Serve file directly (avoid http.ServeFile which adds redirect logic)
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "internal server error", 500)
		return
	}
	defer f.Close()
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

// ============================================================
// P1-2: web Cookie support
// ============================================================

// webSetCookie creates a Set-Cookie header map for use in responses.
func (i *Interpreter) webSetCookie(args []Object) Object {
	if len(args) < 2 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"web.set_cookie requires (name, value)", "web.set_cookie(\"session\", \"abc123\", {http_only: true})")
	}
	name := ""
	if s, ok := args[0].(*StringObject); ok {
		name = s.Value
	}
	value := ""
	if s, ok := args[1].(*StringObject); ok {
		value = s.Value
	}

	// Build Set-Cookie header
	parts := []string{fmt.Sprintf("%s=%s", name, value)}
	path := "/"
	httpOnly := true
	secure := false
	sameSite := "Lax"
	cookieMaxAge := 0

	if len(args) > 2 {
		if opts, ok := args[2].(*MapObject); ok {
			if v, exists := opts.Entries["path"]; exists {
				if s, ok := v.(*StringObject); ok {
					path = s.Value
				}
			}
			if v, exists := opts.Entries["domain"]; exists {
				if s, ok := v.(*StringObject); ok && s.Value != "" {
					parts = append(parts, "Domain="+s.Value)
				}
			}
			if v, exists := opts.Entries["max_age"]; exists {
				if n, ok := v.(*NumberObject); ok {
					cookieMaxAge = int(n.Value)
				}
			}
			if v, exists := opts.Entries["http_only"]; exists {
				if b, ok := v.(*BoolObject); ok {
					httpOnly = b.Value
				}
			}
			if v, exists := opts.Entries["secure"]; exists {
				if b, ok := v.(*BoolObject); ok {
					secure = b.Value
				}
			}
			if v, exists := opts.Entries["same_site"]; exists {
				if s, ok := v.(*StringObject); ok {
					sameSite = s.Value
				}
			}
		}
	}

	parts = append(parts, "Path="+path)
	if cookieMaxAge > 0 {
		parts = append(parts, fmt.Sprintf("Max-Age=%d", cookieMaxAge))
	}
	if httpOnly {
		parts = append(parts, "HttpOnly")
	}
	if secure {
		parts = append(parts, "Secure")
	}
	switch strings.ToLower(sameSite) {
	case "strict":
		parts = append(parts, "SameSite=Strict")
	case "none":
		parts = append(parts, "SameSite=None")
	default:
		parts = append(parts, "SameSite=Lax")
	}

	headerValue := strings.Join(parts, "; ")

	// Merge with existing headers if a 4th arg is passed
	entries := map[string]Object{}
	order := []string{}
	if len(args) > 3 {
		if existing, ok := args[3].(*MapObject); ok {
			for k, v := range existing.Entries {
				entries[k] = v
			}
			order = append(order, existing.Order...)
		}
	}
	entries["Set-Cookie"] = &StringObject{Value: headerValue}
	if _, exists := entries["Set-Cookie"]; !exists {
		order = append(order, "Set-Cookie")
	} else {
		found := false
		for _, k := range order {
			if k == "Set-Cookie" {
				found = true
				break
			}
		}
		if !found {
			order = append(order, "Set-Cookie")
		}
	}

	return &MapObject{Entries: entries, Order: order}
}

// webDeleteCookie creates a Set-Cookie header that deletes a cookie.
func (i *Interpreter) webDeleteCookie(args []Object) Object {
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"web.delete_cookie requires a name", "web.delete_cookie(\"session\")")
	}
	name := ""
	if s, ok := args[0].(*StringObject); ok {
		name = s.Value
	}
	headerValue := fmt.Sprintf("%s=; Path=/; Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:00 GMT", name)
	return &MapObject{
		Entries: map[string]Object{
			"Set-Cookie": &StringObject{Value: headerValue},
		},
		Order: []string{"Set-Cookie"},
	}
}

// ============================================================
// P1-3: web.multipart — File upload
// ============================================================

type multipartOptions struct {
	maxFileSize  int64
	maxTotalSize int64
	tmpDir       string
}

// parseMultipartRequest parses multipart form data from an HTTP request.
// Files are streamed to temporary files, not loaded into memory.
func parseMultipartRequest(r *http.Request, opts multipartOptions) (map[string][]*uploadedFile, map[string]Object, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, fmt.Errorf("multipart parse failed: %w", err)
	}

	files := make(map[string][]*uploadedFile)
	formFields := make(map[string]Object)
	var totalSize int64

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}

		fieldName := part.FormName()
		filename := part.FileName()

		if filename == "" {
			// Text field — read value
			data, err := io.ReadAll(io.LimitReader(part, 1<<20)) // 1MB max for text fields
			if err == nil {
				formFields[fieldName] = &StringObject{Value: string(data)}
			}
			part.Close()
			continue
		}

		// File field — stream to temp file
		tmpDir := opts.tmpDir
		if tmpDir == "" {
			tmpDir = os.TempDir()
		}
		tmpFile, err := os.CreateTemp(tmpDir, "codong-upload-*")
		if err != nil {
			part.Close()
			return nil, nil, err
		}

		written, err := io.Copy(tmpFile, io.LimitReader(part, opts.maxFileSize+1))
		tmpFile.Close()
		part.Close()

		if err != nil {
			os.Remove(tmpFile.Name())
			return nil, nil, err
		}

		if written > opts.maxFileSize {
			os.Remove(tmpFile.Name())
			return nil, nil, fmt.Errorf("E5009: file '%s' too large (%d bytes, max %d)", filename, written, opts.maxFileSize)
		}

		totalSize += written
		if totalSize > opts.maxTotalSize {
			os.Remove(tmpFile.Name())
			return nil, nil, fmt.Errorf("E5009: total upload size exceeds limit (%d bytes)", opts.maxTotalSize)
		}

		ext := filepath.Ext(filename)
		ct := part.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/octet-stream"
		}

		uf := &uploadedFile{
			FieldName:   fieldName,
			Filename:    filepath.Base(filename),
			TmpPath:     tmpFile.Name(),
			Size:        written,
			ContentType: ct,
			Extension:   ext,
		}
		files[fieldName] = append(files[fieldName], uf)
	}

	return files, formFields, nil
}

type uploadedFile struct {
	FieldName   string
	Filename    string
	TmpPath     string
	Size        int64
	ContentType string
	Extension   string
}

func uploadedFileToObject(uf *uploadedFile) *MapObject {
	return &MapObject{
		Entries: map[string]Object{
			"field_name":   &StringObject{Value: uf.FieldName},
			"filename":     &StringObject{Value: uf.Filename},
			"tmp_path":     &StringObject{Value: uf.TmpPath},
			"size":         &NumberObject{Value: float64(uf.Size)},
			"content_type": &StringObject{Value: uf.ContentType},
			"mime":         &StringObject{Value: uf.ContentType},
			"extension":    &StringObject{Value: uf.Extension},
		},
		Order: []string{"field_name", "filename", "tmp_path", "size", "content_type", "mime", "extension"},
	}
}

// ============================================================
// P2-1: web.ws() — WebSocket support
// ============================================================

// wsRoute stores a registered WebSocket route.
type wsRoute struct {
	pattern string
	handler Object // FunctionObject
}

// wsConnection wraps a WebSocket connection for Codong.
type wsConnection struct {
	id       string
	r        *http.Request
	w        http.ResponseWriter
	handlers map[string]Object
	context  *MapObject
	closed   bool
	mu       sync.Mutex
	done     chan struct{}
	sendCh   chan string
}

var (
	wsRoutes []wsRoute
	wsHub    = struct {
		mu    sync.RWMutex
		conns map[string]*wsConnection
	}{conns: make(map[string]*wsConnection)}
	wsIDCounter int64
	wsIDMu      sync.Mutex
)

func nextWSID() string {
	wsIDMu.Lock()
	defer wsIDMu.Unlock()
	wsIDCounter++
	return fmt.Sprintf("ws-%d-%d", time.Now().UnixMilli(), wsIDCounter)
}

// webWsRegister registers a WebSocket route handler.
func (i *Interpreter) webWsRegister(args []Object) Object {
	if len(args) < 2 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"server.ws requires (path, handler)", "server.ws(\"/chat\", fn(conn) { ... })")
	}
	path, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "path must be a string", "")
	}

	webModuleSingleton.mu.Lock()
	defer webModuleSingleton.mu.Unlock()
	wsRoutes = append(wsRoutes, wsRoute{
		pattern: path.Value,
		handler: args[1],
	})
	return NULL_OBJ
}

// webWsBroadcast broadcasts a message to all connections on a given path.
func (i *Interpreter) webWsBroadcast(args []Object) Object {
	if len(args) < 2 {
		return NULL_OBJ
	}
	pathStr := ""
	if s, ok := args[0].(*StringObject); ok {
		pathStr = s.Value
	}
	msg := ""
	if s, ok := args[1].(*StringObject); ok {
		msg = s.Value
	}

	wsHub.mu.RLock()
	defer wsHub.mu.RUnlock()
	for _, conn := range wsHub.conns {
		if !conn.closed {
			_ = pathStr // broadcast to all for simplicity
			select {
			case conn.sendCh <- msg:
			default: // skip if buffer full
			}
		}
	}
	return NULL_OBJ
}

// makeWSConnObject creates a Codong MapObject representing a WebSocket connection.
func (i *Interpreter) makeWSConnObject(conn *wsConnection) *MapObject {
	entries := map[string]Object{
		"id":      &StringObject{Value: conn.id},
		"context": conn.context,
	}
	order := []string{"id", "context"}

	// conn.send(msg)
	entries["send"] = &BuiltinFunction{Name: "conn.send", Fn: func(interp *Interpreter, args ...Object) Object {
		if len(args) > 0 {
			msg := ""
			if s, ok := args[0].(*StringObject); ok {
				msg = s.Value
			} else {
				msg = args[0].Inspect()
			}
			conn.mu.Lock()
			closed := conn.closed
			conn.mu.Unlock()
			if !closed {
				select {
				case conn.sendCh <- msg:
				default:
				}
			}
		}
		return NULL_OBJ
	}}
	order = append(order, "send")

	// conn.close()
	entries["close"] = &BuiltinFunction{Name: "conn.close", Fn: func(interp *Interpreter, args ...Object) Object {
		conn.mu.Lock()
		conn.closed = true
		conn.mu.Unlock()
		close(conn.done)
		return NULL_OBJ
	}}
	order = append(order, "close")

	// conn.on(event, handler)
	entries["on"] = &BuiltinFunction{Name: "conn.on", Fn: func(interp *Interpreter, args ...Object) Object {
		if len(args) >= 2 {
			if event, ok := args[0].(*StringObject); ok {
				conn.mu.Lock()
				conn.handlers[event.Value] = args[1]
				conn.mu.Unlock()
			}
		}
		return NULL_OBJ
	}}
	order = append(order, "on")

	// conn.query(name)
	entries["query"] = &BuiltinFunction{Name: "conn.query", Fn: func(interp *Interpreter, args ...Object) Object {
		if len(args) > 0 {
			if name, ok := args[0].(*StringObject); ok {
				val := conn.r.URL.Query().Get(name.Value)
				if val != "" {
					return &StringObject{Value: val}
				}
			}
		}
		return NULL_OBJ
	}}
	order = append(order, "query")

	// conn.header(name)
	entries["header"] = &BuiltinFunction{Name: "conn.header", Fn: func(interp *Interpreter, args ...Object) Object {
		if len(args) > 0 {
			if name, ok := args[0].(*StringObject); ok {
				val := conn.r.Header.Get(name.Value)
				if val != "" {
					return &StringObject{Value: val}
				}
			}
		}
		return NULL_OBJ
	}}
	order = append(order, "header")

	return &MapObject{Entries: entries, Order: order}
}

// ============================================================
// WebSocket upgrade handler (RFC 6455, basic text frames)
// ============================================================

const wsMagicGUID = "258EAFA5-E914-47DA-95CA-5AB5DC11885E"

// wsUpgradeHandler returns an HTTP handler that upgrades to WebSocket.
func (i *Interpreter) wsUpgradeHandler(handler Object) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Validate upgrade request
		if strings.ToLower(r.Header.Get("Upgrade")) != "websocket" {
			http.Error(w, "expected websocket upgrade", http.StatusBadRequest)
			return
		}

		key := r.Header.Get("Sec-WebSocket-Key")
		if key == "" {
			http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
			return
		}

		// Compute accept key
		h := sha1.New()
		h.Write([]byte(key + wsMagicGUID))
		acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

		// Hijack the connection
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "websocket not supported", http.StatusInternalServerError)
			return
		}
		netConn, rw, err := hj.Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Send upgrade response
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey + "\r\n\r\n"
		netConn.Write([]byte(resp))

		conn := &wsConnection{
			id:       nextWSID(),
			r:        r,
			w:        w,
			handlers: make(map[string]Object),
			context:  &MapObject{Entries: map[string]Object{}, Order: []string{}},
			done:     make(chan struct{}),
			sendCh:   make(chan string, 64),
		}

		// Register connection
		wsHub.mu.Lock()
		wsHub.conns[conn.id] = conn
		wsHub.mu.Unlock()

		// Create Codong conn object and call handler
		connObj := i.makeWSConnObject(conn)

		i.mu.Lock()
		i.applyFunction(handler, []Object{connObj})
		i.mu.Unlock()

		// Writer goroutine
		go func() {
			for {
				select {
				case msg := <-conn.sendCh:
					wsWriteTextFrame(netConn, msg)
				case <-conn.done:
					return
				}
			}
		}()

		// Reader loop
		reader := bufio.NewReader(rw)
		for {
			msg, opcode, err := wsReadFrame(reader)
			if err != nil {
				break
			}

			switch opcode {
			case 0x1: // text frame
				conn.mu.Lock()
				msgHandler := conn.handlers["message"]
				conn.mu.Unlock()
				if msgHandler != nil {
					i.mu.Lock()
					i.applyFunction(msgHandler, []Object{&StringObject{Value: msg}})
					i.mu.Unlock()
				}
			case 0x8: // close frame
				wsWriteFrame(netConn, 0x8, []byte{})
				goto cleanup
			case 0x9: // ping
				wsWriteFrame(netConn, 0xA, []byte(msg)) // pong
			}
		}

	cleanup:
		conn.mu.Lock()
		closeHandler := conn.handlers["close"]
		conn.closed = true
		conn.mu.Unlock()

		if closeHandler != nil {
			i.mu.Lock()
			i.applyFunction(closeHandler, []Object{})
			i.mu.Unlock()
		}

		// Unregister connection
		wsHub.mu.Lock()
		delete(wsHub.conns, conn.id)
		wsHub.mu.Unlock()

		select {
		case <-conn.done:
		default:
			close(conn.done)
		}
		netConn.Close()
	}
}

// wsReadFrame reads a WebSocket frame. Returns message, opcode, error.
func wsReadFrame(reader *bufio.Reader) (string, byte, error) {
	b1, err := reader.ReadByte()
	if err != nil {
		return "", 0, err
	}
	b2, err := reader.ReadByte()
	if err != nil {
		return "", 0, err
	}

	opcode := b1 & 0x0F
	masked := (b2 & 0x80) != 0
	length := int64(b2 & 0x7F)

	if length == 126 {
		var lenBytes [2]byte
		if _, err := io.ReadFull(reader, lenBytes[:]); err != nil {
			return "", 0, err
		}
		length = int64(binary.BigEndian.Uint16(lenBytes[:]))
	} else if length == 127 {
		var lenBytes [8]byte
		if _, err := io.ReadFull(reader, lenBytes[:]); err != nil {
			return "", 0, err
		}
		length = int64(binary.BigEndian.Uint64(lenBytes[:]))
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(reader, mask[:]); err != nil {
			return "", 0, err
		}
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return "", 0, err
	}

	if masked {
		for idx := range payload {
			payload[idx] ^= mask[idx%4]
		}
	}

	return string(payload), opcode, nil
}

// wsWriteTextFrame sends a text frame.
func wsWriteTextFrame(conn interface{ Write([]byte) (int, error) }, msg string) {
	wsWriteFrame(conn, 0x1, []byte(msg))
}

// wsWriteFrame writes a WebSocket frame (server to client, no mask).
func wsWriteFrame(conn interface{ Write([]byte) (int, error) }, opcode byte, payload []byte) {
	frame := []byte{0x80 | opcode}
	length := len(payload)
	if length < 126 {
		frame = append(frame, byte(length))
	} else if length < 65536 {
		frame = append(frame, 126)
		lenBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBytes, uint16(length))
		frame = append(frame, lenBytes...)
	} else {
		frame = append(frame, 127)
		lenBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(lenBytes, uint64(length))
		frame = append(frame, lenBytes...)
	}
	frame = append(frame, payload...)
	conn.Write(frame)
}
