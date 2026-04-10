package interpreter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/codong-lang/codong/stdlib/codongerror"
)

// ============================================================
// Module Object Types
// ============================================================

// FsModuleObject is the singleton `fs` module.
type FsModuleObject struct{}

func (f *FsModuleObject) Type() string    { return "module" }
func (f *FsModuleObject) Inspect() string { return "<module:fs>" }

var fsModuleSingleton = &FsModuleObject{}

// JsonModuleObject is the singleton `json` module.
type JsonModuleObject struct{}

func (j *JsonModuleObject) Type() string    { return "module" }
func (j *JsonModuleObject) Inspect() string { return "<module:json>" }

var jsonModuleSingleton = &JsonModuleObject{}

// EnvModuleObject is the singleton `env` module.
type EnvModuleObject struct{}

func (e *EnvModuleObject) Type() string    { return "module" }
func (e *EnvModuleObject) Inspect() string { return "<module:env>" }

var envModuleSingleton = &EnvModuleObject{}

// ArgsModuleObject is the singleton `args` module for command-line arguments.
type ArgsModuleObject struct{}

func (a *ArgsModuleObject) Type() string    { return "module" }
func (a *ArgsModuleObject) Inspect() string { return "<module:args>" }

var argsModuleSingleton = &ArgsModuleObject{}

// TimeModuleObject is the singleton `time` module.
type TimeModuleObject struct{}

func (t *TimeModuleObject) Type() string    { return "module" }
func (t *TimeModuleObject) Inspect() string { return "<module:time>" }

var timeModuleSingleton = &TimeModuleObject{}

// ============================================================
// fs module helpers
// ============================================================

// fsWorkDir returns the working directory for fs operations.
// It uses the directory of the source file being executed, or cwd.
func (i *Interpreter) fsWorkDir() string {
	// If the interpreter has a known source directory, use it.
	// Otherwise fall back to os.Getwd().
	if i.workDir != "" {
		return i.workDir
	}
	wd, _ := os.Getwd()
	return wd
}

// fsResolve resolves a path relative to the fs working directory.
func (i *Interpreter) fsResolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(i.fsWorkDir(), path))
}

// fsSafeJoin performs safe path joining to prevent path traversal.
func (i *Interpreter) fsSafeJoin(baseDir, userInput string) (string, bool) {
	absBase := i.fsResolve(baseDir)

	// Null byte check (before any processing)
	if strings.ContainsRune(userInput, 0) || strings.Contains(userInput, "\\x00") || strings.Contains(userInput, "\x00") {
		return "", false
	}

	// Reject absolute paths
	if filepath.IsAbs(userInput) || strings.HasPrefix(userInput, "/") {
		return "", false
	}

	// Reject backslash paths (Windows-style traversal)
	if strings.Contains(userInput, "\\") {
		return "", false
	}

	// URL-decode user input to handle %2f bypasses (loop to catch double-encoding)
	decoded := userInput
	for i := 0; i < 3; i++ {
		d, err := url.PathUnescape(decoded)
		if err != nil || d == decoded {
			break
		}
		decoded = d
	}

	// Re-check after decoding
	if strings.Contains(decoded, "..") {
		return "", false
	}
	if filepath.IsAbs(decoded) || strings.HasPrefix(decoded, "/") {
		return "", false
	}

	joined := filepath.Clean(filepath.Join(absBase, decoded))

	// Strict prefix check
	if !strings.HasPrefix(joined+string(filepath.Separator), absBase+string(filepath.Separator)) {
		return "", false
	}

	return filepath.ToSlash(joined), true
}

// fsError creates an ErrorObject for fs operations.
func fsError(code, message, fix string) *ErrorObject {
	return &ErrorObject{
		Error:     codongerror.New(code, message, codongerror.WithFix(fix)),
		IsRuntime: true,
	}
}

// ============================================================
// fs module methods
// ============================================================

func (i *Interpreter) evalFsModuleMethod(prop string) Object {
	return &BuiltinFunction{
		Name: "fs." + prop,
		Fn: func(interp *Interpreter, args ...Object) Object {
			switch prop {
			case "read":
				if len(args) < 1 {
					return fsError("E5001_FILE_NOT_FOUND", "fs.read requires a path argument", "fs.read(\"./file.txt\")")
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				// Check if path is a directory first
				if info, statErr := os.Stat(absPath); statErr == nil && info.IsDir() {
					return fsError("E5004_IS_DIRECTORY",
						fmt.Sprintf("path is a directory: %s", path),
						fmt.Sprintf("use fs.list(\"%s\") to read directory contents", path))
				}
				data, err := os.ReadFile(absPath)
				if err != nil {
					if os.IsNotExist(err) {
						return &ErrorObject{IsRuntime: false, Error: codongerror.New(
							"E5001_FILE_NOT_FOUND",
							fmt.Sprintf("file not found: %s", path),
							codongerror.WithFix(fmt.Sprintf("check file path: %s", absPath)),
						)}
					}
					if os.IsPermission(err) {
						return fsError("E5002_PERMISSION_DENIED",
							fmt.Sprintf("permission denied: %s", path),
							"check file permissions or run with elevated privileges")
					}
					return fsError("E5008_IO_ERROR", err.Error(), "check disk space and file system health")
				}
				return &StringObject{Value: string(data)}

			case "write":
				if len(args) < 2 {
					return fsError("E5008_IO_ERROR", "fs.write requires path and content arguments", "fs.write(\"./file.txt\", \"content\")")
				}
				path := args[0].Inspect()
				content := args[1].Inspect()
				absPath := interp.fsResolve(path)
				if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check disk permissions")
				}
				if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
					if os.IsPermission(err) {
						return fsError("E5002_PERMISSION_DENIED", err.Error(), "check file permissions")
					}
					return fsError("E5008_IO_ERROR", err.Error(), "check disk space")
				}
				return TRUE_OBJ

			case "append":
				if len(args) < 2 {
					return fsError("E5008_IO_ERROR", "fs.append requires path and content arguments", "fs.append(\"./file.txt\", \"content\")")
				}
				path := args[0].Inspect()
				content := args[1].Inspect()
				absPath := interp.fsResolve(path)
				if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check disk permissions")
				}
				f, err := os.OpenFile(absPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				if err != nil {
					if os.IsPermission(err) {
						return fsError("E5002_PERMISSION_DENIED", err.Error(), "check file permissions")
					}
					return fsError("E5008_IO_ERROR", err.Error(), "check disk space")
				}
				defer f.Close()
				if _, err := f.WriteString(content); err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check disk space")
				}
				return TRUE_OBJ

			case "exists":
				if len(args) < 1 {
					return FALSE_OBJ
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				_, err := os.Stat(absPath)
				return nativeBoolToObject(err == nil)

			case "is_dir":
				if len(args) < 1 {
					return FALSE_OBJ
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				info, err := os.Stat(absPath)
				if err != nil {
					return FALSE_OBJ
				}
				return nativeBoolToObject(info.IsDir())

			case "is_file":
				if len(args) < 1 {
					return FALSE_OBJ
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				info, err := os.Stat(absPath)
				if err != nil {
					return FALSE_OBJ
				}
				return nativeBoolToObject(!info.IsDir())

			case "mime_type":
				if len(args) < 1 {
					return &StringObject{Value: "application/octet-stream"}
				}
				path := args[0].Inspect()
				ext := filepath.Ext(path)
				mimeType := mime.TypeByExtension(ext)
				if mimeType == "" {
					mimeType = "application/octet-stream"
				}
				return &StringObject{Value: mimeType}

			case "delete":
				if len(args) < 1 {
					return fsError("E5008_IO_ERROR", "fs.delete requires a path argument", "fs.delete(\"./file.txt\")")
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				if err := os.Remove(absPath); err != nil {
					if os.IsNotExist(err) {
						return TRUE_OBJ // idempotent delete
					}
					if os.IsPermission(err) {
						return fsError("E5002_PERMISSION_DENIED", err.Error(), "check file permissions")
					}
					return fsError("E5008_IO_ERROR", err.Error(), "check disk space")
				}
				return TRUE_OBJ

			case "copy":
				if len(args) < 2 {
					return fsError("E5008_IO_ERROR", "fs.copy requires src and dst arguments", "fs.copy(\"./src.txt\", \"./dst.txt\")")
				}
				src := interp.fsResolve(args[0].Inspect())
				dst := interp.fsResolve(args[1].Inspect())
				srcFile, err := os.Open(src)
				if err != nil {
					if os.IsNotExist(err) {
						return fsError("E5001_FILE_NOT_FOUND", fmt.Sprintf("source file not found: %s", args[0].Inspect()), "check source path")
					}
					return fsError("E5008_IO_ERROR", err.Error(), "check file permissions")
				}
				defer srcFile.Close()
				if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check disk permissions")
				}
				dstFile, err := os.Create(dst)
				if err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check disk permissions")
				}
				defer dstFile.Close()
				if _, err := io.Copy(dstFile, srcFile); err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check disk space")
				}
				return TRUE_OBJ

			case "move":
				if len(args) < 2 {
					return fsError("E5008_IO_ERROR", "fs.move requires src and dst arguments", "fs.move(\"./old.txt\", \"./new.txt\")")
				}
				src := interp.fsResolve(args[0].Inspect())
				dst := interp.fsResolve(args[1].Inspect())
				if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check disk permissions")
				}
				if err := os.Rename(src, dst); err != nil {
					if os.IsNotExist(err) {
						return fsError("E5001_FILE_NOT_FOUND", fmt.Sprintf("source file not found: %s", args[0].Inspect()), "check source path")
					}
					return fsError("E5008_IO_ERROR", err.Error(), "check disk space")
				}
				return TRUE_OBJ

			case "list":
				if len(args) < 1 {
					return fsError("E5008_IO_ERROR", "fs.list requires a directory path", "fs.list(\"./uploads\")")
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				entries, err := os.ReadDir(absPath)
				if err != nil {
					if os.IsNotExist(err) {
						return fsError("E5001_FILE_NOT_FOUND", fmt.Sprintf("directory not found: %s", path), "check directory path")
					}
					return fsError("E5008_IO_ERROR", err.Error(), "check permissions")
				}
				result := make([]Object, 0, len(entries))
				for _, entry := range entries {
					info, _ := entry.Info()
					entryType := "file"
					if entry.IsDir() {
						entryType = "dir"
					}
					var size int64
					var modified string
					if info != nil {
						size = info.Size()
						modified = info.ModTime().UTC().Format(time.RFC3339)
					}
					m := &MapObject{
						Entries: map[string]Object{
							"name":     &StringObject{Value: entry.Name()},
							"path":     &StringObject{Value: filepath.ToSlash(filepath.Join(absPath, entry.Name()))},
							"type":     &StringObject{Value: entryType},
							"size":     &NumberObject{Value: float64(size)},
							"modified": &StringObject{Value: modified},
						},
						Order: []string{"name", "path", "type", "size", "modified"},
					}
					result = append(result, m)
				}
				return &ListObject{Elements: result}

			case "mkdir":
				if len(args) < 1 {
					return fsError("E5008_IO_ERROR", "fs.mkdir requires a path", "fs.mkdir(\"./data\")")
				}
				absPath := interp.fsResolve(args[0].Inspect())
				if err := os.MkdirAll(absPath, 0755); err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check permissions")
				}
				return TRUE_OBJ

			case "rmdir":
				if len(args) < 1 {
					return fsError("E5008_IO_ERROR", "fs.rmdir requires a path", "fs.rmdir(\"./dir\")")
				}
				absPath := interp.fsResolve(args[0].Inspect())
				// Check for recursive named arg or second arg
				recursive := false
				if len(args) >= 2 {
					if b, ok := args[1].(*BoolObject); ok && b.Value {
						recursive = true
					}
					// Check for named arg: recursive:true
					if m, ok := args[len(args)-1].(*MapObject); ok {
						if rv, exists := m.Entries["recursive"]; exists {
							if b, ok := rv.(*BoolObject); ok && b.Value {
								recursive = true
							}
						}
					}
				}
				if recursive {
					if err := os.RemoveAll(absPath); err != nil {
						return fsError("E5008_IO_ERROR", err.Error(), "check permissions")
					}
				} else {
					if err := os.Remove(absPath); err != nil {
						if strings.Contains(err.Error(), "not empty") || strings.Contains(err.Error(), "directory not empty") {
							return fsError("E5006_DIR_NOT_EMPTY",
								fmt.Sprintf("directory not empty: %s", args[0].Inspect()),
								"use fs.rmdir(path, true) to delete recursively")
						}
						return fsError("E5008_IO_ERROR", err.Error(), "check permissions")
					}
				}
				return TRUE_OBJ

			case "stat":
				if len(args) < 1 {
					return NULL_OBJ
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				info, err := os.Stat(absPath)
				if err != nil {
					return NULL_OBJ
				}
				entryType := "file"
				if info.IsDir() {
					entryType = "dir"
				}
				return &MapObject{
					Entries: map[string]Object{
						"name":      &StringObject{Value: info.Name()},
						"path":      &StringObject{Value: filepath.ToSlash(absPath)},
						"type":      &StringObject{Value: entryType},
						"size":      &NumberObject{Value: float64(info.Size())},
						"modified":  &StringObject{Value: info.ModTime().UTC().Format(time.RFC3339)},
						"created":   &StringObject{Value: info.ModTime().UTC().Format(time.RFC3339)},
						"extension": &StringObject{Value: filepath.Ext(info.Name())},
					},
					Order: []string{"name", "path", "type", "size", "modified", "created", "extension"},
				}

			case "read_json":
				if len(args) < 1 {
					return fsError("E5001_FILE_NOT_FOUND", "fs.read_json requires a path", "fs.read_json(\"./config.json\")")
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				data, err := os.ReadFile(absPath)
				if err != nil {
					if os.IsNotExist(err) {
						return fsError("E5001_FILE_NOT_FOUND", fmt.Sprintf("file not found: %s", path), fmt.Sprintf("check path: %s", absPath))
					}
					return fsError("E5008_IO_ERROR", err.Error(), "check file permissions")
				}
				var result interface{}
				if err := json.Unmarshal(data, &result); err != nil {
					return newRuntimeError("E6001_PARSE_ERROR", fmt.Sprintf("JSON parse error in %s: %s", path, err.Error()), "check JSON syntax")
				}
				return goValueToObject(result)

			case "write_json":
				if len(args) < 2 {
					return fsError("E5008_IO_ERROR", "fs.write_json requires path and data", "fs.write_json(\"./out.json\", data)")
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				goVal := objectToGoValue(args[1])
				jsonData, err := json.MarshalIndent(goVal, "", "  ")
				if err != nil {
					return newRuntimeError("E6002_STRINGIFY_ERROR", fmt.Sprintf("JSON stringify error: %s", err.Error()), "remove circular references")
				}
				if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check disk permissions")
				}
				if err := os.WriteFile(absPath, append(jsonData, '\n'), 0644); err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check disk space")
				}
				return TRUE_OBJ

			case "read_lines":
				if len(args) < 1 {
					return fsError("E5001_FILE_NOT_FOUND", "fs.read_lines requires a path", "fs.read_lines(\"./data.csv\")")
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				data, err := os.ReadFile(absPath)
				if err != nil {
					if os.IsNotExist(err) {
						return fsError("E5001_FILE_NOT_FOUND", fmt.Sprintf("file not found: %s", path), fmt.Sprintf("check path: %s", absPath))
					}
					return fsError("E5008_IO_ERROR", err.Error(), "check file permissions")
				}
				content := string(data)
				// Normalize line endings
				content = strings.ReplaceAll(content, "\r\n", "\n")
				lines := strings.Split(content, "\n")
				// Remove trailing empty line if file ends with newline
				if len(lines) > 0 && lines[len(lines)-1] == "" {
					lines = lines[:len(lines)-1]
				}
				elements := make([]Object, len(lines))
				for idx, line := range lines {
					elements[idx] = &StringObject{Value: line}
				}
				return &ListObject{Elements: elements}

			case "write_lines":
				if len(args) < 2 {
					return fsError("E5008_IO_ERROR", "fs.write_lines requires path and lines list", "fs.write_lines(\"./out.csv\", lines)")
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				list, ok := args[1].(*ListObject)
				if !ok {
					return fsError("E5008_IO_ERROR", "fs.write_lines second argument must be a list", "fs.write_lines(path, [\"line1\", \"line2\"])")
				}
				var sb strings.Builder
				for _, el := range list.Elements {
					sb.WriteString(el.Inspect())
					sb.WriteString("\n")
				}
				if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check disk permissions")
				}
				if err := os.WriteFile(absPath, []byte(sb.String()), 0644); err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check disk space")
				}
				return TRUE_OBJ

			case "join":
				parts := make([]string, len(args))
				for idx, a := range args {
					parts[idx] = a.Inspect()
				}
				return &StringObject{Value: filepath.ToSlash(filepath.Join(parts...))}

			case "cwd":
				return &StringObject{Value: filepath.ToSlash(interp.fsWorkDir())}

			case "basename":
				if len(args) < 1 {
					return &StringObject{Value: ""}
				}
				return &StringObject{Value: filepath.Base(args[0].Inspect())}

			case "dirname":
				if len(args) < 1 {
					return &StringObject{Value: ""}
				}
				return &StringObject{Value: filepath.ToSlash(filepath.Dir(args[0].Inspect()))}

			case "extension":
				if len(args) < 1 {
					return &StringObject{Value: ""}
				}
				return &StringObject{Value: filepath.Ext(args[0].Inspect())}

			case "safe_join":
				if len(args) < 2 {
					return NULL_OBJ
				}
				result, ok := interp.fsSafeJoin(args[0].Inspect(), args[1].Inspect())
				if !ok {
					return NULL_OBJ
				}
				return &StringObject{Value: result}

			case "temp_file":
				f, err := os.CreateTemp("", "codong-*.tmp")
				if err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check temp directory permissions")
				}
				tmpPath := filepath.ToSlash(f.Name())
				f.Close()
				// Return a map with path and delete function
				m := &MapObject{
					Entries: map[string]Object{
						"path": &StringObject{Value: tmpPath},
						"delete": &BuiltinFunction{
							Name: "temp_file.delete",
							Fn: func(interp *Interpreter, args ...Object) Object {
								os.Remove(tmpPath)
								return NULL_OBJ
							},
						},
					},
					Order: []string{"path", "delete"},
				}
				return m

			case "temp_dir":
				dir, err := os.MkdirTemp("", "codong-*")
				if err != nil {
					return fsError("E5008_IO_ERROR", err.Error(), "check temp directory permissions")
				}
				tmpPath := filepath.ToSlash(dir)
				m := &MapObject{
					Entries: map[string]Object{
						"path": &StringObject{Value: tmpPath},
						"delete": &BuiltinFunction{
							Name: "temp_dir.delete",
							Fn: func(interp *Interpreter, args ...Object) Object {
								os.RemoveAll(tmpPath)
								return NULL_OBJ
							},
						},
					},
					Order: []string{"path", "delete"},
				}
				return m
			}
			return NULL_OBJ
		},
	}
}

// ============================================================
// json module methods
// ============================================================

func (i *Interpreter) evalJsonModuleMethod(prop string) Object {
	return &BuiltinFunction{
		Name: "json." + prop,
		Fn: func(interp *Interpreter, args ...Object) Object {
			switch prop {
			case "parse":
				if len(args) < 1 {
					return newRuntimeError("E6001_PARSE_ERROR", "json.parse requires a string argument", "json.parse('{\"key\":\"value\"}')")
				}
				s := args[0].Inspect()
				var result interface{}
				if err := json.Unmarshal([]byte(s), &result); err != nil {
					return newRuntimeError("E6001_PARSE_ERROR",
						fmt.Sprintf("JSON parse error: %s", err.Error()),
						"check JSON syntax, use json.valid() first")
				}
				return goValueToObject(result)

			case "stringify":
				if len(args) < 1 {
					return &StringObject{Value: "null"}
				}
				goVal := objectToGoValue(args[0])
				indent := 0
				if len(args) >= 2 {
					if n, ok := args[1].(*NumberObject); ok {
						indent = int(n.Value)
					}
					// Check for named args map (e.g., indent:2 passed as trailing MapObject)
					if m, ok := args[len(args)-1].(*MapObject); ok {
						if indentVal, exists := m.Entries["indent"]; exists {
							if n, ok := indentVal.(*NumberObject); ok {
								indent = int(n.Value)
							}
						}
					}
				}
				var data []byte
				var err error
				if indent > 0 {
					data, err = json.MarshalIndent(goVal, "", strings.Repeat(" ", indent))
				} else {
					data, err = json.Marshal(goVal)
				}
				if err != nil {
					return newRuntimeError("E6002_STRINGIFY_ERROR",
						fmt.Sprintf("JSON stringify error: %s", err.Error()),
						"remove circular references before stringifying")
				}
				return &StringObject{Value: string(data)}

			case "valid":
				if len(args) < 1 {
					return FALSE_OBJ
				}
				s := args[0].Inspect()
				return nativeBoolToObject(json.Valid([]byte(s)))

			case "merge":
				if len(args) < 2 {
					if len(args) == 1 {
						return args[0]
					}
					return &MapObject{Entries: map[string]Object{}, Order: []string{}}
				}
				aGo := objectToGoValue(args[0])
				bGo := objectToGoValue(args[1])
				aMap, aOk := aGo.(map[string]interface{})
				bMap, bOk := bGo.(map[string]interface{})
				if !aOk || !bOk {
					return args[1] // fallback: return second arg
				}
				merged := deepMerge(aMap, bMap)
				return goValueToObject(merged)

			case "get":
				if len(args) < 2 {
					return NULL_OBJ
				}
				data := objectToGoValue(args[0])
				pathStr, ok := args[1].(*StringObject)
				if !ok {
					return NULL_OBJ
				}
				var defaultVal interface{}
				if len(args) >= 3 {
					defaultVal = objectToGoValue(args[2])
				}
				result := jsonGetPath(data, pathStr.Value, defaultVal)
				if result == nil {
					return NULL_OBJ
				}
				return goValueToObject(result)

			case "set":
				if len(args) < 3 {
					return NULL_OBJ
				}
				data := objectToGoValue(args[0])
				pathStr, ok := args[1].(*StringObject)
				if !ok {
					return NULL_OBJ
				}
				value := objectToGoValue(args[2])
				result := jsonSetPath(data, pathStr.Value, value)
				return goValueToObject(result)

			case "flat", "flatten":
				if len(args) < 1 {
					return &MapObject{Entries: map[string]Object{}, Order: []string{}}
				}
				data := objectToGoValue(args[0])
				m, ok := data.(map[string]interface{})
				if !ok {
					return args[0]
				}
				flat := make(map[string]interface{})
				jsonFlatten("", m, flat)
				return goValueToObject(flat)

			case "unflatten":
				if len(args) < 1 {
					return &MapObject{Entries: map[string]Object{}, Order: []string{}}
				}
				data := objectToGoValue(args[0])
				m, ok := data.(map[string]interface{})
				if !ok {
					return args[0]
				}
				result := jsonUnflatten(m)
				return goValueToObject(result)
			}
			return NULL_OBJ
		},
	}
}

// deepMerge performs a deep merge of two maps.
func deepMerge(a, b map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		if aVal, exists := result[k]; exists {
			aMap, aOk := aVal.(map[string]interface{})
			bMap, bOk := v.(map[string]interface{})
			if aOk && bOk {
				result[k] = deepMerge(aMap, bMap)
				continue
			}
		}
		result[k] = v
	}
	return result
}

// jsonGetPath gets a value from a nested structure using dot notation.
func jsonGetPath(data interface{}, path string, defaultVal interface{}) interface{} {
	parts := strings.Split(path, ".")
	cur := data
	for _, part := range parts {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return defaultVal
		}
		cur, ok = m[part]
		if !ok {
			return defaultVal // key doesn't exist - use default
		}
	}
	// Return cur even if nil — null values are explicitly set, not missing
	return cur
}

// jsonSetPath sets a value in a nested structure using dot notation.
// Returns a new map (does not modify the original).
func jsonSetPath(data interface{}, path string, value interface{}) interface{} {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return data
	}
	return jsonSetPathRecursive(data, parts, value)
}

func jsonSetPathRecursive(data interface{}, parts []string, value interface{}) interface{} {
	if len(parts) == 0 {
		return value
	}

	m, ok := data.(map[string]interface{})
	if !ok {
		m = make(map[string]interface{})
	}

	// Deep copy the map
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = v
	}

	key := parts[0]
	if len(parts) == 1 {
		result[key] = value
	} else {
		existing, _ := result[key]
		result[key] = jsonSetPathRecursive(existing, parts[1:], value)
	}
	return result
}

// jsonFlatten flattens a nested map into dot-notation keys.
func jsonFlatten(prefix string, m map[string]interface{}, out map[string]interface{}) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if nested, ok := v.(map[string]interface{}); ok {
			jsonFlatten(key, nested, out)
		} else {
			out[key] = v
		}
	}
}

// jsonUnflatten converts a flat dot-notation map back to a nested map.
func jsonUnflatten(flat map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range flat {
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
	return result
}

// ============================================================
// env module methods
// ============================================================

func (i *Interpreter) evalEnvModuleMethod(prop string) Object {
	return &BuiltinFunction{
		Name: "env." + prop,
		Fn: func(interp *Interpreter, args ...Object) Object {
			switch prop {
			case "get":
				if len(args) < 1 {
					return NULL_OBJ
				}
				name := args[0].Inspect()
				val, ok := os.LookupEnv(name)
				if !ok {
					if len(args) >= 2 {
						return args[1] // return default value
					}
					return NULL_OBJ
				}
				return &StringObject{Value: val}

			case "require":
				if len(args) < 1 {
					return newRuntimeError("E7001_ENV_NOT_SET", "env.require needs a variable name", "env.require(\"DATABASE_URL\")")
				}
				name := args[0].Inspect()
				val, ok := os.LookupEnv(name)
				if !ok {
					return newRuntimeError("E7001_ENV_NOT_SET",
						fmt.Sprintf("required environment variable not set: %s", name),
						fmt.Sprintf("set environment variable: %s", name))
				}
				return &StringObject{Value: val}

			case "has":
				if len(args) < 1 {
					return FALSE_OBJ
				}
				name := args[0].Inspect()
				_, ok := os.LookupEnv(name)
				return nativeBoolToObject(ok)

			case "all":
				entries := make(map[string]Object)
				order := make([]string, 0)
				for _, env := range os.Environ() {
					parts := strings.SplitN(env, "=", 2)
					if len(parts) == 2 {
						entries[parts[0]] = &StringObject{Value: parts[1]}
						order = append(order, parts[0])
					}
				}
				return &MapObject{Entries: entries, Order: order}

			case "load":
				if len(args) < 1 {
					return newRuntimeError("E7002_ENV_FILE_NOT_FOUND", "env.load requires a file path", "env.load(\"./.env\")")
				}
				path := args[0].Inspect()
				absPath := interp.fsResolve(path)
				f, err := os.Open(absPath)
				if err != nil {
					if os.IsNotExist(err) {
						return newRuntimeError("E7002_ENV_FILE_NOT_FOUND",
							fmt.Sprintf(".env file not found: %s", path),
							"create .env file or use env.get() with default")
					}
					return newRuntimeError("E7003_ENV_PARSE_ERROR", err.Error(), "check file permissions")
				}
				defer f.Close()

				count := 0
				scanner := bufio.NewScanner(f)
				for scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())
					// Skip empty lines and comments
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					eqIdx := strings.Index(line, "=")
					if eqIdx < 0 {
						continue
					}
					key := strings.TrimSpace(line[:eqIdx])
					val := strings.TrimSpace(line[eqIdx+1:])
					// Strip quotes
					if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`)) ||
						(strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`)) {
						val = val[1 : len(val)-1]
					}
					// Handle \n in double-quoted values
					if strings.Contains(val, `\n`) {
						val = strings.ReplaceAll(val, `\n`, "\n")
					}
					// Only set if not already set
					if _, ok := os.LookupEnv(key); !ok {
						os.Setenv(key, val)
						count++
					}
				}
				return &NumberObject{Value: float64(count)}
			}
			return NULL_OBJ
		},
	}
}

// ============================================================
// args module methods
// ============================================================

func (i *Interpreter) evalArgsModuleMethod(prop string) Object {
	return &BuiltinFunction{
		Name: "args." + prop,
		Fn: func(interp *Interpreter, args ...Object) Object {
			switch prop {
			case "all":
				// Return all command-line arguments as a list
				result := make([]Object, 0)
				if len(os.Args) > 1 {
					for _, arg := range os.Args[1:] {
						result = append(result, &StringObject{Value: arg})
					}
				}
				return &ListObject{Elements: result}

			case "get":
				// args.get(index, default?) - get argument by index
				if len(args) < 1 {
					return NULL_OBJ
				}
				idxObj, ok := args[0].(*NumberObject)
				if !ok {
					return NULL_OBJ
				}
				idx := int(idxObj.Value)
				if idx < 0 || idx >= len(os.Args)-1 {
					if len(args) >= 2 {
						return args[1] // return default value
					}
					return NULL_OBJ
				}
				return &StringObject{Value: os.Args[idx+1]} // +1 because os.Args[0] is program name

			case "has":
				// args.has(value) - check if argument exists
				if len(args) < 1 {
					return FALSE_OBJ
				}
				search := args[0].Inspect()
				for _, arg := range os.Args[1:] {
					if arg == search {
						return TRUE_OBJ
					}
				}
				return FALSE_OBJ

			case "len":
				// args.len() - number of arguments
				return &NumberObject{Value: float64(len(os.Args) - 1)}
			}
			return NULL_OBJ
		},
	}
}

// ============================================================
// time module methods
// ============================================================

func (i *Interpreter) evalTimeModuleMethod(prop string) Object {
	return &BuiltinFunction{
		Name: "time." + prop,
		Fn: func(interp *Interpreter, args ...Object) Object {
			switch prop {
			case "sleep":
				if len(args) < 1 {
					return NULL_OBJ
				}
				durStr := args[0].Inspect()
				d, err := time.ParseDuration(durStr)
				if err != nil {
					return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
						"invalid duration: "+durStr,
						"use format like '500ms', '2s', '1m', '1h'")
				}
				time.Sleep(d)
				return NULL_OBJ

			case "now":
				return &NumberObject{Value: float64(time.Now().UnixMilli())}

			case "now_iso":
				return &StringObject{Value: time.Now().UTC().Format(time.RFC3339Nano)}

			case "format":
				if len(args) < 2 {
					return &StringObject{Value: ""}
				}
				tsMs := float64(0)
				if n, ok := args[0].(*NumberObject); ok {
					tsMs = n.Value
				}
				fmtStr := args[1].Inspect()
				t := time.UnixMilli(int64(tsMs)).UTC()
				// Built-in format aliases
				switch fmtStr {
				case "date":
					fmtStr = "2006-01-02"
				case "datetime":
					fmtStr = "2006-01-02 15:04:05"
				case "iso":
					fmtStr = time.RFC3339
				case "rfc2822":
					fmtStr = "Mon, 02 Jan 2006 15:04:05 -0700"
				}
				return &StringObject{Value: t.Format(fmtStr)}

			case "parse":
				if len(args) < 1 {
					return NULL_OBJ
				}
				s := args[0].Inspect()
				formats := []string{
					time.RFC3339Nano, time.RFC3339,
					"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
				}
				if len(args) >= 2 {
					formats = []string{args[1].Inspect()}
				}
				for _, f := range formats {
					if t, err := time.Parse(f, s); err == nil {
						return &NumberObject{Value: float64(t.UnixMilli())}
					}
				}
				return NULL_OBJ

			case "diff":
				if len(args) < 2 {
					return NULL_OBJ
				}
				t1 := float64(0)
				t2 := float64(0)
				if n, ok := args[0].(*NumberObject); ok {
					t1 = n.Value
				}
				if n, ok := args[1].(*NumberObject); ok {
					t2 = n.Value
				}
				ms := t2 - t1
				return &NumberObject{Value: ms}

			case "since":
				if len(args) < 1 {
					return NULL_OBJ
				}
				t1 := float64(0)
				if n, ok := args[0].(*NumberObject); ok {
					t1 = n.Value
				}
				now := float64(time.Now().UnixMilli())
				ms := now - t1
				if ms < 0 {
					ms = 0
				}
				return &MapObject{
					Entries: map[string]Object{
						"ms":   &NumberObject{Value: ms},
						"s":    &NumberObject{Value: float64(int64(ms) / 1000)},
						"m":    &NumberObject{Value: float64(int64(ms) / 60000)},
						"h":    &NumberObject{Value: float64(int64(ms) / 3600000)},
						"days": &NumberObject{Value: float64(int64(ms) / 86400000)},
					},
					Order: []string{"ms", "s", "m", "h", "days"},
				}

			case "until":
				if len(args) < 1 {
					return NULL_OBJ
				}
				t1 := float64(0)
				if n, ok := args[0].(*NumberObject); ok {
					t1 = n.Value
				}
				now := float64(time.Now().UnixMilli())
				ms := t1 - now
				if ms < 0 {
					ms = 0
				}
				return &MapObject{
					Entries: map[string]Object{
						"ms":   &NumberObject{Value: ms},
						"s":    &NumberObject{Value: float64(int64(ms) / 1000)},
						"m":    &NumberObject{Value: float64(int64(ms) / 60000)},
						"h":    &NumberObject{Value: float64(int64(ms) / 3600000)},
						"days": &NumberObject{Value: float64(int64(ms) / 86400000)},
					},
					Order: []string{"ms", "s", "m", "h", "days"},
				}

			case "add":
				if len(args) < 2 {
					return NULL_OBJ
				}
				tsMs := float64(0)
				if n, ok := args[0].(*NumberObject); ok {
					tsMs = n.Value
				}
				offset := args[1].Inspect()
				d, err := time.ParseDuration(offset)
				if err != nil {
					return NULL_OBJ
				}
				t := time.UnixMilli(int64(tsMs))
				return &NumberObject{Value: float64(t.Add(d).UnixMilli())}

			case "is_before":
				if len(args) < 2 {
					return FALSE_OBJ
				}
				t1 := float64(0)
				t2 := float64(0)
				if n, ok := args[0].(*NumberObject); ok {
					t1 = n.Value
				}
				if n, ok := args[1].(*NumberObject); ok {
					t2 = n.Value
				}
				return nativeBoolToObject(t1 < t2)

			case "is_after":
				if len(args) < 2 {
					return FALSE_OBJ
				}
				t1 := float64(0)
				t2 := float64(0)
				if n, ok := args[0].(*NumberObject); ok {
					t1 = n.Value
				}
				if n, ok := args[1].(*NumberObject); ok {
					t2 = n.Value
				}
				return nativeBoolToObject(t1 > t2)

			case "today_start":
				now := time.Now().UTC()
				start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
				return &NumberObject{Value: float64(start.UnixMilli())}

			case "today_end":
				now := time.Now().UTC()
				end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999000000, time.UTC)
				return &NumberObject{Value: float64(end.UnixMilli())}

			case "quarter":
				if len(args) < 1 {
					return NULL_OBJ
				}
				tsMs := float64(0)
				if n, ok := args[0].(*NumberObject); ok {
					tsMs = n.Value
				}
				t := time.UnixMilli(int64(tsMs)).UTC()
				q := (int(t.Month())-1)/3 + 1
				return &NumberObject{Value: float64(q)}

			case "weekday":
				if len(args) < 1 {
					return NULL_OBJ
				}
				tsMs := float64(0)
				if n, ok := args[0].(*NumberObject); ok {
					tsMs = n.Value
				}
				t := time.UnixMilli(int64(tsMs)).UTC()
				return &NumberObject{Value: float64(t.Weekday())}
			}
			return NULL_OBJ
		},
	}
}
