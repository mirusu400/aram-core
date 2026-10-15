package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestApplicationCompareBranchTailUsesGuardedSource(t *testing.T) {
	fset, statements := applicationCompareBranchSource(t, "", "")
	helper, err := applicationCompareBranchTailHelper(fset, statements)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"addWithCarry(left, ^right, 1)",
		"b.setNZCV(result, carry, overflow)",
		"b.conditionPassed(condition)",
		"b.recordThumbMicro(&block.thumb[last-1])",
		"b.recordThumbMicro(&block.thumb[last])",
	} {
		if !strings.Contains(helper, want) {
			t.Fatalf("tail helper omitted guarded operation %s", want)
		}
	}
}

func TestApplicationCompareBranchTailRejectsUnknownSource(t *testing.T) {
	for _, mutation := range []struct{ from, to string }{
		{"switch in.op {", "switch other.op {"},
		{"switch in.op {", "switch probe(); in.op {"},
		{"case thumbCompareImmediate:", "case thumbCompareImmediate, thumbMoveImmediate:"},
		{"rd := uint32(instruction>>8) & 7\n\t\t\tresult, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)", "rd := uint32(instruction>>8) & 15\n\t\t\tresult, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)"},
		{"left, right := b.regs[rd], b.regs[rs]", "left, right := b.regs[rs], b.regs[rd]"},
		{"switch op {\n\t\t\tcase 0x0:", "switch probe(); op {\n\t\t\tcase 0x0:"},
		{"case 0xa:", "case 0xb:"},
		{"case 0xa:", "case otherOpcode:"},
		{"addWithCarry(left, ^right, 1)", "addWithCarry(left, ^right, 0)"},
		{"b.conditionPassed(condition)", "otherPredicate(condition)"},
		{"uint32(int32(pc+4) + offset)", "uint32(int32(pc+2) + offset)"},
		{"return index + 1, true, nil, nil", "return index + 2, true, nil, nil"},
	} {
		t.Run(mutation.to, func(t *testing.T) {
			fset, statements := applicationCompareBranchSource(t, mutation.from, mutation.to)
			if _, err := applicationCompareBranchTailHelper(fset, statements); err == nil {
				t.Fatal("accepted an unreviewed CMP/branch semantic shape")
			}
		})
	}
}

func applicationCompareBranchSource(t *testing.T, from, to string) (*token.FileSet, []ast.Stmt) {
	t.Helper()
	source, err := os.ReadFile("../../jit_thumb_micro.go")
	if err != nil {
		t.Fatal(err)
	}
	if from != "" {
		if !strings.Contains(string(source), from) {
			t.Fatalf("mutation source not found: %s", from)
		}
		source = []byte(strings.ReplaceAll(string(source), from, to))
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "guarded.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "executeThumbMicroBlock" {
			continue
		}
		for _, statement := range fn.Body.List {
			if loop, ok := statement.(*ast.RangeStmt); ok {
				return fset, loop.Body.List
			}
		}
	}
	t.Fatal("guarded instruction loop not found")
	return nil, nil
}
