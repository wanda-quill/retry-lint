package retrylint

import (
	"go/ast"
	"go/token"
)

// boundingOperators are comparisons that plausibly gate a loop on an
// attempt count (attempts < max, retries >= limit, and so on).
//
// Equality (== and !=) is deliberately excluded: `err == nil` and
// `err != nil` are the most common expressions inside a retry loop, and
// counting them would make almost every unbounded loop look bounded.
var boundingOperators = map[token.Token]bool{
	token.LSS: true,
	token.LEQ: true,
	token.GTR: true,
	token.GEQ: true,
}

// checkUnboundedRetry flags `for { ... }` loops that sleep between
// attempts but never compare anything against a limit, so nothing in the
// loop body can stop it from retrying forever.
func checkUnboundedRetry(fset *token.FileSet, loop *ast.ForStmt) []Finding {
	if loop.Cond != nil {
		// Already has a loop condition; treat that as the bound.
		return nil
	}
	if !containsSleepCall(loop.Body) {
		return nil
	}
	if containsBoundingComparison(loop.Body) {
		return nil
	}

	pos := fset.Position(loop.Pos())
	return []Finding{{
		Rule:    "unbounded-retry",
		Message: "infinite retry loop has no attempt limit or bound check",
		Line:    pos.Line,
		Column:  pos.Column,
	}}
}

// checkFixedDelay flags time.Sleep calls, inside any loop, whose duration
// argument contains no function call. A literal or constant expression
// (time.Second, 2*time.Second) produces the same delay on every attempt;
// a call (backoff(attempt), jitter(base)) is assumed to vary it.
func checkFixedDelay(fset *token.FileSet, loop *ast.ForStmt) []Finding {
	var findings []Finding
	inspectShallow(loop.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isTimeSleepCall(call) || len(call.Args) != 1 {
			return true
		}
		if argIsComputed(call.Args[0]) {
			return true
		}
		pos := fset.Position(call.Pos())
		findings = append(findings, Finding{
			Rule:    "fixed-delay-no-jitter",
			Message: "retry delay is a fixed duration with no backoff or jitter",
			Line:    pos.Line,
			Column:  pos.Column,
		})
		return true
	})
	return findings
}

func containsSleepCall(body ast.Node) bool {
	found := false
	inspectShallow(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && isTimeSleepCall(call) {
			found = true
			return false
		}
		return true
	})
	return found
}

func containsBoundingComparison(body ast.Node) bool {
	found := false
	inspectShallow(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if bin, ok := n.(*ast.BinaryExpr); ok && boundingOperators[bin.Op] {
			found = true
			return false
		}
		return true
	})
	return found
}

func isTimeSleepCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sleep" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "time"
}

// argIsComputed reports whether expr contains a function call anywhere in
// it. Unlike inspectShallow, this walks the full expression tree: an
// argument expression is small and never contains a nested loop.
func argIsComputed(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if _, ok := n.(*ast.CallExpr); ok {
			found = true
		}
		return true
	})
	return found
}

// inspectShallow walks n like ast.Inspect but does not descend into
// nested for/range loops. Each loop is checked independently by the
// caller, so a sleep or comparison that belongs to a nested loop must not
// be attributed to the loop containing it.
func inspectShallow(n ast.Node, visit func(ast.Node) bool) {
	ast.Inspect(n, func(node ast.Node) bool {
		if node == nil {
			return false
		}
		if node != n {
			switch node.(type) {
			case *ast.ForStmt, *ast.RangeStmt:
				return false
			}
		}
		return visit(node)
	})
}
