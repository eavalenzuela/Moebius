package api

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestRouter_V1BlockRequiresRBACOnEveryRoute enforces — at AST level — that
// every HTTP route registered inside the authenticated `/v1` block carries
// an `rbac.Require(...)` middleware. Authentication (API key / OIDC) proves
// *who* the caller is; `rbac.Require` proves *what* they are allowed to do.
// Missing this decorator on a write route was issue I3 in the security
// validation: the omission slipped in during refactoring because no gate
// existed. This test is that gate.
//
// The check is purely static — it parses router.go and walks the AST — so
// it runs in unit-test time with no DB, no server, no handlers.
//
// Scope: only routes registered via `r.Get/Post/Put/Patch/Delete` are
// covered. If someone introduces routes via `r.Handle` or `r.Method` inside
// /v1, extend `httpVerbMethodNames` below.
func TestRouter_V1BlockRequiresRBACOnEveryRoute(t *testing.T) {
	violations, err := collectV1RouteViolations("router.go", nil)
	if err != nil {
		t.Fatalf("parse router.go: %v", err)
	}
	if len(violations) > 0 {
		t.Errorf("found %d authenticated /v1 route(s) missing rbac.Require(...):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// TestRouter_RBACCheckDetectsMissingDecorator verifies the check itself:
// a synthetic /v1 block with a plain `r.Get` (no With, no RBAC) must be
// reported. Without this negative test, a broken check could silently
// pass — the decorator assertion only has value if we know the walker
// actually detects violations.
func TestRouter_RBACCheckDetectsMissingDecorator(t *testing.T) {
	src := `package api

func fake() {
	r.Route("/v1", func(r chi.Router) {
		r.Get("/no-with-at-all", handler)
	})
}
`
	violations, err := collectV1RouteViolations("synthetic.go", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], "/no-with-at-all") {
		t.Errorf("violation message should name the offending path, got: %s", violations[0])
	}
}

// TestRouter_RBACCheckDetectsWithButNoRBAC verifies that `r.With(...)` alone
// is not sufficient — the With chain must include `rbac.Require`.
func TestRouter_RBACCheckDetectsWithButNoRBAC(t *testing.T) {
	src := `package api

func fake() {
	r.Route("/v1", func(r chi.Router) {
		r.With(MaxBytes(1024)).Post("/with-but-no-rbac", handler)
	})
}
`
	violations, err := collectV1RouteViolations("synthetic.go", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], "/with-but-no-rbac") {
		t.Errorf("violation message should name the offending path, got: %s", violations[0])
	}
}

// TestRouter_RBACCheckAcceptsValidRoute is the positive control: a route
// properly guarded by `rbac.Require` must produce no violations.
func TestRouter_RBACCheckAcceptsValidRoute(t *testing.T) {
	src := `package api

func fake() {
	r.Route("/v1", func(r chi.Router) {
		r.With(rbac.Require(rbac.PermUsersWrite)).Post("/good", handler)
		r.With(MaxBytes(1024), rbac.Require(rbac.PermFilesWrite)).Put("/also-good", handler)
	})
}
`
	violations, err := collectV1RouteViolations("synthetic.go", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected zero violations for well-guarded routes, got: %v", violations)
	}
}

// collectV1RouteViolations parses the given Go source (filename + optional
// src bytes; pass nil src to read from disk) and returns a list of routes
// inside the `r.Route("/v1", ...)` block that are missing an `rbac.Require`
// decorator. Each entry is a human-readable "file:line verb path — reason".
func collectV1RouteViolations(filename string, src any) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}

	v1Block := findV1RouteBlock(file)
	if v1Block == nil {
		return nil, fmt.Errorf(`r.Route("/v1", func(r chi.Router){...}) not found in %s`, filename)
	}

	httpVerbMethodNames := map[string]bool{
		"Get":    true,
		"Post":   true,
		"Put":    true,
		"Patch":  true,
		"Delete": true,
		"Head":   true,
	}

	var violations []string
	ast.Inspect(v1Block, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !httpVerbMethodNames[sel.Sel.Name] {
			return true
		}
		// The receiver of the verb call must be `r.With(...)`.
		withCall, ok := sel.X.(*ast.CallExpr)
		if !ok {
			violations = append(violations,
				formatViolation(fset, call, sel.Sel.Name, "no r.With(...) chain"))
			return true
		}
		withSel, ok := withCall.Fun.(*ast.SelectorExpr)
		if !ok || withSel.Sel.Name != "With" {
			violations = append(violations,
				formatViolation(fset, call, sel.Sel.Name, "receiver is not r.With(...)"))
			return true
		}
		if !withArgsIncludeRBACRequire(withCall.Args) {
			violations = append(violations,
				formatViolation(fset, call, sel.Sel.Name, "r.With(...) does not include rbac.Require(...)"))
		}
		return true
	})

	return violations, nil
}

// findV1RouteBlock locates the FuncLit body passed to r.Route("/v1", ...).
// It looks for any CallExpr whose selector name is "Route" and whose first
// argument is the string literal "/v1". This matches the router structure
// even if nested inside other functions.
func findV1RouteBlock(file *ast.File) *ast.FuncLit {
	var found *ast.FuncLit
	ast.Inspect(file, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Route" {
			return true
		}
		if len(call.Args) != 2 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || lit.Value != `"/v1"` {
			return true
		}
		fn, ok := call.Args[1].(*ast.FuncLit)
		if !ok {
			return true
		}
		found = fn
		return false
	})
	return found
}

// withArgsIncludeRBACRequire reports whether any argument in a With(...)
// call is itself a call to `rbac.Require(...)`.
func withArgsIncludeRBACRequire(args []ast.Expr) bool {
	for _, arg := range args {
		call, ok := arg.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			continue
		}
		if pkg.Name == "rbac" && sel.Sel.Name == "Require" {
			return true
		}
	}
	return false
}

func formatViolation(fset *token.FileSet, call *ast.CallExpr, verb, reason string) string {
	pos := fset.Position(call.Pos())
	path := "<unknown>"
	if len(call.Args) > 0 {
		if lit, ok := call.Args[0].(*ast.BasicLit); ok {
			path = lit.Value
		}
	}
	return fmt.Sprintf("%s:%d %s %s — %s", pos.Filename, pos.Line, verb, path, reason)
}
