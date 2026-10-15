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

func TestApplicationReturnRewrite(t *testing.T) {
	for _, branch := range []string{"false", "true", "includePC", "instruction&(1<<8)!=0"} {
		for _, exit := range []struct{ source, want string }{
			{"return index+1, " + branch + ", nil, nil", "blockRetired = index + 1"},
			{"return index, " + branch + ", nil, err", "return executed + uint64(index), nil, err"},
			{"return index+1, " + branch + ", &reason, nil", "return executed + uint64(index+1), &reason, nil"},
		} {
			fset, body := applicationReturnTestBody(t, exit.source)
			if err := rewriteApplicationBlockReturns(fset, body); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := format.Node(&out, fset, body); err != nil {
				t.Fatal(err)
			}
			compact := func(text string) string { return strings.Join(strings.Fields(text), "") }
			if !strings.Contains(compact(out.String()), compact(exit.want)) {
				t.Fatalf("rewrite = %s, want %s", out.String(), exit.want)
			}
			if strings.HasSuffix(exit.source, "nil, nil") && !strings.Contains(out.String(), "goto blockDone") {
				t.Fatalf("normal exit did not reach the block boundary: %s", out.String())
			}
		}
	}
}

func TestApplicationReturnRewriteRejectsUnknownShape(t *testing.T) {
	for _, source := range []string{
		"return index, sideEffect(), nil, nil",
		"return index, values[0], nil, nil",
		"return index, <-ready, nil, nil",
		"return index, otherBoolean, nil, nil",
		"return index+2, false, nil, nil",
		"return index, false, nil",
		"f := func() { return index, false, nil, nil }; _ = f",
	} {
		fset, body := applicationReturnTestBody(t, source)
		if err := rewriteApplicationBlockReturns(fset, body); err == nil {
			t.Fatalf("accepted unreviewed return shape: %s", source)
		}
	}
}

func applicationReturnTestBody(t *testing.T, source string) (*token.FileSet, *ast.BlockStmt) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "returns.go", "package test\nfunc execute() { "+source+" }", 0)
	if err != nil {
		t.Fatal(err)
	}
	return fset, file.Decls[0].(*ast.FuncDecl).Body
}
