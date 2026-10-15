package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// This contract reads only the generated source. It does not reference the
// proposed generator helper, so the unchanged executor fails an assertion,
// rather than failing to compile because that helper does not exist yet.
func TestApplicationPrivateRouteGeneratedContract(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "../../jit_thumb_application_gen.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var runner *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "executeThumbApplicationMicroBlocks" {
			if runner != nil {
				t.Fatal("multiple application executors")
			}
			runner = fn
		}
	}
	if runner == nil || runner.Body == nil {
		t.Fatal("application executor missing")
	}
	var initializers, writes, guards, oldGuards int
	for _, stmt := range runner.Body.List {
		assignment, ok := stmt.(*ast.AssignStmt)
		if !ok || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			continue
		}
		name, ok := assignment.Lhs[0].(*ast.Ident)
		if ok && name.Name == "privateDataRoute" && applicationRouteContractCondition(assignment.Rhs[0]) {
			initializers++
		}
	}
	ast.Inspect(runner.Body, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.AssignStmt:
			for _, lhs := range current.Lhs {
				if name, ok := lhs.(*ast.Ident); ok && name.Name == "privateDataRoute" {
					writes++
				}
			}
		case *ast.IfStmt:
			if name, ok := current.Cond.(*ast.Ident); ok && name.Name == "privateDataRoute" {
				guards++
			}
			if applicationRouteContractCondition(current.Cond) {
				oldGuards++
			}
		}
		return true
	})
	if initializers != 1 || writes != 1 || guards != 54 || oldGuards != 0 {
		t.Fatalf("call-local initializers=%d writes=%d local guards=%d old guards=%d; want 1/1/54/0",
			initializers, writes, guards, oldGuards)
	}
}

func applicationRouteContractCondition(expr ast.Expr) bool {
	field := func(expr ast.Expr, name string) bool {
		selector, ok := expr.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != name {
			return false
		}
		receiver, ok := selector.X.(*ast.Ident)
		return ok && receiver.Name == "b"
	}
	both, ok := expr.(*ast.BinaryExpr)
	if !ok || both.Op != token.LAND {
		return false
	}
	not, ok := both.X.(*ast.UnaryExpr)
	if !ok || not.Op != token.NOT || !field(not.X, "physicalAccess") {
		return false
	}
	equal, ok := both.Y.(*ast.BinaryExpr)
	if !ok || equal.Op != token.EQL || !field(equal.X, "tlb") {
		return false
	}
	nilValue, ok := equal.Y.(*ast.Ident)
	return ok && nilValue.Name == "nil"
}

func routeHoist26TestBody(t *testing.T, source string) (*token.FileSet, *ast.BlockStmt) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "route.go", "package test\nfunc execute() {"+source+"}", 0)
	if err != nil {
		t.Fatal(err)
	}
	return fset, file.Decls[0].(*ast.FuncDecl).Body
}

func routeHoist26Text(t *testing.T, fset *token.FileSet, body *ast.BlockStmt) string {
	t.Helper()
	var out bytes.Buffer
	if err := format.Node(&out, fset, body); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestApplicationPrivateRouteHoistExactly54Guards(t *testing.T) {
	const original = "if !b.physicalAccess && b.tlb == nil { usePrivate() } else { useScalar() };"
	fset, body := routeHoist26TestBody(t, strings.Repeat(original, 54))
	before := routeHoist26Text(t, fset, body)
	count, err := hoistApplicationPrivateRoute(body)
	if err != nil || count != 54 || requireApplicationPrivateRouteCount(count) != nil {
		t.Fatalf("hoist count=%d error=%v", count, err)
	}
	after := routeHoist26Text(t, fset, body)
	if strings.Count(after, "if privateDataRoute {") != 54 ||
		strings.ReplaceAll(after, "if privateDataRoute {", "if !b.physicalAccess && b.tlb == nil {") != before {
		t.Fatal("route rewrite changed a body, else, statement order or another expression")
	}
}

func TestApplicationPrivateRouteHoistRejectsUnknownShape(t *testing.T) {
	for name, source := range map[string]string{
		"other-receiver":    "if !other.physicalAccess && other.tlb == nil { use() }",
		"mixed-receiver":    "if !b.physicalAccess && other.tlb == nil { use() }",
		"init":              "if x := effect(); !b.physicalAccess && b.tlb == nil { use(x) }",
		"or":                "if !b.physicalAccess || b.tlb == nil { use() }",
		"equality":          "if !b.physicalAccess && b.tlb != nil { use() }",
		"positive-physical": "if b.physicalAccess && b.tlb == nil { use() }",
		"reverse-nil":       "if !b.physicalAccess && nil == b.tlb { use() }",
		"extra-effect":      "if !b.physicalAccess && b.tlb == nil && effect() { use() }",
		"paren-shape":       "if (!b.physicalAccess && b.tlb == nil) { use() }",
		"outside-read":      "x := b.tlb; use(x)",
		"outside-write":     "b.physicalAccess = false",
		"body-write":        "if !b.physicalAccess && b.tlb == nil { b.tlb = nil }",
		"local-conflict":    "privateDataRoute := true; use(privateDataRoute)",
		"receiver-reassign": "b = next; if !b.physicalAccess && b.tlb == nil { use() }",
		"receiver-shadow":   "{ b := next; if !b.physicalAccess && b.tlb == nil { use() } }",
		"receiver-var":      "var b *Backend; use(b)",
		"receiver-range":    "for _, b := range backends { use(b) }",
		"closure":           "func() { if !b.physicalAccess && b.tlb == nil { use() } }()",
	} {
		t.Run(name, func(t *testing.T) {
			fset, body := routeHoist26TestBody(t, source)
			before := routeHoist26Text(t, fset, body)
			if _, err := hoistApplicationPrivateRoute(body); err == nil {
				t.Fatal("unknown route shape was silently accepted")
			}
			if routeHoist26Text(t, fset, body) != before {
				t.Fatal("rejected input was partly rewritten")
			}
		})
	}
	for _, count := range []int{0, 1, 53, 55, 108} {
		if requireApplicationPrivateRouteCount(count) == nil {
			t.Fatalf("accepted route count%d", count)
		}
	}
}
