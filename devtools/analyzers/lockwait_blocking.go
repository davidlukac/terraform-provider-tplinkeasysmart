package main

import (
	"go/ast"
	"go/types"
)

// netMethods are the names of the methods of a connection that wait for the network.
var netMethods = map[string]bool{
	"Read": true, "Write": true, "ReadFrom": true, "WriteTo": true, "ReadFromUDP": true, "WriteToUDP": true,
	"ReadMsgUDP": true, "WriteMsgUDP": true, "Accept": true, "AcceptTCP": true,
}

// netFuncs are the functions of package net that wait for the network (dial, listen, resolve).
var netFuncs = map[string]bool{
	"Dial": true, "DialContext": true, "DialUDP": true, "DialTCP": true, "Listen": true, "ListenPacket": true,
	"ListenUDP": true, "LookupHost": true, "LookupIP": true, "LookupIPAddr": true, "LookupAddr": true,
	"LookupPort": true, "LookupCNAME": true, "LookupMX": true, "LookupTXT": true,
}

// blocking describes why a call may block, or returns "".
func (w *walker) blocking(c *ast.CallExpr) string {
	info := w.pass.TypesInfo
	if sel, ok := ast.Unparen(c.Fun).(*ast.SelectorExpr); ok {
		if fn, _ := info.Uses[sel.Sel].(*types.Func); fn != nil && fn.Pkg() != nil {
			if why, decided := packageBlocking(fn); decided {
				return why
			}
		}
		if netMethods[sel.Sel.Name] && hasMethod(info.TypeOf(sel.X), "SetReadDeadline") {
			return "network " + sel.Sel.Name
		}
	}
	ft := info.TypeOf(c.Fun)
	if ft == nil {
		return ""
	}
	t, ok := ft.Underlying().(*types.Signature)
	if ok && takesContext(t) && !isConversion(info, c) {
		return "call of " + exprText(c.Fun) + ", which takes a context"
	}
	return ""
}

// packageBlocking classifies a function of the standard library by its package; decided is false when
// the package says nothing and the signature decides.
func packageBlocking(fn *types.Func) (why string, decided bool) {
	switch p := fn.Pkg().Path(); {
	case p == "time" && fn.Name() == "Sleep":
		return "time.Sleep", true
	case p == "sync" && fn.Name() == "Wait" && recvName(fn) == "WaitGroup":
		return "sync.WaitGroup.Wait", true
	case p == "context" || p == "log/slog" || p == "testing":
		return "", true
	case p == "net":
		if netFuncs[fn.Name()] || netMethods[fn.Name()] {
			return "network or process call " + fn.FullName(), true
		}
		return "", true
	case p == "net/http" || p == "os/exec":
		return "network or process call " + fn.FullName(), true
	}
	return "", false
}

// recvName is the name of the type a method is declared on, or "" for a function.
func recvName(fn *types.Func) string {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return ""
	}
	t := sig.Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n.Obj().Name()
	}
	return ""
}

// hasMethod reports whether the type, or a pointer to it, has a method of that name.
func hasMethod(t types.Type, name string) bool {
	if t == nil {
		return false
	}
	// The method set of a pointer to an interface is empty: take the interface's own.
	_, isPointer := t.Underlying().(*types.Pointer)
	_, isInterface := t.Underlying().(*types.Interface)
	if !isPointer && !isInterface {
		t = types.NewPointer(t)
	}
	ms := types.NewMethodSet(t)
	for i := range ms.Len() {
		if ms.At(i).Obj().Name() == name {
			return true
		}
	}
	return false
}

// takesContext reports whether a signature has a context.Context parameter.
func takesContext(sig *types.Signature) bool {
	for i := range sig.Params().Len() {
		n, ok := sig.Params().At(i).Type().(*types.Named)
		if ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "context" && n.Obj().Name() == "Context" {
			return true
		}
	}
	return false
}

// isConversion reports whether c is a type conversion, not a call.
func isConversion(info *types.Info, c *ast.CallExpr) bool {
	tv, ok := info.Types[c.Fun]
	return ok && tv.IsType()
}

func exprText(e ast.Expr) string { return types.ExprString(e) }
