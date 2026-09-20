package retrylint

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
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
		if argIsComputed(call.Args[0], loop.Body) {
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

// nonRetryableContextErrors maps the context package's sentinel errors to
// a human-readable reason. Both mean the caller gave up or the deadline
// passed; retrying does nothing but waste the remaining budget.
var nonRetryableContextErrors = map[string]string{
	"Canceled":         "the context was canceled",
	"DeadlineExceeded": "the context deadline was exceeded",
}

// nonRetryable4xxStatus lists client-error status codes that describe a
// problem with the request itself, so a retry with the same request will
// fail the same way every time. 429 (Too Many Requests) is left out on
// purpose: unlike the rest of the 4xx range, it is meant to be retried,
// typically after the delay in a Retry-After header.
var nonRetryable4xxStatus = map[int]bool{
	400: true, 401: true, 403: true, 404: true, 405: true, 406: true,
	409: true, 410: true, 411: true, 412: true, 413: true, 414: true,
	415: true, 416: true, 417: true, 422: true, 451: true,
}

// httpStatusConst maps the net/http status constant names that show up in
// this range to their numeric value, since the AST only gives us the name.
var httpStatusConst = map[string]int{
	"StatusBadRequest":                   400,
	"StatusUnauthorized":                 401,
	"StatusForbidden":                    403,
	"StatusNotFound":                     404,
	"StatusMethodNotAllowed":             405,
	"StatusNotAcceptable":                406,
	"StatusConflict":                     409,
	"StatusGone":                         410,
	"StatusLengthRequired":               411,
	"StatusPreconditionFailed":           412,
	"StatusRequestEntityTooLarge":        413,
	"StatusRequestURITooLong":            414,
	"StatusUnsupportedMediaType":         415,
	"StatusRequestedRangeNotSatisfiable": 416,
	"StatusExpectationFailed":            417,
	"StatusUnprocessableEntity":          422,
	"StatusUnavailableForLegalReasons":   451,
	"StatusTooManyRequests":              429,
}

// checkRetryOnNonRetryable flags an `if` inside a loop that recognizes a
// non-retryable outcome (a canceled context, a 4xx response) but whose
// body doesn't exit the loop, meaning execution falls through to the
// retry logic anyway. The check only looks at the shape of the branch,
// not what runs before or after it, so it catches the bug even in loops
// that are otherwise correctly bounded and backed off.
func checkRetryOnNonRetryable(fset *token.FileSet, loop *ast.ForStmt) []Finding {
	var findings []Finding
	inspectShallow(loop.Body, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		reason, ok := nonRetryableReason(ifStmt.Cond)
		if !ok || blockExits(ifStmt.Body) {
			return true
		}
		pos := fset.Position(ifStmt.Pos())
		findings = append(findings, Finding{
			Rule:    "retry-on-non-retryable-error",
			Message: fmt.Sprintf("retries even when %s, which should not be retried", reason),
			Line:    pos.Line,
			Column:  pos.Column,
		})
		return true
	})
	return findings
}

// nonRetryableReason reports whether cond checks for a known non-retryable
// condition, and if so, a human-readable reason describing it.
func nonRetryableReason(cond ast.Expr) (string, bool) {
	switch c := cond.(type) {
	case *ast.BinaryExpr:
		if c.Op != token.EQL {
			return "", false
		}
		if reason, ok := contextErrorReason(c.X); ok {
			return reason, true
		}
		if reason, ok := contextErrorReason(c.Y); ok {
			return reason, true
		}
		if reason, ok := statusCodeReason(c.X, c.Y); ok {
			return reason, true
		}
		return statusCodeReason(c.Y, c.X)
	case *ast.CallExpr:
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Is" || len(c.Args) != 2 {
			return "", false
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "errors" {
			return "", false
		}
		return contextErrorReason(c.Args[1])
	}
	return "", false
}

func contextErrorReason(expr ast.Expr) (string, bool) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "context" {
		return "", false
	}
	reason, ok := nonRetryableContextErrors[sel.Sel.Name]
	return reason, ok
}

// statusCodeReason checks whether field is a "....StatusCode" selector and
// value names a non-retryable 4xx status, either as an http.StatusXxx
// constant or an integer literal.
func statusCodeReason(field, value ast.Expr) (string, bool) {
	sel, ok := field.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "StatusCode" {
		return "", false
	}
	code, ok := statusCodeValue(value)
	if !ok || !nonRetryable4xxStatus[code] {
		return "", false
	}
	return fmt.Sprintf("the response status was %d", code), true
}

func statusCodeValue(expr ast.Expr) (int, bool) {
	switch v := expr.(type) {
	case *ast.SelectorExpr:
		pkg, ok := v.X.(*ast.Ident)
		if !ok || pkg.Name != "http" {
			return 0, false
		}
		code, ok := httpStatusConst[v.Sel.Name]
		return code, ok
	case *ast.BasicLit:
		if v.Kind != token.INT {
			return 0, false
		}
		n, err := strconv.Atoi(v.Value)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// blockExits reports whether block's last statement unconditionally leaves
// the loop (return, break, goto, panic, os.Exit) rather than falling
// through to whatever runs next, including a `continue` back to the top.
func blockExits(block *ast.BlockStmt) bool {
	if len(block.List) == 0 {
		return false
	}
	return stmtExits(block.List[len(block.List)-1])
}

func stmtExits(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return s.Tok == token.BREAK || s.Tok == token.GOTO
	case *ast.ExprStmt:
		call, ok := s.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "panic" {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "os" && sel.Sel.Name == "Exit"
	default:
		return false
	}
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

// argIsComputed reports whether expr's value can vary between iterations:
// either it contains a function call directly (`backoff(attempt)`), or
// it's a plain identifier whose value was assigned, somewhere in body,
// from an expression that contains one (`delay := backoff(attempt)`
// followed by `time.Sleep(delay)`). Without the second case, the very
// common pattern of computing a delay into a variable before sleeping on
// it reads as a fixed delay and produces a false positive.
func argIsComputed(expr ast.Expr, body ast.Node) bool {
	if exprContainsCall(expr) {
		return true
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	return identComputedInBody(body, ident.Name)
}

// exprContainsCall reports whether expr contains a function call anywhere
// in it. Unlike inspectShallow, this walks the full expression tree: an
// argument expression is small and never contains a nested loop.
func exprContainsCall(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if _, ok := n.(*ast.CallExpr); ok {
			found = true
		}
		return true
	})
	return found
}

// identComputedInBody reports whether name is assigned, directly inside
// body, from an expression that contains a function call. It does not
// follow chains of assignments (`a := b; b := backoff()`), only the
// expression assigned to name itself.
func identComputedInBody(body ast.Node, name string) bool {
	found := false
	inspectShallow(body, func(n ast.Node) bool {
		if found {
			return false
		}
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name != name || i >= len(assign.Rhs) {
				continue
			}
			if exprContainsCall(assign.Rhs[i]) {
				found = true
				return false
			}
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
