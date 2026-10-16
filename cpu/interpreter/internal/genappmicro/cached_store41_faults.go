package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
)

// promoteApplicationFaultErrors41 is a post-format pass. It changes only proven
// local error bindings and their terminal exits; instruction bodies are already
// derived and checked by the unchanged R7 transforms.
func promoteApplicationFaultErrors41(source []byte) ([]byte, error) {
	const slot = "cachedStore41FaultError"
	const label = "cachedStore41Fault"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "application-fault41.go", source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("cached-store fault parse: %w", err)
	}
	var fn *ast.FuncDecl
	for _, declaration := range file.Decls {
		if candidate, ok := declaration.(*ast.FuncDecl); ok && candidate.Name.Name == "executeThumbApplicationMicroBlocks" {
			if fn != nil {
				return nil, fmt.Errorf("cached-store fault executor duplicated")
			}
			fn = candidate
		}
	}
	fail := func(message string) ([]byte, error) {
		return nil, fmt.Errorf("cached-store fault contract: %s", message)
	}
	if fn == nil || fn.Body == nil || fn.Recv == nil || len(fn.Recv.List) != 1 ||
		len(fn.Recv.List[0].Names) != 1 || fn.Recv.List[0].Names[0].Name != "b" ||
		fn.Type.Params == nil || len(fn.Type.Params.List) != 2 ||
		fn.Type.Results == nil || len(fn.Type.Results.List) != 3 {
		return fail("executor signature")
	}
	text := func(node ast.Node) string {
		var out bytes.Buffer
		if format.Node(&out, fset, node) != nil {
			return ""
		}
		return strings.Join(strings.Fields(out.String()), "")
	}
	if text(fn.Recv.List[0].Type) != "*Backend" ||
		fn.Type.TypeParams != nil ||
		text(fn.Type) != "func(block*jitBlock,limituint64)(uint64,*cpu.StopReason,error)" {
		return fail("executor types or named results")
	}
	receiverObject := fn.Recv.List[0].Names[0].Obj
	if receiverObject == nil {
		return fail("missing receiver lexical binding")
	}
	var forbidden string
	ast.Inspect(fn, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.Ident:
			if strings.HasPrefix(item.Name, "cachedStore41Fault") {
				forbidden = "reserved fault namespace"
			}
		case *ast.FuncLit, *ast.DeferStmt, *ast.GoStmt:
			forbidden = "closure or deferred/concurrent operation"
		}
		return forbidden == ""
	})
	if forbidden != "" {
		return fail(forbidden)
	}
	for _, group := range file.Comments {
		if group.Pos() >= fn.Pos() && group.End() <= fn.End() {
			return fail("unexpected executor-internal comment")
		}
	}
	if len(fn.Body.List) != 4 || text(fn.Body.List[0]) != "varexecuteduint64" ||
		text(fn.Body.List[1]) != "privateDataRoute:=!b.physicalAccess&&b.tlb==nil" ||
		text(fn.Body.List[3]) != "returnexecuted,nil,nil" {
		return fail("outer executor prelude or footer")
	}
	executedDecl := fn.Body.List[0].(*ast.DeclStmt).Decl.(*ast.GenDecl).Specs[0].(*ast.ValueSpec)
	executedObject := executedDecl.Names[0].Obj
	outer, ok := fn.Body.List[2].(*ast.ForStmt)
	if !ok || outer.Init != nil || outer.Post != nil || text(outer.Cond) != "executed<limit" {
		return fail("outer execution loop")
	}
	var loop *ast.RangeStmt
	for _, statement := range outer.Body.List {
		if candidate, ok := statement.(*ast.RangeStmt); ok {
			if loop != nil {
				return fail("multiple instruction ranges")
			}
			loop = candidate
		}
	}
	if loop == nil || loop.Tok != token.DEFINE || loop.Value != nil ||
		text(loop.Key) != "index" || text(loop.X) != "instructions" || len(loop.Body.List) != 6 {
		return fail("instruction range")
	}
	indexObject := loop.Key.(*ast.Ident).Obj
	if executedObject == nil || indexObject == nil {
		return fail("missing lexical retirement bindings")
	}
	wantPrelude := []string{"in:=&instructions[index]", "pc:=in.pc",
		"ifcollectThumbMicro{b.recordThumbMicro(in)}", "instruction:=in.raw",
		"b.regs[cpu.RegisterPC]=pc+2"}
	for index, want := range wantPrelude {
		if text(loop.Body.List[index]) != want {
			return fail("instruction prelude")
		}
	}
	dispatch, ok := loop.Body.List[5].(*ast.SwitchStmt)
	if !ok || dispatch.Init != nil || text(dispatch.Tag) != "in.appOp" {
		return fail("instruction dispatch")
	}
	invariant := true
	isRetirement := func(node ast.Node) bool {
		name, ok := node.(*ast.Ident)
		return ok && (name.Obj == executedObject || name.Obj == indexObject)
	}
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.AssignStmt:
			for _, lhs := range item.Lhs {
				ast.Inspect(lhs, func(child ast.Node) bool {
					if isRetirement(child) {
						invariant = false
					}
					return true
				})
			}
		case *ast.IncDecStmt:
			if isRetirement(item.X) {
				invariant = false
			}
		case *ast.UnaryExpr:
			if item.Op == token.AND && isRetirement(item.X) {
				invariant = false
			}
		}
		return invariant
	})
	if !invariant {
		return fail("retirement binding mutation or escape")
	}
	// Every edit is planned before any AST mutation. Obj identity, not spelling,
	// binds the producer, test and exit; an extra consumer rejects the whole pass.
	bindings := map[*ast.Object]map[*ast.Ident]bool{}
	promoted := map[*ast.Object]bool{}
	directExits := map[*ast.ReturnStmt]bool{}
	copyExits := map[*ast.ReturnStmt]bool{}
	removeDeclarations := map[*ast.DeclStmt]bool{}
	var writerInits []*ast.AssignStmt
	var planError string
	readCount, writerCount, fetchCount, unsupportedCount := 0, 0, 0, 0
	readCalls, writerCalls, fetchCalls := 0, 0, 0
	name := func(expression ast.Expr, expected string) (*ast.Ident, bool) {
		identifier, ok := expression.(*ast.Ident)
		return identifier, ok && identifier.Name == expected && identifier.Obj != nil
	}
	callIs := func(expression ast.Expr, method, permission string, argc int) bool {
		call, ok := expression.(*ast.CallExpr)
		if !ok || call.Ellipsis.IsValid() || len(call.Args) != argc {
			return false
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != method {
			return false
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok || receiver.Name != "b" || receiver.Obj != receiverObject {
			return false
		}
		return permission == "" || text(call.Args[len(call.Args)-1]) == "cpu."+permission
	}
	// Match the original lexical count, not only its spelling: an inner index or
	// executed shadow would otherwise change retirement at the common label.
	originalSum := func(expression ast.Expr) bool {
		sum, ok := expression.(*ast.BinaryExpr)
		if !ok || sum.Op != token.ADD {
			return false
		}
		executed, ok := sum.X.(*ast.Ident)
		if !ok || executed.Name != "executed" || executed.Obj != executedObject {
			return false
		}
		conversion, ok := sum.Y.(*ast.CallExpr)
		if !ok || len(conversion.Args) != 1 || conversion.Ellipsis.IsValid() {
			return false
		}
		kind, ok := conversion.Fun.(*ast.Ident)
		if !ok || kind.Name != "uint64" || kind.Obj != nil {
			return false
		}
		index, ok := conversion.Args[0].(*ast.Ident)
		return ok && index.Name == "index" && index.Obj == indexObject
	}
	builtinNil := func(expression ast.Expr) bool {
		identifier, ok := expression.(*ast.Ident)
		return ok && identifier.Name == "nil" && identifier.Obj == nil
	}
	retirementExit := func(statement ast.Stmt, object *ast.Object) (*ast.ReturnStmt, *ast.Ident, bool) {
		exit, ok := statement.(*ast.ReturnStmt)
		if !ok || len(exit.Results) != 3 || !originalSum(exit.Results[0]) ||
			!builtinNil(exit.Results[1]) {
			return nil, nil, false
		}
		identifier, ok := name(exit.Results[2], "err")
		if !ok || identifier.Obj != object {
			return nil, nil, false
		}
		return exit, identifier, true
	}
	errorIf := func(statement ast.Stmt, object *ast.Object) (*ast.Ident, *ast.ReturnStmt, *ast.Ident, bool) {
		conditional, ok := statement.(*ast.IfStmt)
		if !ok || conditional.Else != nil || len(conditional.Body.List) != 1 {
			return nil, nil, nil, false
		}
		condition, ok := conditional.Cond.(*ast.BinaryExpr)
		if !ok || condition.Op != token.NEQ || text(condition.Y) != "nil" {
			return nil, nil, nil, false
		}
		test, ok := name(condition.X, "err")
		if !ok || test.Obj != object {
			return nil, nil, nil, false
		}
		exit, result, ok := retirementExit(conditional.Body.List[0], object)
		return test, exit, result, ok
	}
	addBinding := func(object *ast.Object, identifiers ...*ast.Ident) {
		if object == nil || bindings[object] != nil {
			planError = "duplicate or unresolved producer binding"
			return
		}
		uses := map[*ast.Ident]bool{}
		for _, identifier := range identifiers {
			if identifier == nil || identifier.Obj != object || uses[identifier] {
				planError = "producer reference identity"
				return
			}
			uses[identifier] = true
		}
		bindings[object] = uses
	}
	var lists []*[]ast.Stmt
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.BlockStmt:
			lists = append(lists, &item.List)
		case *ast.CaseClause:
			lists = append(lists, &item.Body)
		case *ast.CallExpr:
			selector, ok := item.Fun.(*ast.SelectorExpr)
			if !ok || text(selector.X) != "b" {
				break
			}
			switch selector.Sel.Name {
			case "read8", "read16", "read32":
				readCalls++
			case "write8", "write16", "write32":
				writerCalls++
			case "fetch16":
				fetchCalls++
			}
		}
		return true
	})
	for _, list := range lists {
		for position, statement := range *list {
			switch item := statement.(type) {
			case *ast.IfStmt:
				init, ok := item.Init.(*ast.AssignStmt)
				if !ok || init.Tok != token.DEFINE || len(init.Lhs) != 1 || len(init.Rhs) != 1 {
					continue
				}
				definition, ok := name(init.Lhs[0], "err")
				if !ok {
					continue
				}
				matched := false
				for _, method := range []string{"write8", "write16", "write32"} {
					matched = matched || callIs(init.Rhs[0], method, "PermissionWrite", 3)
				}
				if !matched {
					continue
				}
				test, exit, result, ok := errorIf(item, definition.Obj)
				if !ok {
					return fail("writer error test or exit")
				}
				addBinding(definition.Obj, definition, test, result)
				promoted[definition.Obj], directExits[exit] = true, true
				writerInits = append(writerInits, init)
				writerCount++
			case *ast.DeclStmt:
				declaration, ok := item.Decl.(*ast.GenDecl)
				if !ok || declaration.Tok != token.VAR || len(declaration.Specs) != 1 {
					continue
				}
				spec, ok := declaration.Specs[0].(*ast.ValueSpec)
				if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "err" {
					continue
				}
				if position != 0 || len(*list) != 3 || len(spec.Values) != 0 ||
					text(spec.Type) != "error" || spec.Names[0].Obj == nil {
					return fail("read error declaration")
				}
				errorType, ok := spec.Type.(*ast.Ident)
				if !ok || errorType.Obj != nil {
					return fail("read error type shadows builtin error")
				}
				assignment, ok := (*list)[1].(*ast.AssignStmt)
				if !ok || assignment.Tok != token.ASSIGN || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 {
					return fail("read tuple assignment")
				}
				value, ok := assignment.Lhs[0].(*ast.Ident)
				if !ok || value.Name != "value" || value.Obj == nil {
					return fail("read value binding")
				}
				errorLHS, ok := name(assignment.Lhs[1], "err")
				if !ok || errorLHS.Obj != spec.Names[0].Obj {
					return fail("read error tuple binding")
				}
				matched := false
				for _, method := range []string{"read8", "read16", "read32"} {
					matched = matched || callIs(assignment.Rhs[0], method, "PermissionRead", 2)
				}
				conditional, ok := (*list)[2].(*ast.IfStmt)
				if !matched || !ok || conditional.Init != nil {
					return fail("read producer or conditional init")
				}
				test, exit, result, ok := errorIf(conditional, spec.Names[0].Obj)
				if !ok {
					return fail("read error test or exit")
				}
				addBinding(spec.Names[0].Obj, spec.Names[0], errorLHS, test, result)
				promoted[spec.Names[0].Obj], directExits[exit] = true, true
				removeDeclarations[item] = true
				readCount++
			case *ast.AssignStmt:
				if item.Tok != token.DEFINE || len(item.Lhs) != 2 || len(item.Rhs) != 1 ||
					!callIs(item.Rhs[0], "fetch16", "", 1) {
					continue
				}
				suffix, ok := item.Lhs[0].(*ast.Ident)
				definition, bound := name(item.Lhs[1], "err")
				call := item.Rhs[0].(*ast.CallExpr)
				if !ok || suffix.Name != "suffix" || suffix.Obj == nil || !bound ||
					text(call.Args[0]) != "pc+2" || position+1 >= len(*list) {
					return fail("BL suffix producer")
				}
				conditional, ok := (*list)[position+1].(*ast.IfStmt)
				if !ok || conditional.Init != nil {
					return fail("BL suffix test")
				}
				test, exit, result, ok := errorIf(conditional, definition.Obj)
				if !ok {
					return fail("BL suffix error exit")
				}
				addBinding(definition.Obj, definition, test, result)
				copyExits[exit] = true
				fetchCount++
			}
		}
	}
	if planError != "" {
		return fail(planError)
	}
	// Else-if writer init is not a statement-list element. Visit it in place;
	// never lift its producer out of the load/store condition.
	ast.Inspect(loop.Body, func(node ast.Node) bool {
		item, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		init, ok := item.Init.(*ast.AssignStmt)
		if !ok || init.Tok != token.DEFINE || len(init.Lhs) != 1 || len(init.Rhs) != 1 {
			return true
		}
		definition, ok := name(init.Lhs[0], "err")
		if !ok || bindings[definition.Obj] != nil {
			return true
		}
		matched := false
		for _, method := range []string{"write8", "write16", "write32"} {
			matched = matched || callIs(init.Rhs[0], method, "PermissionWrite", 3)
		}
		if !matched {
			return true
		}
		test, exit, result, ok := errorIf(item, definition.Obj)
		if !ok {
			planError = "else-if writer error test or exit"
			return false
		}
		addBinding(definition.Obj, definition, test, result)
		promoted[definition.Obj], directExits[exit] = true, true
		writerInits = append(writerInits, init)
		writerCount++
		return true
	})
	if planError != "" {
		return fail(planError)
	}
	seenReferences := 0
	expectedReferences := 0
	for _, uses := range bindings {
		expectedReferences += len(uses)
	}
	ast.Inspect(fn, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok || identifier.Name != "err" {
			return true
		}
		if identifier.Obj == nil || !bindings[identifier.Obj][identifier] {
			planError = "unexpected error consumer, escape, reassignment or shadow"
			return false
		}
		seenReferences++
		return true
	})
	if planError != "" || seenReferences != expectedReferences {
		return fail("unexpected error consumer, escape, reassignment or shadow")
	}
	for _, list := range lists {
		for _, statement := range *list {
			exit, ok := statement.(*ast.ReturnStmt)
			if !ok {
				continue
			}
			if len(exit.Results) != 3 {
				return fail("instruction return arity")
			}
			if text(exit.Results[2]) == "nil" {
				continue
			}
			if directExits[exit] || copyExits[exit] {
				continue
			}
			if !originalSum(exit.Results[0]) || !builtinNil(exit.Results[1]) ||
				!callIs(exit.Results[2], "unsupportedThumb", "", 2) {
				return fail("unrecognized error exit")
			}
			call := exit.Results[2].(*ast.CallExpr)
			if text(call.Args[0]) != "pc" || text(call.Args[1]) != "instruction" {
				return fail("unsupported error operands")
			}
			copyExits[exit] = true
			unsupportedCount++
		}
	}
	if readCount != 54 || writerCount != 37 || fetchCount != 1 || unsupportedCount != 4 ||
		readCalls != readCount || writerCalls != writerCount || fetchCalls != fetchCount ||
		len(directExits) != 91 || len(copyExits) != 5 {
		return fail("producer or fault counts changed")
	}
	// All validation above is read-only on this freshly parsed AST.
	for object, uses := range bindings {
		if promoted[object] {
			for identifier := range uses {
				identifier.Name = slot
			}
		}
	}
	for _, init := range writerInits {
		init.Tok = token.ASSIGN
	}
	for _, list := range lists {
		updated := make([]ast.Stmt, 0, len(*list))
		for _, statement := range *list {
			if declaration, ok := statement.(*ast.DeclStmt); ok && removeDeclarations[declaration] {
				continue
			}
			exit, ok := statement.(*ast.ReturnStmt)
			if ok && (directExits[exit] || copyExits[exit]) {
				if copyExits[exit] {
					updated = append(updated, &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(slot)},
						Tok: token.ASSIGN, Rhs: []ast.Expr{exit.Results[2]}})
				}
				updated = append(updated, &ast.BranchStmt{Tok: token.GOTO, Label: ast.NewIdent(label)})
				continue
			}
			updated = append(updated, statement)
		}
		*list = updated
	}
	faultReturn := &ast.ReturnStmt{Results: []ast.Expr{
		&ast.BinaryExpr{X: ast.NewIdent("executed"), Op: token.ADD,
			Y: &ast.CallExpr{Fun: ast.NewIdent("uint64"), Args: []ast.Expr{ast.NewIdent("index")}}},
		ast.NewIdent("nil"), ast.NewIdent(slot)}}
	loop.Body.List = append(loop.Body.List, &ast.BranchStmt{Tok: token.CONTINUE},
		&ast.LabeledStmt{Label: ast.NewIdent(label), Stmt: faultReturn})
	declaration := &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR,
		Specs: []ast.Spec{&ast.ValueSpec{Names: []*ast.Ident{ast.NewIdent(slot)}, Type: ast.NewIdent("error")}}}}
	fn.Body.List = append(fn.Body.List[:1], append([]ast.Stmt{declaration}, fn.Body.List[1:]...)...)
	start, end := fset.PositionFor(fn.Pos(), false).Offset, fset.PositionFor(fn.End(), false).Offset
	fn.Doc = nil // Preserve the original external doc bytes rather than formatting them twice.
	var replacement bytes.Buffer
	if err := format.Node(&replacement, fset, fn); err != nil {
		return nil, fmt.Errorf("cached-store fault format: %w", err)
	}
	result := make([]byte, 0, len(source)+replacement.Len())
	result = append(result, source[:start]...)
	result = append(result, replacement.Bytes()...)
	result = append(result, source[end:]...)
	if _, err := parser.ParseFile(token.NewFileSet(), "promoted-application41.go", result, 0); err != nil {
		return nil, fmt.Errorf("cached-store fault result parse: %w", err)
	}
	return result, nil
}
