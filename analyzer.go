// Package retrylint statically checks Go source for retry loops that are
// likely to misbehave in production: loops with no attempt limit, and
// retry delays that never change or vary, which turn a single outage into
// a synchronized retry storm against whatever they're hitting.
//
// The analysis works on syntax only (go/parser), not on type-checked
// packages, so it never needs to load a module's dependencies to run.
package retrylint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
)

// Finding is a single rule violation at a specific source location.
type Finding struct {
	Rule    string
	Message string
	Line    int
	Column  int
}

// Analyze parses src as a Go source file and runs every rule against it.
// filename is used only for error messages produced by the parser.
func Analyze(filename string, src []byte) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.AllErrors)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}

	var findings []Finding
	ast.Inspect(file, func(n ast.Node) bool {
		loop, ok := n.(*ast.ForStmt)
		if !ok {
			return true
		}
		findings = append(findings, checkUnboundedRetry(fset, loop)...)
		findings = append(findings, checkFixedDelay(fset, loop)...)
		return true
	})

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Column < findings[j].Column
	})
	return findings, nil
}
