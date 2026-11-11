package main

import (
	"go/ast"
	"go/token"
	"go/types"
)

// stmts walks a statement list in order and returns the locks held at its end and whether the list ends
// the path (return, panic, break, continue).
func (w *walker) stmts(list []ast.Stmt, st lockSet) (lockSet, bool) {
	for _, s := range list {
		var done bool
		if st, done = w.stmt(s, st); done {
			return st, true
		}
	}
	return st, false
}

// stmt walks one statement: compound ones through their own helpers, simple ones by scanning their expressions.
func (w *walker) stmt(s ast.Stmt, st lockSet) (lockSet, bool) {
	switch s := s.(type) {
	case *ast.BlockStmt:
		return w.stmts(s.List, st)
	case *ast.IfStmt:
		return w.ifStmt(s, st)
	case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		return w.compound(s, st), false
	case *ast.LabeledStmt:
		return w.stmt(s.Stmt, st)
	case *ast.ReturnStmt:
		for _, e := range s.Results {
			w.scan(e, st, s.Pos(), false)
		}
		return st, true
	case *ast.BranchStmt:
		return st, true
	case *ast.DeferStmt:
		return w.deferred(s, st), false
	case *ast.GoStmt:
		for _, a := range s.Call.Args {
			w.scan(a, st, s.Pos(), false)
		}
		return st, false
	case *ast.SendStmt:
		w.scan(s.Value, st, s.Pos(), false)
		w.block(s.Pos(), st, "channel send")
		return st, false
	default:
		return w.simple(s, st)
	}
}

// simple walks the statements that hold expressions and nothing else.
func (w *walker) simple(s ast.Stmt, st lockSet) (lockSet, bool) {
	switch s := s.(type) {
	case *ast.ExprStmt:
		st = w.scan(s.X, st, s.Pos(), true)
		c, ok := s.X.(*ast.CallExpr)
		return st, ok && isPanic(c)
	case *ast.AssignStmt:
		for _, e := range s.Rhs {
			st = w.scan(e, st, s.Pos(), true)
		}
	case *ast.DeclStmt:
		ast.Inspect(s, func(n ast.Node) bool {
			if e, ok := n.(ast.Expr); ok {
				st = w.scan(e, st, s.Pos(), true)
				return false
			}
			return true
		})
	case *ast.IncDecStmt:
		st = w.scan(s.X, st, s.Pos(), false)
	}
	return st, false
}

func isPanic(c *ast.CallExpr) bool {
	id, ok := c.Fun.(*ast.Ident)
	return ok && id.Name == "panic"
}

// ifStmt walks both branches; a branch that ends the path does not flow into what follows.
func (w *walker) ifStmt(s *ast.IfStmt, st lockSet) (lockSet, bool) {
	if s.Init != nil {
		st, _ = w.stmt(s.Init, st)
	}
	w.scan(s.Cond, st, s.Pos(), false)
	thenSt, thenDone := w.stmts(s.Body.List, st.clone())
	elseSt, elseDone := st, false
	if s.Else != nil {
		elseSt, elseDone = w.stmt(s.Else, st.clone())
	}
	switch {
	case thenDone && elseDone:
		return union(thenSt, elseSt), true
	case thenDone:
		return elseSt, false
	case elseDone:
		return thenSt, false
	}
	return union(thenSt, elseSt), false
}

// compound walks loops, switches and select; what may be held after them is what may be held after any path through.
func (w *walker) compound(s ast.Stmt, st lockSet) lockSet {
	switch s := s.(type) {
	case *ast.ForStmt:
		if s.Init != nil {
			st, _ = w.stmt(s.Init, st)
		}
		w.scan(s.Cond, st, s.Pos(), false)
		body, _ := w.stmts(s.Body.List, st.clone())
		return union(st, body)
	case *ast.RangeStmt:
		w.scan(s.X, st, s.Pos(), false)
		if t := w.pass.TypesInfo.TypeOf(s.X); t != nil {
			if _, ok := t.Underlying().(*types.Chan); ok {
				w.block(s.Pos(), st, "range over a channel")
			}
		}
		body, _ := w.stmts(s.Body.List, st.clone())
		return union(st, body)
	case *ast.SwitchStmt:
		if s.Init != nil {
			st, _ = w.stmt(s.Init, st)
		}
		w.scan(s.Tag, st, s.Pos(), false)
		return w.caseBodies(s.Body.List, st)
	case *ast.TypeSwitchStmt:
		if s.Init != nil {
			st, _ = w.stmt(s.Init, st)
		}
		return w.caseBodies(s.Body.List, st)
	case *ast.SelectStmt:
		return w.selectStmt(s, st)
	}
	return st
}

func (w *walker) caseBodies(list []ast.Stmt, st lockSet) lockSet {
	out := st
	for _, c := range list {
		body, _ := w.stmts(c.(*ast.CaseClause).Body, st.clone())
		out = union(out, body)
	}
	return out
}

func (w *walker) selectStmt(s *ast.SelectStmt, st lockSet) lockSet {
	hasDefault := false
	for _, c := range s.Body.List {
		if c.(*ast.CommClause).Comm == nil {
			hasDefault = true
		}
	}
	if !hasDefault {
		w.block(s.Pos(), st, "select without default")
	}
	out := st
	for _, c := range s.Body.List {
		body, _ := w.stmts(c.(*ast.CommClause).Body, st.clone())
		out = union(out, body)
	}
	return out
}

// deferred handles `defer x.Unlock()` and `defer func() { ... x.Unlock() ... }()`: the lock is held to the
// end of the function.
func (w *walker) deferred(d *ast.DeferStmt, st lockSet) lockSet {
	st = st.clone()
	mark := func(key string) {
		if v, ok := st[key]; ok {
			v.deferred = true
			st[key] = v
		}
	}
	if key, kind := w.lockCall(d.Call); kind == releasesLock {
		mark(key)
		return st
	}
	if fl, ok := d.Call.Fun.(*ast.FuncLit); ok {
		ast.Inspect(fl.Body, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if key, kind := w.lockCall(c); kind == releasesLock {
					mark(key)
				}
			}
			return true
		})
		return st
	}
	for _, a := range d.Call.Args {
		w.scan(a, st, d.Pos(), false)
	}
	return st
}

// scan visits the calls and receives of an expression in source order, applying lock and unlock events
// when apply is set and reporting blocking operations while something is held. It does not enter
// function literals (they are walked as functions of their own).
func (w *walker) scan(e ast.Node, st lockSet, at token.Pos, apply bool) lockSet {
	if e == nil {
		return st
	}
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				w.block(at, st, "channel receive")
			}
		case *ast.CallExpr:
			if key, kind := w.lockCall(n); kind != notALock {
				if apply {
					st = w.applyLock(st, key, kind, n.Pos(), at)
				}
				return true
			}
			if why := w.blocking(n); why != "" {
				w.block(n.Pos(), st, why)
			}
		}
		return true
	})
	return st
}

// applyLock returns the locks held after a call that takes or releases key.
func (w *walker) applyLock(st lockSet, key string, kind lockKind, call, stmt token.Pos) lockSet {
	st = st.clone()
	if kind == takesLock {
		st[key] = held{line: w.line(call), excused: w.notes.near(call) || w.notes.near(stmt)}
		return st
	}
	delete(st, key)
	return st
}
