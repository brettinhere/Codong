package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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

	goSource, parseErrors := compile(string(source))
	if len(parseErrors) > 0 {
		for _, e := range parseErrors {
			fmt.Fprintln(os.Stderr, e)
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

	goSource, parseErrors := compile(string(source))
	if len(parseErrors) > 0 {
		for _, e := range parseErrors {
			fmt.Fprintln(os.Stderr, e)
		}
		return fmt.Errorf("parse errors")
	}

	return buildGoSource(goSource, outputPath)
}

func compile(source string) (string, []string) {
	l := lexer.New(source)
	p := parser.New(l)
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return "", p.Errors()
	}
	goSource := goirgen.Generate(program)
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
		// go run main.go
		cmd := exec.Command("go", "run", "main.go")
		cmd.Dir = dir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		return cmd.Run()
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
