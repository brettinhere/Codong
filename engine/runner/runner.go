package runner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/codong-lang/codong/engine/goirgen"
	"github.com/codong-lang/codong/engine/lexer"
	"github.com/codong-lang/codong/engine/parser"
)

// Run compiles and runs a .cod file via Go IR.
func Run(codFile string) error {
	source, err := os.ReadFile(codFile)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", codFile, err)
	}

	sourceDir := filepath.Dir(codFile)
	if absDir, err := filepath.Abs(sourceDir); err == nil {
		sourceDir = absDir
	}

	goSource, parseErrors := compile(string(source), sourceDir)
	if len(parseErrors) > 0 {
		for _, e := range parseErrors {
			fmt.Printf("[E1001_SYNTAX_ERROR] %s\n", e)
			fmt.Println("  fix: check syntax")
		}
		return fmt.Errorf("parse errors")
	}

	return runGoSource(goSource)
}

// Build compiles a .cod file to a standalone binary.
func Build(codFile, outputPath string) error {
	source, err := os.ReadFile(codFile)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", codFile, err)
	}

	sourceDir := filepath.Dir(codFile)
	if absDir, err := filepath.Abs(sourceDir); err == nil {
		sourceDir = absDir
	}

	goSource, parseErrors := compile(string(source), sourceDir)
	if len(parseErrors) > 0 {
		for _, e := range parseErrors {
			fmt.Fprintln(os.Stderr, e)
		}
		return fmt.Errorf("parse errors")
	}

	return buildGoSource(goSource, outputPath)
}

func compile(source string, sourceDir string) (string, []string) {
	l := lexer.New(source)
	p := parser.New(l)
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		var errs []string
		for _, e := range p.Errors() {
			errs = append(errs, e)
		}
		return "", errs
	}
	goSource := goirgen.Generate(program, sourceDir)
	return goSource, nil
}

func runGoSource(goSource string) error {
	dir, err := os.MkdirTemp("", "codong-run-*")
	if err != nil {
		return fmt.Errorf("cannot create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	return execInDir(dir, goSource, "run")
}

func buildGoSource(goSource, outputPath string) error {
	dir, err := os.MkdirTemp("", "codong-build-*")
	if err != nil {
		return fmt.Errorf("cannot create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("invalid output path: %w", err)
	}

	return execInDir(dir, goSource, "build", absOutput)
}

func execInDir(dir, goSource, mode string, extra ...string) error {
	// Write main.go
	mainFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainFile, []byte(goSource), 0644); err != nil {
		return fmt.Errorf("cannot write main.go: %w", err)
	}

	// Write go.mod
	goMod := `module codong-app

go 1.22

require modernc.org/sqlite v1.47.0
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		return fmt.Errorf("cannot write go.mod: %w", err)
	}

	// Run go mod tidy
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	if mode == "run" {
		// go run main.go — intercept stderr to convert Go errors to Codong errors
		cmd := exec.Command("go", "run", "main.go")
		cmd.Dir = dir
		cmd.Stdout = os.Stdout
		cmd.Stdin = os.Stdin
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		err := cmd.Run()
		if err != nil {
			stderr := stderrBuf.String()
			// Check if it's a Go compilation error (not a runtime error)
			if strings.Contains(stderr, "# command-line-arguments") {
				// Convert Go compile errors to Codong error format
				codongErr := translateGoError(stderr)
				fmt.Print(codongErr) // to stdout for test compatibility
				os.Exit(1)
			}
			// Check for runtime panics and convert to Codong errors
			if strings.Contains(stderr, "goroutine") && strings.Contains(stderr, "panic") {
				codongErr := translateRuntimePanic(stderr)
				if codongErr != "" {
					fmt.Print(codongErr)
					os.Exit(1)
				}
			}
			// If stderr is empty or only contains "exit status N" from go run,
			// the program already handled the error output
			// (e.g., via cPanicExit which prints to stdout and calls os.Exit)
			trimmed := strings.TrimSpace(stderr)
			if trimmed == "" || strings.HasPrefix(trimmed, "exit status") {
				os.Exit(1)
			}
			// Runtime error — pass through
			fmt.Fprint(os.Stderr, stderrBuf.String())
			os.Exit(1)
		}
		// Success — pass through any stderr (like server listening messages)
		if stderrBuf.Len() > 0 {
			fmt.Fprint(os.Stderr, stderrBuf.String())
		}
		return nil
	}

	// go build -o output main.go
	outputPath := extra[0]
	cmd := exec.Command("go", "build", "-o", outputPath, "main.go")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Built: %s\n", outputPath)
	return nil
}

// translateRuntimePanic converts Go runtime panics to Codong error format.
func translateRuntimePanic(stderr string) string {
	type codongError struct {
		Code    string `json:"code"`
		Error   string `json:"error"`
		Message string `json:"message"`
		Fix     string `json:"fix"`
		Retry   bool   `json:"retry"`
	}

	var ce codongError
	ce.Error = "runtime"
	ce.Retry = false

	switch {
	case strings.Contains(stderr, "interface conversion") && strings.Contains(stderr, "func("):
		ce.Code = "E1004_UNDEFINED_FUNC"
		ce.Message = "attempted to call a non-function value"
		ce.Fix = "ensure the value is a function before calling it"
	case strings.Contains(stderr, "runtime: goroutine stack exceeds"):
		ce.Code = "E9002_STACK_OVERFLOW"
		ce.Message = "maximum call stack exceeded"
		ce.Fix = "check for infinite recursion or add a base case"
	case strings.Contains(stderr, "index out of range"):
		ce.Code = "E1005_INDEX_ERROR"
		ce.Message = "list index out of bounds"
		ce.Fix = "check the list length before accessing by index"
	default:
		return ""
	}

	s := fmt.Sprintf("[%s] %s\n", ce.Code, ce.Message)
	if ce.Fix != "" {
		s += fmt.Sprintf("  fix: %s\n", ce.Fix)
	}
	return s
}

// translateGoError converts Go compiler errors to Codong error format.
func translateGoError(stderr string) string {
	type codongError struct {
		Code    string `json:"code"`
		Error   string `json:"error"`
		Message string `json:"message"`
		Fix     string `json:"fix"`
		Retry   bool   `json:"retry"`
	}

	for _, line := range strings.Split(stderr, "\n") {
		// Skip non-error lines
		if !strings.Contains(line, "main.go:") {
			continue
		}

		// Extract error message: "./main.go:123:45: error message here"
		// Find the third colon (after file:line:col)
		idx := 0
		colons := 0
		for i, c := range line {
			if c == ':' {
				colons++
				if colons == 3 {
					idx = i + 1
					break
				}
			}
		}
		if idx == 0 { continue }
		msg := strings.TrimSpace(line[idx:])

		var ce codongError
		ce.Error = "runtime"
		ce.Retry = false

		switch {
		case strings.Contains(msg, "undefined:"):
			varName := strings.TrimSpace(strings.TrimPrefix(msg, "undefined:"))
			// Check for common user mistakes
			if varName == "_len" || varName == "len" {
				ce.Code = "E1004_UNDEFINED_FUNC"
				ce.Message = "len() is not a Codong function. Use .len() method instead"
				ce.Fix = "use items.len() instead of len(items)"
			} else if varName == "console" {
				ce.Code = "E1004_UNDEFINED_FUNC"
				ce.Message = "console.log() is not a Codong function"
				ce.Fix = "use print() instead: print(\"your message\")"
			} else if varName == "log" {
				ce.Code = "E1004_UNDEFINED_FUNC"
				ce.Message = "log() is not a Codong function"
				ce.Fix = "use print() instead: print(\"your message\")"
			} else {
				ce.Code = "E1003_UNDEFINED_VAR"
				ce.Message = fmt.Sprintf("variable '%s' is not defined", varName)
				ce.Fix = fmt.Sprintf("declare %s before using it: %s = value", varName, varName)
			}

		case strings.Contains(msg, "declared and not used:"):
			varName := strings.TrimSpace(strings.TrimPrefix(msg, "declared and not used:"))
			ce.Code = "E1001_SYNTAX_ERROR"
			ce.Message = fmt.Sprintf("cannot assign to const '%s'", varName)
			ce.Fix = "remove const declaration or use a different variable name"

		case strings.Contains(msg, "cannot use _ as value"):
			ce.Code = "E1001_SYNTAX_ERROR"
			ce.Message = "invalid use of _ identifier"
			ce.Fix = "use a named variable instead"

		case strings.Contains(msg, "use of untyped nil"):
			ce.Code = "E1001_SYNTAX_ERROR"
			ce.Message = "invalid nil assignment"
			ce.Fix = "check the expression"

		case strings.Contains(msg, "len (built-in) must be called"):
			ce.Code = "E1004_UNDEFINED_FUNC"
			ce.Message = "len() is not a Codong function. Use .len() method instead"
			ce.Fix = "use items.len() instead of len(items)"

		case strings.Contains(msg, "too many return values"):
			ce.Code = "E1001_SYNTAX_ERROR"
			ce.Message = "nested try/catch error propagation"
			ce.Fix = "simplify error handling"

		default:
			continue
		}

		s := fmt.Sprintf("[%s] %s\n", ce.Code, ce.Message)
		if ce.Fix != "" {
			s += fmt.Sprintf("  fix: %s\n", ce.Fix)
		}
		return s
	}

	// If no pattern matched, return original
	return stderr
}
