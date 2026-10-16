package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cachedStore41FaultR7Source(t *testing.T) []byte {
	t.Helper()
	file := filepath.Join(t.TempDir(), "generated.go")
	if err := generate("../../jit_thumb_micro.go", file, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// Anchor the unmutated R7 fixture before any producer/negative-case mutation.
	// The mechanical inverse core never calls this fixture or a producer matcher.
	restored := cachedStore41RestoreFaultPromotionCore(t, raw)
	cachedStore41RequireWholeFilePin(t, restored, cachedStore41LegacyR7TokenSHA256)
	return restored
}

func cachedStore41FaultText(t *testing.T, fset *token.FileSet, node ast.Node) string {
	t.Helper()
	var out bytes.Buffer
	if err := format.Node(&out, fset, node); err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(out.String()), "")
}

func cachedStore41FaultTree(t *testing.T, raw []byte) (*token.FileSet, *ast.File, *ast.FuncDecl, *ast.RangeStmt) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "independent-fault41.go", raw, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	var loop *ast.RangeStmt
	for _, declaration := range file.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if !ok || candidate.Name.Name != "executeThumbApplicationMicroBlocks" {
			continue
		}
		if fn != nil {
			t.Fatal("duplicate application executor")
		}
		fn = candidate
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if candidate, ok := node.(*ast.RangeStmt); ok &&
				cachedStore41FaultText(t, fset, candidate.X) == "instructions" {
				if loop != nil {
					t.Fatal("duplicate instruction loop")
				}
				loop = candidate
			}
			return true
		})
	}
	if fn == nil || loop == nil {
		t.Fatal("executor or instruction loop missing")
	}
	return fset, file, fn, loop
}

// Independent inverse. It does not call the producer or its matchers. Restoring
// the whole frozen R7 file also checks all original error types/args, scopes,
// := declarations, partial-fault ordering, successful exits and old tail.
func cachedStore41RestoreFaultPromotionCore(t *testing.T, raw []byte) []byte {
	t.Helper()
	const slot = "cachedStore41FaultError"
	const target = "cachedStore41Fault"
	fset, _, fn, loop := cachedStore41FaultTree(t, raw)
	compact := func(node ast.Node) string { return cachedStore41FaultText(t, fset, node) }
	if len(fn.Body.List) != 5 || compact(fn.Body.List[1]) != "var"+slot+"error" ||
		len(loop.Body.List) != 8 {
		t.Fatal("shared error slot or loop scope changed")
	}
	step, ok := loop.Body.List[6].(*ast.BranchStmt)
	if !ok || step.Tok != token.CONTINUE || step.Label != nil {
		t.Fatal("normal fallthrough must continue before the cold label")
	}
	label, ok := loop.Body.List[7].(*ast.LabeledStmt)
	if !ok || label.Label.Name != target ||
		compact(label.Stmt) != "returnexecuted+uint64(index),nil,"+slot {
		t.Fatal("shared label retirement/error return changed")
	}
	slotObject := fn.Body.List[1].(*ast.DeclStmt).Decl.(*ast.GenDecl).Specs[0].(*ast.ValueSpec).Names[0].Obj
	if slotObject == nil {
		t.Fatal("shared slot has no lexical object")
	}
	oldReturn := func(errorValue ast.Expr) ast.Stmt {
		return &ast.ReturnStmt{Results: []ast.Expr{
			&ast.BinaryExpr{X: ast.NewIdent("executed"), Op: token.ADD,
				Y: &ast.CallExpr{Fun: ast.NewIdent("uint64"), Args: []ast.Expr{ast.NewIdent("index")}}},
			ast.NewIdent("nil"), errorValue}}
	}
	jump := func(statement ast.Stmt) bool {
		branch, ok := statement.(*ast.BranchStmt)
		return ok && branch.Tok == token.GOTO && branch.Label != nil && branch.Label.Name == target
	}
	slotName := func(expression ast.Expr) bool {
		name, ok := expression.(*ast.Ident)
		return ok && name.Name == slot && name.Obj == slotObject
	}
	condition := func(statement *ast.IfStmt) *ast.Ident {
		test, ok := statement.Cond.(*ast.BinaryExpr)
		if !ok || test.Op != token.NEQ || compact(test.Y) != "nil" || !slotName(test.X) ||
			statement.Else != nil || len(statement.Body.List) != 1 || !jump(statement.Body.List[0]) {
			t.Fatal("promoted producer conditional changed")
		}
		return test.X.(*ast.Ident)
	}
	method := func(expression ast.Expr) string {
		call, ok := expression.(*ast.CallExpr)
		if !ok {
			return ""
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || compact(selector.X) != "b" {
			return ""
		}
		return selector.Sel.Name
	}
	writers, reads := 0, 0
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		item, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		init, ok := item.Init.(*ast.AssignStmt)
		if !ok || len(init.Lhs) != 1 || len(init.Rhs) != 1 || !slotName(init.Lhs[0]) {
			return true
		}
		kind := method(init.Rhs[0])
		if init.Tok != token.ASSIGN || (kind != "write8" && kind != "write16" && kind != "write32") {
			t.Fatal("promoted writer initializer changed")
		}
		test := condition(item)
		init.Tok = token.DEFINE
		init.Lhs[0].(*ast.Ident).Name = "err"
		test.Name = "err"
		item.Body.List = []ast.Stmt{oldReturn(ast.NewIdent("err"))}
		writers++
		return true
	})
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		block, ok := node.(*ast.BlockStmt)
		if !ok || len(block.List) != 2 {
			return true
		}
		tuple, ok := block.List[0].(*ast.AssignStmt)
		if !ok || len(tuple.Lhs) != 2 || len(tuple.Rhs) != 1 || !slotName(tuple.Lhs[1]) {
			return true
		}
		kind := method(tuple.Rhs[0])
		if tuple.Tok != token.ASSIGN || compact(tuple.Lhs[0]) != "value" ||
			(kind != "read8" && kind != "read16" && kind != "read32") {
			t.Fatal("promoted read tuple changed")
		}
		test, ok := block.List[1].(*ast.IfStmt)
		if !ok || test.Init != nil {
			t.Fatal("promoted read conditional changed")
		}
		errorTest := condition(test)
		tuple.Lhs[1].(*ast.Ident).Name = "err"
		errorTest.Name = "err"
		test.Body.List = []ast.Stmt{oldReturn(ast.NewIdent("err"))}
		declaration := &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{
			&ast.ValueSpec{Names: []*ast.Ident{ast.NewIdent("err")}, Type: ast.NewIdent("error")}}}}
		block.List = append([]ast.Stmt{declaration}, block.List...)
		reads++
		return true
	})
	var lists []*[]ast.Stmt
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.BlockStmt:
			lists = append(lists, &item.List)
		case *ast.CaseClause:
			lists = append(lists, &item.Body)
		}
		return true
	})
	fetchCopies, unsupportedCopies := 0, 0
	for _, list := range lists {
		var restored []ast.Stmt
		for index := 0; index < len(*list); index++ {
			statement := (*list)[index]
			assignment, ok := statement.(*ast.AssignStmt)
			if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 || !slotName(assignment.Lhs[0]) {
				restored = append(restored, statement)
				continue
			}
			if assignment.Tok != token.ASSIGN || index+1 >= len(*list) || !jump((*list)[index+1]) {
				t.Fatal("copied error must immediately jump")
			}
			if compact(assignment.Rhs[0]) == "err" {
				fetchCopies++
			} else if compact(assignment.Rhs[0]) == "b.unsupportedThumb(pc,instruction)" {
				unsupportedCopies++
			} else {
				t.Fatal("unexpected copied error expression")
			}
			restored = append(restored, oldReturn(assignment.Rhs[0]))
			index++
		}
		*list = restored
	}
	if writers != 37 || reads != 54 || fetchCopies != 1 || unsupportedCopies != 4 {
		t.Fatalf("inverse counts writer=%d read=%d fetch=%d unsupported=%d",
			writers, reads, fetchCopies, unsupportedCopies)
	}
	fn.Body.List = append(fn.Body.List[:1], fn.Body.List[2:]...)
	loop.Body.List = loop.Body.List[:6]
	ast.Inspect(fn, func(node ast.Node) bool {
		if name, ok := node.(*ast.Ident); ok && strings.HasPrefix(name.Name, "cachedStore41Fault") {
			t.Fatal("inverse left a fault slot or label reference")
		}
		return true
	})
	start, end := fset.PositionFor(fn.Pos(), false).Offset, fset.PositionFor(fn.End(), false).Offset
	fn.Doc = nil
	var out bytes.Buffer
	if err := format.Node(&out, fset, fn); err != nil {
		t.Fatal(err)
	}
	result := append([]byte{}, raw[:start]...)
	result = append(result, out.Bytes()...)
	result = append(result, raw[end:]...)
	return result
}

// The anchored wrapper keeps every original full-inverse caller fail-closed.
// It must not call cachedStore41FaultR7Source: that would recurse during fixture creation.
func cachedStore41RestoreFaultPromotionForLegacy(t *testing.T, raw []byte) []byte {
	t.Helper()
	result := cachedStore41RestoreFaultPromotionCore(t, raw)
	cachedStore41RequireWholeFilePin(t, result, cachedStore41LegacyR7TokenSHA256)
	return result
}

func TestApplicationCachedStore41FaultPromotionExactBindingsAndInverse(t *testing.T) {
	before := cachedStore41FaultR7Source(t)
	saved := append([]byte{}, before...)
	after, err := promoteApplicationFaultErrors41(before)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, saved) {
		t.Fatal("producer mutated its input bytes")
	}
	fset, _, fn, _ := cachedStore41FaultTree(t, after)
	text := cachedStore41FaultText(t, fset, fn)
	if strings.Count(text, "gotocachedStore41Fault") != 96 ||
		strings.Count(text, "varcachedStore41FaultErrorerror") != 1 ||
		strings.Count(text, "varerrerror") != 0 ||
		strings.Count(text, "cachedStore41FaultError=b.unsupportedThumb(pc,instruction)") != 4 ||
		strings.Count(text, "cachedStore41FaultError=err") != 1 {
		t.Fatal("error-slot/fault counts changed")
	}
	cachedStore41RestoreFaultPromotionForLegacy(t, after)
	const tail = "func (b *Backend) executeThumbCompareBranchTail"
	oldIndex, newIndex := bytes.Index(before, []byte(tail)), bytes.Index(after, []byte(tail))
	if oldIndex < 0 || newIndex < 0 || !bytes.Equal(before[oldIndex:], after[newIndex:]) {
		t.Fatal("original225 tail bytes changed")
	}
	if _, err := promoteApplicationFaultErrors41(after); err == nil {
		t.Fatal("already-promoted source accepted")
	}
}

func TestApplicationCachedStore41FaultPromotionSTMAndBLScopes(t *testing.T) {
	after, err := promoteApplicationFaultErrors41(cachedStore41FaultR7Source(t))
	if err != nil {
		t.Fatal(err)
	}
	fset, _, _, loop := cachedStore41FaultTree(t, after)
	compact := func(node ast.Node) string { return cachedStore41FaultText(t, fset, node) }
	stm, bl := 0, 0
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.IfStmt:
			child, ok := item.Else.(*ast.IfStmt)
			if !ok || compact(item.Cond) != "load" || child.Init == nil {
				break
			}
			if compact(child.Init) != "cachedStore41FaultError=b.write32(address,b.regs[register],cpu.PermissionWrite)" ||
				compact(child.Cond) != "cachedStore41FaultError!=nil" ||
				compact(child.Body) != "{gotocachedStore41Fault}" {
				t.Fatal("STM else-if producer moved or changed")
			}
			stm++
		case *ast.AssignStmt:
			if compact(item) == "suffix,err:=b.fetch16(pc+2)" {
				definition := item.Lhs[1].(*ast.Ident)
				if definition.Obj == nil {
					t.Fatal("BL local error binding lost")
				}
				bl++
			}
		}
		return true
	})
	if stm != 1 || bl != 1 {
		t.Fatalf("scoped STM=%d BL=%d", stm, bl)
	}
	cachedStore41RestoreFaultPromotionForLegacy(t, after)
}

func cachedStore41FaultReplace(t *testing.T, raw []byte, from, to string) []byte {
	t.Helper()
	if !strings.Contains(string(raw), from) {
		t.Fatalf("mutation anchor absent: %q", from)
	}
	return []byte(strings.Replace(string(raw), from, to, 1))
}

func cachedStore41FaultReject(t *testing.T, source []byte) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), "negative-fault41.go", source, 0); err != nil {
		t.Fatalf("negative fixture is not parsable: %v", err)
	}
	saved := append([]byte{}, source...)
	out, err := promoteApplicationFaultErrors41(source)
	if err == nil || !strings.HasPrefix(err.Error(), "cached-store fault contract:") || out != nil {
		t.Fatalf("expected contract rejection, output=%d error=%v", len(out), err)
	}
	if !bytes.Equal(source, saved) {
		t.Fatal("rejection changed input bytes")
	}
}

func TestApplicationCachedStore41FaultPromotionRejectsConsumers(t *testing.T) {
	t.Run("read/operand-consumer", func(t *testing.T) {
		source := cachedStore41FaultReplace(t, cachedStore41FaultR7Source(t),
			"value, err = b.read32(address, cpu.PermissionRead)",
			"value, err = b.read32(address + uint32(len(err.Error())), cpu.PermissionRead)")
		cachedStore41FaultReject(t, source)
		if _, err := promoteApplicationFaultErrors41(source); err == nil ||
			!strings.Contains(err.Error(), "unexpected error consumer") {
			t.Fatalf("same-shape RHS consumer bypassed the Obj audit: %v", err)
		}
	})
	for _, operation := range []string{"_ = err", "err = nil", "sink(&err)",
		"_ = func() error { return err }", "defer sink(err)", "go sink(err)"} {
		for _, producer := range []string{"read", "writer"} {
			t.Run(producer+"/"+strings.ReplaceAll(operation, " ", ""), func(t *testing.T) {
				source := cachedStore41FaultR7Source(t)
				if producer == "read" {
					source = cachedStore41FaultReplace(t, source, "var err error", "var err error\n"+operation)
				} else {
					const writer = "if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil {"
					source = cachedStore41FaultReplace(t, source, writer, writer+"\n"+operation)
				}
				cachedStore41FaultReject(t, source)
			})
		}
	}
}

func TestApplicationCachedStore41FaultPromotionRejectsProducerAndExitChanges(t *testing.T) {
	cases := []struct{ name, from, to string }{
		{"read-value-err", "value, err = b.read32(", "err, err = b.read32("},
		{"read-value-blank", "value, err = b.read32(", "_, err = b.read32("},
		{"read-value-index", "value, err = b.read32(", "values[0], err = b.read32("},
		{"read-error-other", "value, err = b.read32(", "value, otherErr = b.read32("},
		{"read-error-blank", "value, err = b.read32(", "value, _ = b.read32("},
		{"read-define-shadow", "value, err = b.read32(", "value, err := b.read32("},
		{"read-call-unknown", "value, err = b.read32(", "value, err = b.unknownRead("},
		{"read-call-receiver", "value, err = b.read32(", "value, err = callback().read32("},
		{"read-permission", "value, err = b.read32(address, cpu.PermissionRead)", "value, err = b.read32(address, cpu.PermissionWrite)"},
		{"read-type", "var err error", "var err otherError"},
		{"read-extra-value", "var err error", "var err error = nil"},
		{"writer-call-unknown", "if err := b.write32(address, b.regs[rd], cpu.PermissionWrite);", "if err := b.unknownWrite(address, b.regs[rd], cpu.PermissionWrite);"},
		{"writer-condition", "if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil", "if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err == nil"},
		{"writer-permission", "if err := b.write32(address, b.regs[rd], cpu.PermissionWrite);", "if err := b.write32(address, b.regs[rd], cpu.PermissionRead);"},
		{"fault-retirement", "return executed + uint64(index), nil, err", "return executed + uint64(index+1), nil, err"},
		{"fault-stop", "return executed + uint64(index), nil, err", "return executed + uint64(index), stop, err"},
		{"fault-wrap", "return executed + uint64(index), nil, err", "return executed + uint64(index), nil, wrap(err)"},
		{"fault-other-binding", "return executed + uint64(index), nil, err", "return executed + uint64(index), nil, otherErr"},
		{"BL-fault-retirement", "suffix, err := b.fetch16(pc + 2)\n\t\t\t\tif err != nil {\n\t\t\t\t\treturn executed + uint64(index), nil, err",
			"suffix, err := b.fetch16(pc + 2)\n\t\t\t\tif err != nil {\n\t\t\t\t\treturn executed + uint64(index+1), nil, err"},
		{"unsupported-retirement", "return executed + uint64(index), nil, b.unsupportedThumb(pc, instruction)", "return executed + uint64(index+1), nil, b.unsupportedThumb(pc, instruction)"},
		{"unsupported-operands", "return executed + uint64(index), nil, b.unsupportedThumb(pc, instruction)", "return executed + uint64(index), nil, b.unsupportedThumb(pc+2, instruction)"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			cachedStore41FaultReject(t, cachedStore41FaultReplace(t, cachedStore41FaultR7Source(t), item.from, item.to))
		})
	}
}

func TestApplicationCachedStore41FaultPromotionRejectsScopeAndControlChanges(t *testing.T) {
	const ret = "return executed + uint64(index), nil, err"
	const unsupported = "return executed + uint64(index), nil, b.unsupportedThumb(pc, instruction)"
	cases := []struct{ name, from, to string }{
		{"read-ancestor-index-shadow", "case thumbLiteralLoad:", "case thumbLiteralLoad:\nindex := 7"},
		{"read-ancestor-executed-shadow", "case thumbLiteralLoad:", "case thumbLiteralLoad:\nexecuted := uint64(7)"},
		{"read-receiver-shadow", "case thumbLiteralLoad:", "case thumbLiteralLoad:\nb := shadowWriter{Backend: b}"},
		{"writer-receiver-shadow", "if err := b.write32(address, b.regs[rd], cpu.PermissionWrite);", "b := shadowWriter{Backend: b}\nif err := b.write32(address, b.regs[rd], cpu.PermissionWrite);"},
		{"read-builtin-error-shadow", "case thumbLiteralLoad:", "case thumbLiteralLoad:\ntype error interface { Error() string }"},
		{"fault-nil-shadow", "case thumbLiteralLoad:", "case thumbLiteralLoad:\nnil := otherNil"},
		{"writer-index-shadow", "if err := b.write32(address, b.regs[rd], cpu.PermissionWrite);", "index := 7\nif err := b.write32(address, b.regs[rd], cpu.PermissionWrite);"},
		{"writer-executed-shadow", "if err := b.write32(address, b.regs[rd], cpu.PermissionWrite);", "executed := uint64(7)\nif err := b.write32(address, b.regs[rd], cpu.PermissionWrite);"},
		{"fault-uint64-shadow", ret, "uint64 := conversion\n" + ret},
		{"unsupported-index-shadow", unsupported, "index := 7\n" + unsupported},
		{"unsupported-executed-shadow", unsupported, "executed := uint64(7)\n" + unsupported},
		{"BL-error-shadow", "if err != nil {\n\t\t\t\t\treturn executed + uint64(index), nil, err", "if err != nil {\n\t\t\t\t\terr := otherErr\n\t\t\t\t\treturn executed + uint64(index), nil, err"},
		{"outside-loop-slot", "var executed uint64", "var executed uint64\nvar cachedStore41FaultError error"},
		{"before-dispatch-label", "instruction := in.raw", "instruction := in.raw\ncachedStore41Fault: ;"},
		{"after-dispatch-slot", "if block.compareBranchTail != nil {\n\t\t\tb.executeThumbCompareBranchTail(block)", "var cachedStore41FaultOther error\nif block.compareBranchTail != nil {\n\t\t\tb.executeThumbCompareBranchTail(block)"},
		{"retirement-write", "instruction := in.raw", "instruction := in.raw\nindex++"},
		{"retirement-address", "instruction := in.raw", "instruction := in.raw\nsink(&index)"},
		{"extra-prelude", "instruction := in.raw", "instruction := in.raw\ncallback()"},
		{"named-results", "(uint64, *cpu.StopReason, error) {", "(n uint64, reason *cpu.StopReason, failure error) {"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			cachedStore41FaultReject(t, cachedStore41FaultReplace(t, cachedStore41FaultR7Source(t), item.from, item.to))
		})
	}
}

func TestApplicationCachedStore41FaultPromotionGeneratedAndOldTail(t *testing.T) {
	file := filepath.Join(t.TempDir(), "private_generated.go")
	if err := generate("../../jit_thumb_micro.go", file, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	cachedStore41RestoreFaultPromotionForLegacy(t, raw)
	if err := generate("../../jit_thumb_micro.go", file, true); err != nil {
		t.Fatal(err)
	}
}
