package main

import (
	"go/ast"
	"go/types"
)

// calledFunc returns the function or method that a call names. It returns nil
// for a conversion, a builtin, or a call through a function value.
func calledFunc(info *types.Info, call *ast.CallExpr) *types.Func {
	var id *ast.Ident
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		id = fun
	case *ast.SelectorExpr:
		id = fun.Sel
	default:
		return nil
	}
	fn, _ := info.Uses[id].(*types.Func)
	return fn
}

// receiverTypeName returns the name of the named type that a method is declared
// on, or "" for a plain function.
func receiverTypeName(fn *types.Func) string {
	recv := fn.Type().(*types.Signature).Recv()
	if recv == nil {
		return ""
	}
	t := recv.Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}
