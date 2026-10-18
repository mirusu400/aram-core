package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This independent test-local inverse validates and removes only the reviewed
// case-local indexed-four boundary. The existing R5/R6/R7 whole-file token pins then still
// cover every previously shipped instruction, error, route and block boundary.
// Historical fixtures without the new boundary pass through unchanged.
func restoreIndexedLoadReturnBoundaryForLegacyRoot(raw []byte) ([]byte, bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "indexed-four-contract.go", raw, 0)
	if err != nil {
		return nil, false, err
	}
	text := func(node ast.Node) string {
		var output bytes.Buffer
		if format.Node(&output, fset, node) != nil {
			return ""
		}
		return strings.Join(strings.Fields(output.String()), "")
	}
	methodCalls, directCalls, metadataReads := 0, 0, 0
	ast.Inspect(file, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok {
			if selector.Sel.Name == "tryThumbApplicationIndexedLoadReturnAfterFirst" {
				methodCalls++
			}
			if selector.Sel.Name == "tryThumbApplicationIndexedLoadReturn" {
				directCalls++
			}
			if selector.Sel.Name == "indexedLoadReturn" {
				metadataReads++
			}
		}
		return true
	})
	if methodCalls == 0 && directCalls == 0 && metadataReads == 0 {
		return raw, false, nil
	}
	fail := func(message string) ([]byte, bool, error) {
		return nil, false, fmt.Errorf("indexed-four generated boundary: %s", message)
	}
	if methodCalls != 1 || directCalls != 0 || metadataReads != 1 {
		return fail("helper/metadata occurrence count")
	}
	var runner *ast.FuncDecl
	for _, declaration := range file.Decls {
		if candidate, ok := declaration.(*ast.FuncDecl); ok && candidate.Name.Name == "executeThumbApplicationMicroBlocks" {
			if runner != nil {
				return fail("duplicate application runner")
			}
			runner = candidate
		}
	}
	if runner == nil || runner.Body == nil {
		return fail("application runner missing")
	}
	var outer *ast.ForStmt
	for _, statement := range runner.Body.List {
		if candidate, ok := statement.(*ast.ForStmt); ok && text(candidate.Cond) == "executed<limit" {
			if outer != nil {
				return fail("duplicate execution loop")
			}
			outer = candidate
		}
	}
	if outer == nil || outer.Body == nil || len(outer.Body.List) < 11 {
		return fail("execution loop missing or shortened")
	}
	expectedPrelude := []string{
		"blockInstructions:=len(block.thumb)",
		"ifuint64(blockInstructions)>limit-executed{returnexecuted,nil,nil}",
		"ifcollectThumbMicro{b.recordThumbBlock(block)}",
		"prefixInstructions:=blockInstructions",
		"ifblock.compareBranchTail!=nil{prefixInstructions-=2}",
		"instructions:=block.thumb[:prefixInstructions]",
		"blockGeneration:=b.jitGen",
		"blockRetired:=blockInstructions",
	}
	for index, expected := range expectedPrelude {
		if text(outer.Body.List[index]) != expected {
			return fail(fmt.Sprintf("prelude order at %d", index))
		}
	}
	loop, ok := outer.Body.List[8].(*ast.RangeStmt)
	if !ok || loop.Tok != token.DEFINE || loop.Value != nil || text(loop.Key) != "index" || text(loop.X) != "instructions" || loop.Body == nil || len(loop.Body.List) < 6 {
		return fail("original instruction range or all-block-hook absence")
	}
	expectedInstructionPrelude := []string{
		"in:=&instructions[index]",
		"pc:=in.pc",
		"ifcollectThumbMicro{b.recordThumbMicro(in)}",
		"instruction:=in.raw",
		"b.regs[cpu.RegisterPC]=pc+2",
	}
	for index, expected := range expectedInstructionPrelude {
		if text(loop.Body.List[index]) != expected {
			return fail(fmt.Sprintf("instruction prelude order at %d", index))
		}
	}
	dispatch, ok := loop.Body.List[5].(*ast.SwitchStmt)
	if !ok || dispatch.Init != nil || text(dispatch.Tag) != "in.appOp" || dispatch.Body == nil {
		return fail("original application dispatch missing after PC prestore")
	}
	var deadShift *ast.CaseClause
	for _, statement := range dispatch.Body.List {
		clause, ok := statement.(*ast.CaseClause)
		if !ok {
			return fail("malformed application switch")
		}
		if len(clause.List) == 1 && text(clause.List[0]) == "thumbShiftImmediate+thumbDeadFlagsOffset" {
			if deadShift != nil {
				return fail("duplicate dead shift case")
			}
			deadShift = clause
		}
	}
	if deadShift == nil || len(deadShift.Body) < 2 || text(deadShift.Body[1]) != "constflagsLive=false" {
		return fail("dead shift case or original first statement missing")
	}
	boundary, ok := deadShift.Body[0].(*ast.IfStmt)
	if !ok || boundary.Init != nil || boundary.Else != nil || len(boundary.Body.List) != 1 ||
		text(boundary.Cond) != "block.indexedLoadReturn!=nil&&b.tryThumbApplicationIndexedLoadReturnAfterFirst(block)" ||
		text(boundary.Body.List[0]) != "gotoblockDone" {
		return fail("exact first-case guarded call, arity, index or blockDone jump")
	}
	start := fset.PositionFor(boundary.Pos(), false).Offset
	end := fset.PositionFor(boundary.End(), false).Offset
	result := append([]byte(nil), raw[:start]...)
	result = append(result, raw[end:]...)
	return result, true, nil
}

func TestApplicationIndexedLoadReturnGeneratedBoundaryRoot(t *testing.T) {
	output := filepath.Join(t.TempDir(), "indexed-four-generated.go")
	if err := generate("../../jit_thumb_micro.go", output, false); err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{output, "../../jit_thumb_application_gen.go"} {
		raw, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		saved := append([]byte(nil), raw...)
		restored, found, err := restoreIndexedLoadReturnBoundaryForLegacyRoot(raw)
		if err != nil || !found {
			t.Fatalf("candidate boundary absent or invalid for %s: found=%t error=%v", filename, found, err)
		}
		if !bytes.Equal(raw, saved) || bytes.Equal(raw, restored) {
			t.Fatal("boundary inverse mutated its input or removed nothing")
		}
		if _, found, err := restoreIndexedLoadReturnBoundaryForLegacyRoot(restored); err != nil || found {
			t.Fatalf("second inverse must be historical pass-through: found=%t error=%v", found, err)
		}
	}
	if err := generate("../../jit_thumb_micro.go", output, true); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationIndexedLoadReturnBoundaryRejectsMutationsRoot(t *testing.T) {
	output := filepath.Join(t.TempDir(), "indexed-four-generated.go")
	if err := generate("../../jit_thumb_micro.go", output, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	guardPrefix := "if block.indexedLoadReturn != nil && b.tryThumbApplicationIndexedLoadReturnAfterFirst(block) {"
	guardAt := strings.Index(string(raw), guardPrefix)
	if guardAt < 0 {
		t.Fatal("case-local mutation fixture boundary missing")
	}
	jumpAt := strings.Index(string(raw[guardAt:]), "goto blockDone")
	if jumpAt < 0 {
		t.Fatal("case-local mutation fixture jump missing")
	}
	guardAndJump := string(raw[guardAt : guardAt+jumpAt+len("goto blockDone")])
	for _, mutation := range []struct{ name, before, after string }{
		{"metadata", "block.indexedLoadReturn != nil", "block.indexedLoadReturn == nil"},
		{"arguments", "b.tryThumbApplicationIndexedLoadReturnAfterFirst(block)", "b.tryThumbApplicationIndexedLoadReturnAfterFirst(nil)"},
		{"extra-argument", "b.tryThumbApplicationIndexedLoadReturnAfterFirst(block)", "b.tryThumbApplicationIndexedLoadReturnAfterFirst(block, limit)"},
		{"wrong-target", guardAndJump, strings.Replace(guardAndJump, "goto blockDone", "goto indexedWrongTarget", 1)},
		{"budget", "uint64(blockInstructions) > limit-executed", "uint64(blockInstructions) >= limit-executed"},
		{"generation", "blockGeneration := b.jitGen", "blockGeneration := uint64(0)"},
		{"retirement", "blockRetired := blockInstructions", "blockRetired := 0"},
		{"duplicate-call", "b.tryThumbApplicationIndexedLoadReturnAfterFirst(block)", "b.tryThumbApplicationIndexedLoadReturnAfterFirst(block) && b.tryThumbApplicationIndexedLoadReturnAfterFirst(block)"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			if strings.Count(string(raw), mutation.before) != 1 {
				t.Fatalf("mutation anchor not unique: %s", mutation.before)
			}
			mutated := []byte(strings.Replace(string(raw), mutation.before, mutation.after, 1))
			if _, found, err := restoreIndexedLoadReturnBoundaryForLegacyRoot(mutated); err == nil && found {
				t.Fatal("unreviewed boundary mutation accepted")
			}
		})
	}
	without, found, err := restoreIndexedLoadReturnBoundaryForLegacyRoot(raw)
	if err != nil || !found {
		t.Fatalf("positive mutation fixture invalid: found=%t error=%v", found, err)
	}
	if _, found, err := restoreIndexedLoadReturnBoundaryForLegacyRoot(without); err != nil || found {
		t.Fatalf("deleted boundary must not count as a positive contract: found=%t error=%v", found, err)
	}
}
