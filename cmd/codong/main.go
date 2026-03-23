package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/codong-lang/codong/engine/interpreter"
	"github.com/codong-lang/codong/engine/lexer"
	"github.com/codong-lang/codong/engine/parser"
	"github.com/codong-lang/codong/engine/runner"
)

var validProjectName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	switch command {
	case "run":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: codong run <file.cod>")
			os.Exit(2)
		}
		if err := runner.Run(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "eval":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: codong eval <file.cod>")
			os.Exit(2)
		}
		evalFile(os.Args[2])
	case "version":
		fmt.Printf("codong %s\n", version)
	case "fmt":
		fmt.Println("codong fmt: not yet implemented (stage 1)")
	case "build":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: codong build <file.cod> [-o output]")
			os.Exit(2)
		}
		input := os.Args[2]
		output := strings.TrimSuffix(filepath.Base(input), ".cod")
		for i, arg := range os.Args {
			if arg == "-o" && i+1 < len(os.Args) {
				output = os.Args[i+1]
			}
		}
		if err := runner.Build(input, output); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "new":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: codong new <project-name>")
			os.Exit(2)
		}
		newProject(os.Args[2])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Codong - A programming language designed for AI")
	fmt.Println()
	fmt.Println("Usage: codong <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  run <file.cod>     Run a .cod file (Go IR path)")
	fmt.Println("  eval <file.cod>    Evaluate a .cod file (interpreter, no stdlib)")
	fmt.Println("  build <file.cod>   Compile to binary (stage 4)")
	fmt.Println("  new <name>         Create a new project")
	fmt.Println("  fmt [files...]     Format code")
	fmt.Println("  version            Show version")
}

// evalFile runs a .cod file through the AST interpreter (no stdlib support).
func evalFile(path string) {
	source, err := os.ReadFile(path)
	if err != nil {
		writeJSONError("fs", "E5001_FILE_NOT_FOUND", "file not found: "+path, "check file path")
		os.Exit(1)
	}

	l := lexer.New(string(source))
	p := parser.New(l)
	program := p.ParseProgram()

	if len(p.Errors()) > 0 {
		for _, e := range p.Errors() {
			fmt.Fprintf(os.Stderr, "[E1001_SYNTAX_ERROR] %s\n", e)
			fmt.Fprintln(os.Stderr, "  fix: check syntax")
		}
		os.Exit(2)
	}

	interp := interpreter.New()
	// Set workDir to the directory containing the source file
	absPath, _ := filepath.Abs(path)
	interp.SetWorkDir(filepath.Dir(absPath))
	env := interpreter.NewEnvironment()
	result := interp.Eval(program, env)

	if errObj, ok := result.(*interpreter.ErrorObject); ok {
		fmt.Fprintln(os.Stderr, errObj.Inspect())
		os.Exit(1)
	}

	// Keep process alive if web servers are running
	interp.WaitForServers()
}

// runFile runs a .cod file through the Go IR path.
// For stage 1, we use the interpreter as a fallback.

// newProject creates a new Codong project directory.
func newProject(name string) {
	if !validProjectName.MatchString(name) {
		fmt.Fprintln(os.Stderr, "invalid project name: must contain only letters, digits, hyphens, and underscores")
		os.Exit(1)
	}
	dirs := []string{
		name,
		name + "/tests",
		name + "/migrations",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory %s: %v\n", d, err)
			os.Exit(1)
		}
	}

	// main.cod
	writeFile(name+"/main.cod", `// Codong application entry point
print("Hello, Codong!")
`)

	// codong.toml
	writeFile(name+"/codong.toml", fmt.Sprintf(`[project]
name = "%s"
version = "0.1.0"
entry = "main.cod"

[build]
optimize = false
output_dir = "dist"

[test]
timeout = "30s"
coverage_threshold = 80
`, name))

	// .codong.env.example
	writeFile(name+"/.codong.env.example", `# Environment variables for Codong
# Copy this file to .codong.env and fill in values
# CODONG_ENV=dev
# CODONG_ERROR_FORMAT=json
`)

	// .gitignore
	writeFile(name+"/.gitignore", `.codong.env
dist/
*.exe
`)

	fmt.Printf("Created new Codong project: %s\n", name)
	fmt.Printf("  cd %s && codong run main.cod\n", name)
}

func writeFile(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", path, err)
	}
}

func writeJSONError(source, code, message, fix string) {
	errObj := map[string]any{
		"error":   source,
		"code":    code,
		"message": message,
		"fix":     fix,
		"retry":   false,
	}
	b, _ := json.Marshal(errObj)
	fmt.Fprintln(os.Stderr, string(b))
}
