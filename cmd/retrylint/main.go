// Command retrylint runs retry-policy checks over Go source files and
// prints one line per finding in the usual "file:line:col: message" shape.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wanda-quill/retry-lint"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"."}
	}

	files, err := collectGoFiles(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "retrylint:", err)
		os.Exit(2)
	}

	foundIssue := false
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "retrylint:", err)
			os.Exit(2)
		}

		findings, err := retrylint.Analyze(path, src)
		if err != nil {
			fmt.Fprintln(os.Stderr, "retrylint:", err)
			os.Exit(2)
		}

		for _, f := range findings {
			foundIssue = true
			fmt.Printf("%s:%d:%d: %s [%s]\n", path, f.Line, f.Column, f.Message, f.Rule)
		}
	}

	if foundIssue {
		os.Exit(1)
	}
}

// collectGoFiles expands args (files or directories) into a flat list of
// .go files, skipping tests and dot-directories like .git.
func collectGoFiles(args []string) ([]string, error) {
	var files []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, arg)
			continue
		}

		err = filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != arg && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}
