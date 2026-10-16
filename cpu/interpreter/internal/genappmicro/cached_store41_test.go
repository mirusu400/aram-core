package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Historical whole-file anchors contain only hashes of public host Go source.
// These literal pins were calibrated once from the frozen R5/R6/R7 artifacts.
const (
	cachedStore41LegacyR5TokenSHA256 = "ec2f342a49a6779893c8dbfcb207fbe228986e9f0f4d2b43e3435b035f40570f"
	cachedStore41LegacyR6TokenSHA256 = "d462977aefc61b5b814f0a3664b71354021d7f8fd3f5445d558af71041a5cdb3"
	cachedStore41LegacyR7TokenSHA256 = "d4882b20fdb4aa3f823016bb3b51b64998167a8ad07787ece5c85cf449e3c411"
)

// Only valid token positions are normalized; NoPos presence flags stay zero.
// Whole-file framed tokens retain every semicolon and literal byte (including
// spaces inside strings). This is test-local and uses no producer matcher.
func cachedStore41WholeFileFingerprint(t *testing.T, raw []byte) string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "historical.go", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	positionType := reflect.TypeFor[token.Pos]()
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			return false
		}
		fields := reflect.ValueOf(node).Elem()
		for i := 0; i < fields.NumField(); i++ {
			field := fields.Field(i)
			if field.Type() == positionType && field.Int() != 0 {
				field.SetInt(1)
			}
		}
		return true
	})
	printSet := token.NewFileSet()
	printSet.AddFile("uniform.go", -1, len(raw)+4096)
	var canonical bytes.Buffer
	if err := format.Node(&canonical, printSet, file); err != nil {
		t.Fatal(err)
	}
	var lexer scanner.Scanner
	var lexErr error
	lexSet := token.NewFileSet()
	lexFile := lexSet.AddFile("canonical.go", -1, canonical.Len())
	lexer.Init(lexFile, canonical.Bytes(), func(_ token.Position, msg string) {
		lexErr = fmt.Errorf("scan canonical AST: %s", msg)
	}, 0)
	hash := sha256.New()
	for {
		_, kind, literal := lexer.Scan()
		if kind == token.EOF {
			break
		}
		fmt.Fprintf(hash, "%d:%d:", kind, len(literal))
		hash.Write([]byte(literal))
	}
	if lexErr != nil {
		t.Fatal(lexErr)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func cachedStore41RequireWholeFilePin(t *testing.T, raw []byte, want string) {
	t.Helper()
	if got := cachedStore41WholeFileFingerprint(t, raw); got != want {
		t.Fatalf("whole-file historical AST fingerprint changed: got %s want %s", got, want)
	}
}

func cachedStore41RequireWholeNodePin(t *testing.T, fset *token.FileSet, file *ast.File, want string) {
	t.Helper()
	var raw bytes.Buffer
	if err := format.Node(&raw, fset, file); err != nil {
		t.Fatal(err)
	}
	cachedStore41RequireWholeFilePin(t, raw.Bytes(), want)
}

func cachedStore41GuardedSwitch(t *testing.T, mutation func(string) string) (*token.FileSet, *ast.SwitchStmt) {
	t.Helper()
	raw, err := os.ReadFile("../../jit_thumb_micro.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	if mutation != nil {
		source = mutation(source)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "guarded.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var dispatch *ast.SwitchStmt
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "executeThumbMicroBlock" {
			continue
		}
		for _, statement := range fn.Body.List {
			loop, ok := statement.(*ast.RangeStmt)
			if !ok {
				continue
			}
			for _, child := range loop.Body.List {
				candidate, ok := child.(*ast.SwitchStmt)
				if !ok {
					continue
				}
				name, ok := candidate.Tag.(*ast.SelectorExpr)
				if ok && name.Sel.Name == "op" {
					if dispatch != nil {
						t.Fatal("multiple source opcode switches")
					}
					dispatch = candidate
				}
			}
		}
	}
	if dispatch == nil {
		t.Fatal("source opcode switch missing")
	}
	if err := specializeApplicationFlags(fset, dispatch); err != nil {
		t.Fatal(err)
	}
	if err := specializeApplicationTransfers(fset, dispatch); err != nil {
		t.Fatal(err)
	}
	count, err := hoistApplicationPrivateRoute(dispatch)
	if err != nil || requireApplicationPrivateRouteCount(count) != nil {
		t.Fatalf("original route contract changed: count=%d error=%v", count, err)
	}
	return fset, dispatch
}

func cachedStore41NodeText(t *testing.T, fset *token.FileSet, node ast.Node) string {
	t.Helper()
	var out bytes.Buffer
	if err := format.Node(&out, fset, node); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// Check the complete label/goto wrapper independently of the producer.
// Return the untouched canonical fault and the literal pre-R7 structured wrapper.
func cachedStore41GotoParts(t *testing.T, fset *token.FileSet, block *ast.BlockStmt) (*ast.IfStmt, *ast.BlockStmt, string) {
	t.Helper()
	if len(block.List) != 3 {
		t.Fatal("goto wrapper statement count changed")
	}
	route, ok := block.List[0].(*ast.IfStmt)
	if !ok || route.Init != nil || route.Else != nil || len(route.Body.List) != 1 {
		t.Fatal("goto private route shape changed")
	}
	routeCond, err := storeText41(fset, route.Cond)
	if err != nil || routeCond != "foldPrivateScalarBounds&&privateDataRoute" {
		t.Fatal("goto private route condition changed")
	}
	hit, ok := route.Body.List[0].(*ast.IfStmt)
	if !ok || hit.Else != nil || len(hit.Body.List) != 2 {
		t.Fatal("goto if-init/hit body changed")
	}
	fault, ok := block.List[1].(*ast.IfStmt)
	if !ok || fault.Else != nil {
		t.Fatal("canonical fault if is not the middle statement")
	}
	faultInit, ok := fault.Init.(*ast.AssignStmt)
	if !ok || len(faultInit.Lhs) != 1 || len(faultInit.Rhs) != 1 {
		t.Fatal("canonical writer init changed")
	}
	call, ok := faultInit.Rhs[0].(*ast.CallExpr)
	if !ok {
		t.Fatal("canonical writer call missing")
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		t.Fatal("canonical writer selector missing")
	}
	width, helper, value := "4", "cachedStore32", "b.regs[rd]"
	switch selector.Sel.Name {
	case "write8":
		width, helper, value = "1", "cachedStore8", "byte(b.regs[rd])"
	case "write16":
		width, helper, value = "2", "cachedStore16", "uint16(b.regs[rd])"
	case "write32":
	default:
		t.Fatal("unknown canonical writer")
	}
	writerText, writerErr := storeText41(fset, call)
	if writerErr != nil || writerText != "b."+selector.Sel.Name+"(address,"+value+",cpu.PermissionWrite)" {
		t.Fatal("canonical writer operands changed")
	}
	init, ok := hit.Init.(*ast.AssignStmt)
	if !ok || init.Tok != token.DEFINE || len(init.Lhs) != 2 || len(init.Rhs) != 1 {
		t.Fatal("cached tuple must be declared once in the hit if-init")
	}
	initText, initErr := storeText41(fset, init)
	hitCond, condErr := storeText41(fset, hit.Cond)
	smcText, smcErr := storeText41(fset, hit.Body.List[0])
	wantInit := "cachedStore41Perms,cachedStore41Hit:=" + helper + "(&b.privateDataCache[privateDataCacheIndex(address,cpu.PermissionWrite)],address," + value + ")"
	if initErr != nil || condErr != nil || smcErr != nil || initText != wantInit ||
		hitCond != "cachedStore41Hit" || smcText != "b.cachedStore41SMC(address,"+width+",cachedStore41Perms)" {
		t.Fatal("if-init/hit/SMC operands or ordering changed")
	}
	jump, ok := hit.Body.List[1].(*ast.BranchStmt)
	if !ok || jump.Tok != token.GOTO || jump.Label == nil {
		t.Fatal("hit must end with one goto")
	}
	label, ok := block.List[2].(*ast.LabeledStmt)
	if !ok || label.Label == nil {
		t.Fatal("goto target is not the wrapper's final statement")
	}
	if _, ok := label.Stmt.(*ast.EmptyStmt); !ok || jump.Label.Name != label.Label.Name {
		t.Fatal("goto must target the same wrapper's terminal empty label")
	}
	switch label.Label.Name {
	case "cachedStore41Done0", "cachedStore41Done1", "cachedStore41Done2", "cachedStore41Done3",
		"cachedStore41Done4", "cachedStore41Done5", "cachedStore41Done6":
	default:
		t.Fatal("goto label outside the seven reserved names")
	}
	literal := "package interpreter\nfunc old() {{cachedStore41Perms,cachedStore41Hit:=cpu.Permissions(0),false\n" +
		"if foldPrivateScalarBounds&&privateDataRoute{cachedStore41Perms,cachedStore41Hit=" + helper +
		"(&b.privateDataCache[privateDataCacheIndex(address,cpu.PermissionWrite)],address," + value + ")}\n" +
		"if cachedStore41Hit {b.cachedStore41SMC(address," + width + ",cachedStore41Perms)}\n}}"
	legacy, err := parser.ParseFile(fset, "literal-r6-wrapper41.go", literal, 0)
	if err != nil {
		t.Fatal(err)
	}
	legacyBlock := legacy.Decls[0].(*ast.FuncDecl).Body.List[0].(*ast.BlockStmt)
	legacyBlock.List[2].(*ast.IfStmt).Else = fault
	return fault, legacyBlock, label.Label.Name
}

func TestApplicationCachedStore41ExactSevenAndInverse(t *testing.T) {
	fset, dispatch := cachedStore41GuardedSwitch(t, nil)
	before := cachedStore41NodeText(t, fset, dispatch)
	count, err := cacheApplicationScalarStores41(fset, dispatch)
	if err != nil || count != 7 {
		t.Fatalf("count=%d error=%v", count, err)
	}
	text := cachedStore41NodeText(t, fset, dispatch)
	if strings.Count(text, "if privateDataRoute {") != 54 ||
		strings.Count(text, "if foldPrivateScalarBounds && privateDataRoute {") != 7 ||
		strings.Count(text, "cachedStore32(") != 3 || strings.Count(text, "cachedStore16(") != 2 ||
		strings.Count(text, "cachedStore8(") != 2 || strings.Count(text, "b.cachedStore41SMC(") != 7 ||
		strings.Count(text, "goto cachedStore41Done") != 7 ||
		strings.Count(text, "if b.jitBlocks != nil || b.nativeBlocks != nil {") != 0 {
		t.Fatal("route/helper/goto/SMC source contract changed")
	}
	var lists []*[]ast.Stmt
	ast.Inspect(dispatch, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.BlockStmt:
			lists = append(lists, &item.List)
		case *ast.CaseClause:
			lists = append(lists, &item.Body)
		}
		return true
	})
	restored := 0
	labels := make(map[string]bool)
	for _, list := range lists {
		for index, child := range *list {
			block, ok := child.(*ast.BlockStmt)
			if !ok || len(block.List) != 3 {
				continue
			}
			route, ok := block.List[0].(*ast.IfStmt)
			if !ok {
				continue
			}
			condition, err := storeText41(fset, route.Cond)
			if err != nil || condition != "foldPrivateScalarBounds&&privateDataRoute" {
				continue
			}
			fault, _, label := cachedStore41GotoParts(t, fset, block)
			if labels[label] {
				t.Fatal("duplicate goto label")
			}
			labels[label] = true
			(*list)[index] = fault
			restored++
		}
	}
	if restored != 7 || len(labels) != 7 || cachedStore41NodeText(t, fset, dispatch) != before {
		t.Fatal("inverse does not recover every original writer/fault/jitGen opcode token")
	}
}

func TestApplicationCachedStore41RejectsChangedSourceWithoutMutation(t *testing.T) {
	for name, mutation := range map[string]func(string) string{
		"wrong-value": func(s string) string {
			return strings.ReplaceAll(s, "b.write32(address, b.regs[rd]", "b.write32(address, b.regs[rd]+1")
		},
		"effectful-value": func(s string) string {
			return strings.ReplaceAll(s, "b.write32(address, b.regs[rd]", "b.write32(address, callback()")
		},
		"wrong-address": func(s string) string { return strings.ReplaceAll(s, "b.write16(address,", "b.write16(address+1,") },
		"wrong-permission": func(s string) string {
			return strings.ReplaceAll(s, "b.write8(address, byte(b.regs[rd]), cpu.PermissionWrite)", "b.write8(address, byte(b.regs[rd]), cpu.PermissionRead)")
		},
		"wrong-fault-retire": func(s string) string {
			return strings.ReplaceAll(s, "return index, false, nil, err", "return index+1, false, nil, err")
		},
		"missing-generation": func(s string) string {
			return strings.ReplaceAll(s, "if b.jitGen != blockGeneration", "if b.jitGen == blockGeneration")
		},
		"wrong-generation-retire": func(s string) string {
			return strings.ReplaceAll(s, "return index + 1, false, nil, nil", "return index, false, nil, nil")
		},
		"reserved-local": func(s string) string {
			return strings.Replace(s, "case thumbHalfwordTransfer:", "case thumbHalfwordTransfer:\ncachedStore41Hit := false; _ = cachedStore41Hit", 1)
		},
		"extra-prelude-callback": func(s string) string {
			return strings.Replace(s, "address := b.regs[rb] + b.regs[ro]", "address := b.regs[rb] + b.regs[ro]; callback()", 1)
		},
		"changed-base-alias": func(s string) string {
			return strings.Replace(s, "address := b.regs[rb] + b.regs[ro]", "address := b.regs[rb] + b.regs[rd]", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fset, dispatch := cachedStore41GuardedSwitch(t, mutation)
			before := cachedStore41NodeText(t, fset, dispatch)
			if _, err := cacheApplicationScalarStores41(fset, dispatch); err == nil {
				t.Fatal("changed guarded store accepted")
			}
			if cachedStore41NodeText(t, fset, dispatch) != before {
				t.Fatal("rejection partly mutated the opcode switch")
			}
		})
	}
	for _, count := range []int{0, 1, 6, 8, 37} {
		if requireApplicationCachedScalarStores41(count) == nil {
			t.Fatalf("accepted count %d", count)
		}
	}
}

func TestApplicationCachedStore41MalformedSpecializationDeclines(t *testing.T) {
	for _, mode := range []string{"missing", "duplicate", "grouped", "missing-prelude", "wrong-constant", "extra-store-statement"} {
		t.Run(mode, func(t *testing.T) {
			fset, dispatch := cachedStore41GuardedSwitch(t, nil)
			var target *ast.CaseClause
			var targetIndex int
			for index, statement := range dispatch.Body.List {
				clause := statement.(*ast.CaseClause)
				if len(clause.List) == 1 {
					label, err := storeText41(fset, clause.List[0])
					if err != nil {
						t.Fatal(err)
					}
					if label == "thumbAppRegisterTransferBase+0" {
						target, targetIndex = clause, index
					}
				}
			}
			if target == nil {
				t.Fatal("positive subtype missing before mutation")
			}
			switch mode {
			case "missing":
				dispatch.Body.List = append(dispatch.Body.List[:targetIndex], dispatch.Body.List[targetIndex+1:]...)
			case "duplicate":
				dispatch.Body.List = append(dispatch.Body.List, target)
			case "grouped":
				target.List = append(target.List, ast.NewIdent("futureSubtype"))
			case "missing-prelude":
				target.Body = target.Body[len(target.Body)-1:]
			case "wrong-constant":
				target.Body[0].(*ast.DeclStmt).Decl.(*ast.GenDecl).Specs[0].(*ast.ValueSpec).Values[0] = &ast.BasicLit{Kind: token.INT, Value: "7"}
			case "extra-store-statement":
				inner := target.Body[len(target.Body)-1].(*ast.SwitchStmt).Body.List[0].(*ast.CaseClause)
				inner.Body = append(inner.Body, &ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("callback")}})
			}
			before := cachedStore41NodeText(t, fset, dispatch)
			if _, err := cacheApplicationScalarStores41(fset, dispatch); err == nil || cachedStore41NodeText(t, fset, dispatch) != before {
				t.Fatal("malformed specialization accepted or partly mutated")
			}
		})
	}
}

func TestApplicationCachedStore41RejectsOuterNamespaceCollision(t *testing.T) {
	for _, placement := range []string{"before-dispatch", "after-dispatch"} {
		t.Run(placement, func(t *testing.T) {
			raw, err := os.ReadFile("../../jit_thumb_micro.go")
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "outside-namespace41.go", raw, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			var loop *ast.RangeStmt
			for _, declaration := range file.Decls {
				fn, ok := declaration.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "executeThumbMicroBlock" {
					continue
				}
				for _, statement := range fn.Body.List {
					if candidate, ok := statement.(*ast.RangeStmt); ok {
						if loop != nil {
							t.Fatal("multiple source instruction loops")
						}
						loop = candidate
					}
				}
			}
			if loop == nil {
				t.Fatal("source instruction loop missing")
			}
			pcIndex, dispatchIndex := -1, -1
			for index, statement := range loop.Body.List {
				text, err := storeText41(fset, statement)
				if err != nil {
					t.Fatal(err)
				}
				if text == "b.regs[cpu.RegisterPC]=pc+2" {
					pcIndex = index
				}
				if dispatch, ok := statement.(*ast.SwitchStmt); ok {
					if selector, ok := dispatch.Tag.(*ast.SelectorExpr); ok && selector.Sel.Name == "op" {
						dispatchIndex = index
					}
				}
			}
			if pcIndex < 0 || dispatchIndex <= pcIndex {
				t.Fatal("emitted source boundaries changed")
			}
			at := dispatchIndex
			if placement == "after-dispatch" {
				at++
			}
			label := &ast.LabeledStmt{Label: ast.NewIdent("cachedStore41Done0"), Stmt: &ast.EmptyStmt{}}
			tail := append([]ast.Stmt(nil), loop.Body.List[at:]...)
			loop.Body.List = append(loop.Body.List[:at], label)
			loop.Body.List = append(loop.Body.List, tail...)
			var source bytes.Buffer
			if err := format.Node(&source, fset, file); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			input, output := filepath.Join(dir, "guarded.go"), filepath.Join(dir, "generated.go")
			if err := os.WriteFile(input, source.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			err = generate(input, output, false)
			if err == nil || err.Error() != "application cached-store namespace already exists: cachedStore41Done0" {
				t.Fatalf("outside-label guard did not reject for its exact reason: %v", err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("namespace rejection materialized output or inspection failed: %v", err)
			}
		})
	}
}

func cachedStore41ExpandGotoWrappers(t *testing.T, fset *token.FileSet, file *ast.File) int {
	t.Helper()
	var lists []*[]ast.Stmt
	ast.Inspect(file, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.BlockStmt:
			lists = append(lists, &item.List)
		case *ast.CaseClause:
			lists = append(lists, &item.Body)
		}
		return true
	})
	restored := 0
	labels := make(map[string]bool)
	for _, list := range lists {
		for index, statement := range *list {
			block, ok := statement.(*ast.BlockStmt)
			if !ok || len(block.List) != 3 {
				continue
			}
			route, ok := block.List[0].(*ast.IfStmt)
			if !ok {
				continue
			}
			condition, err := storeText41(fset, route.Cond)
			if err != nil || condition != "foldPrivateScalarBounds&&privateDataRoute" {
				continue
			}
			_, legacy, label := cachedStore41GotoParts(t, fset, block)
			if labels[label] {
				t.Fatal("duplicate generated goto label")
			}
			labels[label] = true
			(*list)[index] = legacy
			restored++
		}
	}
	if len(labels) != 7 {
		t.Fatal("generated goto target set differs from the seven local labels")
	}
	return restored
}

// Expand only the new call to the independent literal old guard/call AST.
// The entire expanded generated file must equal the frozen R5 generated AST.
func cachedStore41ExpandSMCOutline(t *testing.T, fset *token.FileSet, file *ast.File) int {
	t.Helper()
	var lists []*[]ast.Stmt
	ast.Inspect(file, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.BlockStmt:
			lists = append(lists, &item.List)
		case *ast.CaseClause:
			lists = append(lists, &item.Body)
		}
		return true
	})
	restored := 0
	for _, list := range lists {
		for index, statement := range *list {
			expr, ok := statement.(*ast.ExprStmt)
			if !ok {
				continue
			}
			call, ok := expr.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "cachedStore41SMC" {
				continue
			}
			if len(call.Args) != 3 || call.Ellipsis.IsValid() {
				t.Fatal("outlined SMC arity changed")
			}
			width, ok := call.Args[1].(*ast.BasicLit)
			if !ok || width.Kind != token.INT || (width.Value != "1" && width.Value != "2" && width.Value != "4") {
				t.Fatal("outlined SMC width changed")
			}
			got, err := storeText41(fset, call)
			if err != nil || got != "b.cachedStore41SMC(address,"+width.Value+",cachedStore41Perms)" {
				t.Fatal("outlined SMC receiver/address/full-permissions changed")
			}
			legacy, err := parser.ParseFile(fset, "literal-old-smc41.go",
				"package interpreter\nfunc old() { if b.jitBlocks != nil || b.nativeBlocks != nil { b.smcInvalidate(address,"+width.Value+",cachedStore41Perms) } }", 0)
			if err != nil {
				t.Fatal(err)
			}
			(*list)[index] = legacy.Decls[0].(*ast.FuncDecl).Body.List[0]
			restored++
		}
	}
	return restored
}

func TestApplicationCachedStore41GeneratedContractAndOldTail(t *testing.T) {
	output := filepath.Join(t.TempDir(), "private_generated.go")
	if err := generate("../../jit_thumb_micro.go", output, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	raw = cachedStore41RestoreFaultPromotionForLegacy(t, raw)
	text := string(raw)
	if strings.Count(text, "if privateDataRoute {") != 54 ||
		strings.Count(text, "if foldPrivateScalarBounds && privateDataRoute {") != 7 ||
		strings.Count(text, "b.write32(")+strings.Count(text, "b.write16(")+strings.Count(text, "b.write8(") != 37 ||
		strings.Count(text, "b.cachedStore41SMC(") != 7 ||
		strings.Count(text, "goto cachedStore41Done") != 7 ||
		strings.Count(text, "if b.jitBlocks != nil || b.nativeBlocks != nil {") != 0 {
		t.Fatal("generated route/fallback source contract changed")
	}
	fset := token.NewFileSet()
	candidateAST, err := parser.ParseFile(fset, "candidate41.go", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cachedStore41ExpandGotoWrappers(t, fset, candidateAST) != 7 {
		t.Fatal("goto expansion did not restore exactly seven R6 wrappers")
	}
	cachedStore41RequireWholeNodePin(t, fset, candidateAST, cachedStore41LegacyR6TokenSHA256)
	if cachedStore41ExpandSMCOutline(t, fset, candidateAST) != 7 {
		t.Fatal("SMC outline did not expand exactly seven calls")
	}
	cachedStore41RequireWholeNodePin(t, fset, candidateAST, cachedStore41LegacyR5TokenSHA256)
	original, err := os.ReadFile("../../jit_thumb_application_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	const boundary = "func (b *Backend) executeThumbCompareBranchTail"
	oldIndex, newIndex := strings.Index(string(original), boundary), strings.Index(text, boundary)
	if oldIndex < 0 || newIndex < 0 || string(original[oldIndex:]) != text[newIndex:] {
		t.Fatal("old compare/branch helper source changed")
	}
}

func TestApplicationCachedStore41WholeFileFingerprintIgnoresCommentsAndSpacing(t *testing.T) {
	before := []byte("package example\nfunc f() string { return \"a b\" }\n")
	after := []byte("// comment before package\npackage example\n\nfunc f( ) string{\n// comment before return\nreturn \"a b\"\n}\n")
	if cachedStore41WholeFileFingerprint(t, before) != cachedStore41WholeFileFingerprint(t, after) {
		t.Fatal("comments or nonliteral spacing changed the token fingerprint")
	}
}

func TestApplicationCachedStore41WholeFileFingerprintDetectsChanges(t *testing.T) {
	cases := []struct {
		name, before, after string
	}{
		{"string-internal-space", "package example\nfunc f() string { return \"a b\" }\n", "package example\nfunc f() string { return \"ab\" }\n"},
		{"string-same-length", "package example\nfunc f() string { return \"a b\" }\n", "package example\nfunc f() string { return \"a c\" }\n"},
		{"identifier", "package example\nfunc f() string { return \"a b\" }\n", "package example\nfunc g() string { return \"a b\" }\n"},
		{"operator", "package example\nfunc f() int { return 1 + 2 }\n", "package example\nfunc f() int { return 1 - 2 }\n"},
		{"literal", "package example\nfunc f() int { return 1 + 2 }\n", "package example\nfunc f() int { return 1 + 3 }\n"},
		{"declaration", "package example\nfunc f() int { return 1 }\n", "package example\nfunc f() int { return 1 }\nfunc g() int { return 2 }\n"},
		{"for-init-vs-post", "package example\nfunc f(){for g();; {}}\n", "package example\nfunc f(){for ;;g() {}}\n"},
		{"variadic-call", "package example\nfunc f(){g(x...)}\n", "package example\nfunc f(){g(x)}\n"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if cachedStore41WholeFileFingerprint(t, []byte(item.before)) == cachedStore41WholeFileFingerprint(t, []byte(item.after)) {
				t.Fatal("changed whole-file token/literal sequence kept the same fingerprint")
			}
		})
	}
}
