package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"strconv"
	"strings"
)

// Copy the arithmetic and predicate from the guarded executor, rather than
// introducing a second editable definition of CMP/conditional-branch semantics.
// The intentionally narrow shape checks fail closed when that source changes.
func applicationCompareBranchTailHelper(fset *token.FileSet, statements []ast.Stmt) (string, error) {
	text := func(node ast.Node) string {
		var out bytes.Buffer
		if err := format.Node(&out, fset, node); err != nil {
			panic(err)
		}
		return out.String()
	}
	compact := func(value string) string { return strings.Join(strings.Fields(value), "") }
	var dispatch *ast.SwitchStmt
	for _, statement := range statements {
		candidate, ok := statement.(*ast.SwitchStmt)
		if !ok || candidate.Tag == nil || compact(text(candidate.Tag)) != "in.op" {
			continue
		}
		if dispatch != nil || candidate.Init != nil {
			return "", fmt.Errorf("CMP tail dispatch shape changed")
		}
		dispatch = candidate
	}
	if dispatch == nil {
		return "", fmt.Errorf("CMP tail dispatch not found")
	}
	cases := make(map[string]*ast.CaseClause)
	for _, statement := range dispatch.Body.List {
		clause, ok := statement.(*ast.CaseClause)
		if !ok || len(clause.List) != 1 {
			continue
		}
		name, ok := clause.List[0].(*ast.Ident)
		if !ok {
			continue
		}
		if cases[name.Name] != nil {
			return "", fmt.Errorf("duplicate CMP tail source case %s", name.Name)
		}
		cases[name.Name] = clause
	}
	immediate, alu, branch := cases["thumbCompareImmediate"], cases["thumbALU"], cases["thumbConditionalBranch"]
	if immediate == nil || alu == nil || branch == nil {
		return "", fmt.Errorf("CMP tail source cases not found")
	}
	bodyText := func(body []ast.Stmt) string {
		var out strings.Builder
		for _, statement := range body {
			out.WriteString(text(statement))
		}
		return compact(out.String())
	}
	if bodyText(immediate.Body) != compact(`
rd := uint32(instruction>>8) & 7
result, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)
b.setNZCV(result, carry, overflow)
`) || len(alu.Body) != 5 || bodyText(alu.Body[:4]) != compact(`
op := (instruction >> 6) & 0xf
rs := uint32(instruction>>3) & 7
rd := uint32(instruction) & 7
left, right := b.regs[rd], b.regs[rs]
`) {
		return "", fmt.Errorf("CMP tail operand shape changed")
	}
	inner, ok := alu.Body[4].(*ast.SwitchStmt)
	if !ok || inner.Init != nil || inner.Tag == nil || compact(text(inner.Tag)) != "op" || len(inner.Body.List) != 16 {
		return "", fmt.Errorf("CMP tail ALU dispatch shape changed")
	}
	var compare *ast.CaseClause
	seen := uint16(0)
	for _, statement := range inner.Body.List {
		clause, ok := statement.(*ast.CaseClause)
		if !ok || len(clause.List) != 1 {
			return "", fmt.Errorf("CMP tail ALU arm shape changed")
		}
		literal, ok := clause.List[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.INT {
			return "", fmt.Errorf("CMP tail ALU selector changed")
		}
		opcode, err := strconv.ParseUint(literal.Value, 0, 4)
		if err != nil || seen&(1<<opcode) != 0 {
			return "", fmt.Errorf("CMP tail ALU selectors changed")
		}
		seen |= 1 << opcode
		if opcode == 10 {
			compare = clause
		}
	}
	if seen != 0xffff || compare == nil || bodyText(compare.Body) != compact(`
result, carry, overflow := addWithCarry(left, ^right, 1)
b.setNZCV(result, carry, overflow)
`) || bodyText(branch.Body) != compact(`
condition := uint8(instruction>>8) & 0xf
if b.conditionPassed(condition) {
offset := int32(int8(instruction&0xff)) << 1
b.regs[cpu.RegisterPC] = uint32(int32(pc+4) + offset)
return index + 1, true, nil, nil
}
`) {
		return "", fmt.Errorf("CMP tail arithmetic or branch shape changed")
	}
	predicate := branch.Body[1].(*ast.IfStmt)
	var out strings.Builder
	out.WriteString(`
// executeThumbCompareBranchTail is entered only after a complete ordinary prefix.
// conditionPassed materializes the final CMP flags before either branch edge.
func (b *Backend) executeThumbCompareBranchTail(block *jitBlock) {
tail := block.compareBranchTail
if collectThumbMicro {
last := len(block.thumb)-1
b.recordThumbMicro(&block.thumb[last-1])
b.recordThumbMicro(&block.thumb[last])
}
left := b.regs[tail.left]
right := tail.right
if tail.register { right = b.regs[right] }
b.regs[cpu.RegisterPC] = tail.branchPC
`)
	for _, statement := range compare.Body {
		out.WriteString(text(statement))
		out.WriteByte('\n')
	}
	out.WriteString("b.regs[cpu.RegisterPC] = tail.branchPC + 2\ncondition := tail.condition\nif ")
	out.WriteString(text(predicate.Cond))
	out.WriteString(" { b.regs[cpu.RegisterPC] = tail.target }\n}\n")
	return out.String(), nil
}
