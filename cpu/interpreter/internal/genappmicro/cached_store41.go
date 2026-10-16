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

// Run after the existing flags/transfer/route
// transforms, before rewriteApplicationBlockReturns. No source subtree is
// pruned: the pre-existing final generated 54 route guards must remain exact.
// Only seven direct scalar-store paths are wrapped, not PUSH/STM or the other
// transfer subtype copies. The original fault IfStmt and following jitGen
// check are retained, not reimplemented from a new retirement convention.
const applicationCachedScalarStores41 = 7

type applicationStoreSite41 struct {
	body  *[]ast.Stmt
	width int
}

func storeText41(fset *token.FileSet, node ast.Node) (string, error) {
	if node == nil {
		return "", nil
	}
	var out bytes.Buffer
	if err := format.Node(&out, fset, node); err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(out.String()), ""), nil
}

func requireApplicationCachedScalarStores41(count int) error {
	if count != applicationCachedScalarStores41 {
		return fmt.Errorf("application cached scalar stores=%d, want exactly 7", count)
	}
	return nil
}

func cacheApplicationScalarStores41(fset *token.FileSet, stmt ast.Stmt) (int, error) {
	dispatch, ok := stmt.(*ast.SwitchStmt)
	if !ok {
		return 0, nil
	}
	tag, err := storeText41(fset, dispatch.Tag)
	if err != nil || tag != "in.appOp" {
		return 0, err
	}
	var conflict bool
	ast.Inspect(dispatch, func(node ast.Node) bool {
		if name, ok := node.(*ast.Ident); ok && strings.HasPrefix(name.Name, "cachedStore41") {
			conflict = true
		}
		return true
	})
	if conflict {
		return 0, fmt.Errorf("application cached-store local name already exists")
	}
	// All shape checks and replacement construction precede the first mutation.
	var sites []applicationStoreSite41
	seen := make(map[string]bool)
	for _, statement := range dispatch.Body.List {
		clause, ok := statement.(*ast.CaseClause)
		if !ok {
			return 0, fmt.Errorf("application cached-store dispatch clause changed")
		}
		wanted := ""
		for _, expr := range clause.List {
			label, err := storeText41(fset, expr)
			if err != nil {
				return 0, err
			}
			switch label {
			case "thumbAppRegisterTransferBase+0", "thumbAppRegisterTransferBase+1", "thumbAppRegisterTransferBase+2",
				"thumbAppImmediateTransferBase+0", "thumbAppImmediateTransferBase+2", "thumbHalfwordTransfer", "thumbStackTransfer":
				wanted = label
			}
		}
		if wanted == "" {
			continue
		}
		if len(clause.List) != 1 || seen[wanted] {
			return 0, fmt.Errorf("application cached-store grouped/duplicate %s", wanted)
		}
		seen[wanted] = true
		if err := validateApplicationStorePrelude41(fset, clause, wanted); err != nil {
			return 0, err
		}
		var site applicationStoreSite41
		switch wanted {
		case "thumbHalfwordTransfer", "thumbStackTransfer":
			site.width = 2
			if wanted == "thumbStackTransfer" {
				site.width = 4
			}
			for _, child := range clause.Body {
				branch, ok := child.(*ast.IfStmt)
				if !ok {
					continue
				}
				condition, err := storeText41(fset, branch.Cond)
				if err != nil {
					return 0, err
				}
				if condition != "load" {
					continue
				}
				fallback, ok := branch.Else.(*ast.BlockStmt)
				if branch.Init != nil || !ok || site.body != nil {
					return 0, fmt.Errorf("application cached-store load/else changed")
				}
				site.body = &fallback.List
			}
		default:
			var nested *ast.SwitchStmt
			for _, child := range clause.Body {
				if candidate, ok := child.(*ast.SwitchStmt); ok {
					if nested != nil {
						return 0, fmt.Errorf("application cached-store multiple transfer switches")
					}
					nested = candidate
				}
			}
			if nested == nil || nested.Init != nil {
				return 0, fmt.Errorf("application cached-store transfer switch missing")
			}
			innerLabel := ""
			if strings.HasPrefix(wanted, "thumbAppRegisterTransferBase+") {
				if len(clause.Body) == 0 {
					return 0, fmt.Errorf("application cached-store register prelude missing")
				}
				subtype := wanted[len(wanted)-1:]
				constant, err := storeText41(fset, clause.Body[0])
				if err != nil || constant != "constop="+subtype {
					return 0, fmt.Errorf("application cached-store register subtype changed")
				}
				innerTag, err := storeText41(fset, nested.Tag)
				if err != nil || innerTag != "op" {
					return 0, fmt.Errorf("application cached-store register dispatch changed")
				}
				innerLabel = subtype
				site.width = 4
				if subtype == "1" {
					site.width = 2
				} else if subtype == "2" {
					site.width = 1
				}
			} else {
				if nested.Tag != nil || len(clause.Body) < 2 {
					return 0, fmt.Errorf("application cached-store immediate dispatch changed")
				}
				byteValue := "false"
				site.width = 4
				if strings.HasSuffix(wanted, "+2") {
					byteValue, innerLabel, site.width = "true", "byteTransfer", 1
				}
				first, err := storeText41(fset, clause.Body[0])
				if err != nil {
					return 0, err
				}
				second, err := storeText41(fset, clause.Body[1])
				if err != nil || first != "constbyteTransfer="+byteValue || second != "constload=false" {
					return 0, fmt.Errorf("application cached-store immediate subtype changed")
				}
			}
			for _, child := range nested.Body.List {
				arm, ok := child.(*ast.CaseClause)
				if !ok {
					return 0, fmt.Errorf("application cached-store inner clause changed")
				}
				label := ""
				if len(arm.List) != 0 {
					label, err = storeText41(fset, arm.List[0])
					if err != nil {
						return 0, err
					}
				}
				if label == innerLabel {
					if len(arm.List) > 1 || site.body != nil {
						return 0, fmt.Errorf("application cached-store inner grouped/duplicate")
					}
					site.body = &arm.Body
				}
			}
		}
		if site.body == nil || len(*site.body) != 2 {
			return 0, fmt.Errorf("application cached-store statement pair changed in %s", wanted)
		}
		value, writer := "b.regs[rd]", "write32"
		if site.width == 1 {
			value, writer = "byte(b.regs[rd])", "write8"
		} else if site.width == 2 {
			value, writer = "uint16(b.regs[rd])", "write16"
		}
		fault, err := storeText41(fset, (*site.body)[0])
		if err != nil {
			return 0, err
		}
		generation, err := storeText41(fset, (*site.body)[1])
		if err != nil || fault != "iferr:=b."+writer+"(address,"+value+",cpu.PermissionWrite);err!=nil{returnindex,false,nil,err}" ||
			generation != "ifb.jitGen!=blockGeneration{returnindex+1,false,nil,nil}" {
			return 0, fmt.Errorf("application cached-store operands/fault/generation changed in %s", wanted)
		}
		sites = append(sites, site)
	}
	if err := requireApplicationCachedScalarStores41(len(sites)); err != nil {
		return 0, err
	}
	replacements := make([]ast.Stmt, len(sites))
	for index, site := range sites {
		value, helper := "b.regs[rd]", "cachedStore32"
		if site.width == 1 {
			value, helper = "byte(b.regs[rd])", "cachedStore8"
		} else if site.width == 2 {
			value, helper = "uint16(b.regs[rd])", "cachedStore16"
		}
		label := fmt.Sprintf("cachedStore41Done%d", index)
		text := fmt.Sprintf(`package interpreter
func replacement() {{
if foldPrivateScalarBounds && privateDataRoute {
if cachedStore41Perms, cachedStore41Hit := %s(&b.privateDataCache[privateDataCacheIndex(address, cpu.PermissionWrite)], address, %s); cachedStore41Hit {
b.cachedStore41SMC(address, %d, cachedStore41Perms)
goto %s
}
}
%s: ;
}}`, helper, value, site.width, label, label)
		parsed, err := parser.ParseFile(fset, "cached-store41-replacement.go", text, 0)
		if err != nil {
			return 0, err
		}
		block := parsed.Decls[0].(*ast.FuncDecl).Body.List[0].(*ast.BlockStmt)
		// Reuse the exact original full writer and its error/retirement AST once.
		block.List = []ast.Stmt{block.List[0], (*site.body)[0], block.List[1]}
		replacements[index] = block
	}
	for index, site := range sites {
		(*site.body)[0] = replacements[index]
	}
	return len(sites), nil
}

func validateApplicationStorePrelude41(fset *token.FileSet, clause *ast.CaseClause, label string) error {
	var expected []string
	switch {
	case strings.HasPrefix(label, "thumbAppRegisterTransferBase+"):
		expected = []string{"constop=" + label[len(label)-1:], "ro:=uint32(instruction>>6)&7", "rb:=uint32(instruction>>3)&7", "rd:=uint32(instruction)&7", "address:=b.regs[rb]+b.regs[ro]"}
	case strings.HasPrefix(label, "thumbAppImmediateTransferBase+"):
		byteValue := "false"
		if strings.HasSuffix(label, "+2") {
			byteValue = "true"
		}
		expected = []string{"constbyteTransfer=" + byteValue, "constload=false", "offset:=uint32(instruction>>6)&0x1f", "rb:=uint32(instruction>>3)&7", "rd:=uint32(instruction)&7", "if!byteTransfer{offset*=4}", "address:=b.regs[rb]+offset"}
	case label == "thumbHalfwordTransfer":
		expected = []string{"load:=instruction&(1<<11)!=0", "offset:=uint32(instruction>>6)&0x1f", "rb:=uint32(instruction>>3)&7", "rd:=uint32(instruction)&7", "address:=b.regs[rb]+offset*2"}
	case label == "thumbStackTransfer":
		expected = []string{"load:=instruction&(1<<11)!=0", "rd:=uint32(instruction>>8)&7", "address:=b.regs[cpu.RegisterSP]+uint32(instruction&0xff)*4"}
	}
	if len(expected) == 0 || len(clause.Body) != len(expected)+1 {
		return fmt.Errorf("application cached-store prelude length changed in %s", label)
	}
	for index, want := range expected {
		got, err := storeText41(fset, clause.Body[index])
		if err != nil || got != want {
			return fmt.Errorf("application cached-store prelude %d changed in %s", index, label)
		}
	}
	return nil
}

// validateApplicationCachedStoreNamespace41 runs over every emitted statement
// before flags/transfer/route/store transforms, not only the opcode switch.
func validateApplicationCachedStoreNamespace41(body []ast.Stmt) error {
	var conflict string
	for _, statement := range body {
		ast.Inspect(statement, func(node ast.Node) bool {
			if conflict != "" {
				return false
			}
			if name, ok := node.(*ast.Ident); ok && strings.HasPrefix(name.Name, "cachedStore41") {
				conflict = name.Name
				return false
			}
			return true
		})
		if conflict != "" {
			return fmt.Errorf("application cached-store namespace already exists: %s", conflict)
		}
	}
	return nil
}
