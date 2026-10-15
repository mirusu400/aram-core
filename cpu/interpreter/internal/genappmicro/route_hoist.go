package main

import (
	"fmt"
	"go/ast"
	"go/token"
)

const applicationPrivateRouteGuards = 54

// This transform applies only to the application copy, after transfer/flag
// specialization. Validate the whole statement before mutating any condition.
// Invariance of these fields over an admitted call is a separately reviewed
// runtime contract, not a fact this local AST walk can prove.
func hoistApplicationPrivateRoute(stmt ast.Stmt) (int, error) {
	var guards []*ast.IfStmt
	allowed := make(map[*ast.SelectorExpr]bool)
	var failure error
	ast.Inspect(stmt, func(node ast.Node) bool {
		if failure != nil {
			return false
		}
		if ident, ok := node.(*ast.Ident); ok && ident.Name == "privateDataRoute" {
			failure = fmt.Errorf("application private route local already exists")
			return false
		}
		switch current := node.(type) {
		case *ast.AssignStmt:
			for _, lhs := range current.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok && ident.Name == "b" {
					failure = fmt.Errorf("application receiver is assigned or shadowed")
				}
			}
		case *ast.ValueSpec:
			for _, name := range current.Names {
				if name.Name == "b" {
					failure = fmt.Errorf("application receiver is redeclared")
				}
			}
		case *ast.RangeStmt:
			for _, binding := range []ast.Expr{current.Key, current.Value} {
				if ident, ok := binding.(*ast.Ident); ok && ident.Name == "b" {
					failure = fmt.Errorf("application receiver is range-bound")
				}
			}
		case *ast.FuncLit:
			failure = fmt.Errorf("application instruction closure requires route review")
		case *ast.IfStmt:
			if !containsApplicationRouteField(current.Cond) {
				break
			}
			physical, tlb, ok := applicationPrivateRouteCondition(current.Cond)
			if !ok || current.Init != nil {
				failure = fmt.Errorf("application private route guard shape changed")
				break
			}
			guards = append(guards, current)
			allowed[physical], allowed[tlb] = true, true
		}
		return failure == nil
	})
	if failure != nil {
		return 0, failure
	}
	ast.Inspect(stmt, func(node ast.Node) bool {
		if access, ok := node.(*ast.SelectorExpr); ok && isApplicationRouteField(access) && !allowed[access] {
			failure = fmt.Errorf("application route field used outside an exact guard")
		}
		return failure == nil
	})
	if failure != nil {
		return 0, failure
	}
	for _, guard := range guards {
		guard.Cond = ast.NewIdent("privateDataRoute")
	}
	return len(guards), nil
}

func isApplicationRouteField(access *ast.SelectorExpr) bool {
	return access.Sel.Name == "physicalAccess" || access.Sel.Name == "tlb"
}

func containsApplicationRouteField(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if access, ok := node.(*ast.SelectorExpr); ok && isApplicationRouteField(access) {
			found = true
		}
		return true
	})
	return found
}

func applicationPrivateRouteCondition(expr ast.Expr) (*ast.SelectorExpr, *ast.SelectorExpr, bool) {
	both, ok := expr.(*ast.BinaryExpr)
	if !ok || both.Op != token.LAND {
		return nil, nil, false
	}
	not, ok := both.X.(*ast.UnaryExpr)
	if !ok || not.Op != token.NOT {
		return nil, nil, false
	}
	physical, ok := not.X.(*ast.SelectorExpr)
	if !ok || !applicationReceiverField(physical, "physicalAccess") {
		return nil, nil, false
	}
	comparison, ok := both.Y.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.EQL {
		return nil, nil, false
	}
	tlb, ok := comparison.X.(*ast.SelectorExpr)
	if !ok || !applicationReceiverField(tlb, "tlb") {
		return nil, nil, false
	}
	nilValue, ok := comparison.Y.(*ast.Ident)
	if !ok || nilValue.Name != "nil" {
		return nil, nil, false
	}
	return physical, tlb, true
}

func applicationReceiverField(access *ast.SelectorExpr, name string) bool {
	receiver, ok := access.X.(*ast.Ident)
	return ok && receiver.Name == "b" && access.Sel.Name == name
}

func requireApplicationPrivateRouteCount(count int) error {
	if count != applicationPrivateRouteGuards {
		return fmt.Errorf("application private route guards=%d, want exactly%d; review generation", count, applicationPrivateRouteGuards)
	}
	return nil
}
