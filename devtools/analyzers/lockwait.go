package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// lockwaitMarker is the comment that explains a blocking operation done while a lock is held; the reason
// after it is mandatory.
const lockwaitMarker = "lockwait:"

// defaultLockFuncs are the escp-go client's own lock wrappers: c.lock takes c.mu, c.beginModify takes
// c.modify, and acquire(ctx, &mu) takes the mutex it is given.
const defaultLockFuncs = "lock=mu,acquire,beginModify=modify"

// lockwaitConfig is what the flags of the lockwait check set.
type lockwaitConfig struct {
	lockFuncs string
	tests     bool
}

// Lockwait is the lockwait check: a blocking operation between taking a lock and releasing it.
//
// A lock held across a channel operation, a sleep, a wait, a network read or write, or a call that takes a
// context is how "Close waits for a stuck exchange" and "a cancel is not seen" defects begin (the
// firmware gate of escp-go#154 held its lock across a read of the switch). Sometimes it is intended (the
// client serializes a whole exchange on purpose: the device handles one request at a time), and then the
// rule is that the reason is written next to it, so the next reader knows what ends the wait:
//
//	// lockwait: <reason>
//
// on the operation's line, on the line above it, on the line of the Lock (the whole critical section is
// explained) or in the function's doc comment (the whole function). An annotation without a reason is
// itself reported.
//
// What blocks: a channel send or receive, a `range` over a channel, a `select` without `default`,
// time.Sleep, sync.WaitGroup.Wait, a call into net, net/http or os/exec that dials, listens, reads or
// writes, a Read/Write/Accept method on a type that has SetReadDeadline (the library's own socket
// interface), and any call whose signature takes a context.Context (log/slog and context calls aside).
// What takes a lock: sync.Mutex and sync.RWMutex Lock and RLock (also through an embedded mutex) and the
// project's wrappers, named by -lockwait.lockfuncs, a comma-separated list of the functions and methods that
// return with a lock held: `name=field` says the lock is that field of the receiver (`c.lock(ctx)` with
// `lock=mu` takes c.mu, which `c.mu.Unlock()` releases), `name` alone that the lock is the receiver or the
// `&argument` (`acquire(ctx, &c.mu)`). The default is the escp-go client's: "lock=mu,acquire,beginModify=modify".
// `-lockwait.tests` also checks _test.go files.
//
// It works inside one function, in statement order: a function that takes the lock for its caller is only
// seen at the caller's call; a function literal is a function of its own, so a closure handed to a call
// made under a lock is reported at the call, not inside.
var Lockwait = newLockwait()

func newLockwait() *analysis.Analyzer {
	cfg := &lockwaitConfig{}
	a := &analysis.Analyzer{
		Name: "lockwait",
		Doc:  "reports a blocking operation (channel, sleep, wait, network I/O, a call taking a context) between a lock and its unlock that carries no `// lockwait: <reason>` comment",
		Run:  func(pass *analysis.Pass) (any, error) { return runLockwait(pass, cfg) },
	}
	a.Flags.StringVar(&cfg.lockFuncs, "lockfuncs", defaultLockFuncs, "comma-separated functions and methods that return with a lock held: name=field (a field of the receiver is the lock) or name (the receiver, or the &argument, is)")
	a.Flags.BoolVar(&cfg.tests, "tests", false, "also check _test.go files")
	return a
}

func runLockwait(pass *analysis.Pass, cfg *lockwaitConfig) (any, error) {
	wrappers := parseLockFuncs(cfg.lockFuncs)
	for _, f := range pass.Files {
		if !cfg.tests && isTestFile(pass, f) {
			continue
		}
		w := &walker{pass: pass, notes: readAnnotations(pass, f, lockwaitMarker), wrappers: wrappers}
		ast.Inspect(f, func(n ast.Node) bool {
			switch fn := n.(type) {
			case *ast.FuncDecl:
				if fn.Body != nil {
					fw := *w
					fw.excused = docHasMarker(fn.Doc, lockwaitMarker)
					fw.stmts(fn.Body.List, lockSet{})
				}
			case *ast.FuncLit:
				fw := *w
				fw.stmts(fn.Body.List, lockSet{})
			}
			return true
		})
	}
	return nil, nil
}

// parseLockFuncs reads the value of -lockwait.lockfuncs into a map from the name of a wrapper to the field
// of the receiver it locks ("" when the lock is the receiver itself or the &argument).
func parseLockFuncs(list string) map[string]string {
	wrappers := map[string]string{}
	for _, entry := range strings.Split(list, ",") {
		name, field, _ := strings.Cut(strings.TrimSpace(entry), "=")
		if name = strings.TrimSpace(name); name != "" {
			wrappers[name] = strings.TrimSpace(field)
		}
	}
	return wrappers
}

// held describes one lock taken and not yet released on the path being walked.
type held struct {
	line     int
	deferred bool // released by a defer: held to the end of the function
	excused  bool // the Lock line carries an annotation: the whole critical section is explained
}

// lockSet is the locks held at a point of a function, by the text of the lock expression.
type lockSet map[string]held

func (s lockSet) clone() lockSet {
	c := make(lockSet, len(s))
	for k, v := range s {
		c[k] = v
	}
	return c
}

// union is what may be held after two paths join: a lock held on either, and held to the end of the
// function when either says so.
func union(a, b lockSet) lockSet {
	c := a.clone()
	for k, v := range b {
		if old, ok := c[k]; !ok || (!old.deferred && v.deferred) {
			c[k] = v
		}
	}
	return c
}

// walker walks one function (or function literal) in statement order.
type walker struct {
	pass     *analysis.Pass
	notes    *annotations
	wrappers map[string]string // wrapper name -> field of the receiver it locks
	excused  bool              // the function's doc comment carries the annotation
}

func (w *walker) line(p token.Pos) int { return w.pass.Fset.Position(p).Line }

// block reports a blocking operation at p when a lock is held and nothing excuses it. Every lock held
// must be explained (an annotation on its Lock line, on the operation, or on the function): a section
// nested in another is not silenced by the outer one's reason.
func (w *walker) block(p token.Pos, st lockSet, what string) {
	if w.excused || w.notes.near(p) {
		return
	}
	var open []string
	for key, h := range st {
		if !h.excused {
			open = append(open, key)
		}
	}
	if len(open) == 0 {
		return
	}
	// One lock is named, the first by name, so the message does not change from one run to the next.
	slices.Sort(open)
	w.pass.Reportf(p, "%s while %s is held (locked at line %d): add `// lockwait: <reason>` or move it out of the critical section", what, open[0], st[open[0]].line)
}

// lockKind says what a call does to a lock.
type lockKind int

const (
	notALock lockKind = iota
	takesLock
	releasesLock
)

// lockCall classifies a call as taking or releasing a lock, with the lock's key.
func (w *walker) lockCall(c *ast.CallExpr) (string, lockKind) {
	if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
		if fn, ok := w.pass.TypesInfo.Uses[sel.Sel].(*types.Func); ok && fn.Pkg() != nil && fn.Pkg().Path() == "sync" {
			return types.ExprString(sel.X), syncLockKind(fn.Name())
		}
		if field, ok := w.wrappers[sel.Sel.Name]; ok {
			return joinField(types.ExprString(sel.X), field), takesLock
		}
	}
	if id, ok := c.Fun.(*ast.Ident); ok {
		if field, ok := w.wrappers[id.Name]; ok {
			return joinField(wrapperKey(id.Name, c.Args), field), takesLock
		}
	}
	return "", notALock
}

// syncLockKind classifies the name of a method of package sync.
func syncLockKind(name string) lockKind {
	switch name {
	case "Lock", "RLock":
		return takesLock
	case "Unlock", "RUnlock":
		return releasesLock
	}
	return notALock
}

// wrapperKey is the lock a call of a wrapper function takes: the `&argument` when there is one, else the function.
func wrapperKey(fn string, args []ast.Expr) string {
	key := fn
	for _, a := range args {
		if u, ok := a.(*ast.UnaryExpr); ok && u.Op == token.AND {
			key = types.ExprString(u.X)
		}
	}
	return key
}

// joinField is the key of the lock that is field of the expression x (x itself when there is no field).
func joinField(x, field string) string {
	if field == "" {
		return x
	}
	return x + "." + field
}
