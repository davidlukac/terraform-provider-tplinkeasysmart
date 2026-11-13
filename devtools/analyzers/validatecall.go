package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

// validatecallMarker is the comment that explains an exported function that takes a value it does not validate.
const validatecallMarker = "validatecall:"

// Defaults of the flags of the validatecall check: the root package of escp-go, and the method names that
// validate a value. Another repository passes its own with `-validatecall.packages=...`.
const (
	// defaultValidatePackages matches the import path of the packages whose exported API is checked.
	defaultValidatePackages = `^github\.com/davidlukac/escp-go$`
	// defaultValidateNames matches the methods that validate the value they are called on.
	defaultValidateNames = `^Validate\w*$`
)

// validatecallConfig is what the flags of the validatecall check set.
type validatecallConfig struct {
	packages, names string
	tests           bool
}

// Validatecall is the validatecall check: an exported function or method that takes a value whose type has
// a `Validate` method must call it (or a helper that does) before it uses the value.
//
// The shape is the one of the defect class "one rule at every entry" (escp-go#169: an empty
// `SetPortPriorities` sent a full rewrite because the writer did not apply the rule of its argument): a
// caller's value enters through an exported function, and the one place that knows what a valid value is
// is the type's own `Validate`. A function that never calls it, or that hands the value on before it
// does, is the entry that accepts what another refuses.
//
// What counts as validating is the call of a method whose name matches -validatecall.names and that
// returns an error, made on the parameter or on a value taken from it (a field, an element, the variable
// of a range over it), or the call of a helper of the package, at any depth, that makes such a call and is
// given the parameter (`requirePorts(p)`, `c.checkLimit(cfg)`). A "use" before that is the parameter, or a
// value taken from it, as the receiver or an argument of another call; builtins (`len`) and conversions
// are not uses, and reading a field is not one. Only the type of the parameter itself is looked at, so a
// struct without a `Validate` of its own whose fields have one is not required to validate them, and an
// interface type is not looked at.
//
// An exported function that passes the value on to a callee that validates it at the next layer, or
// that does not need the rule, says why with `// validatecall: <reason>` in its doc comment or on the line
// of (or above) the parameter. Configuration is by flag:
//
//	-validatecall.packages  regular expression of the import path of the packages whose exported
//	                        functions are checked (default: the escp-go root package)
//	-validatecall.names     regular expression of the names of the methods that validate (default
//	                        `^Validate\w*$`); a method with such a name is itself not checked
//	-validatecall.tests     also look at _test.go files (default: no)
//
// The check is syntactic, inside one package: it does not follow the value into another package, through
// an interface, or through a variable assigned to a field, so a helper that validates something other
// than the parameter it is handed is taken for one that validates it.
var Validatecall = newValidatecall()

func newValidatecall() *analysis.Analyzer {
	cfg := &validatecallConfig{}
	a := &analysis.Analyzer{
		Name: "validatecall",
		Doc:  "reports an exported function that takes a value of a type with a Validate method and does not call it (or a helper that does) before using the value",
		Run:  func(pass *analysis.Pass) (any, error) { return runValidatecall(pass, cfg) },
	}
	a.Flags.StringVar(&cfg.packages, "packages", defaultValidatePackages, "regular expression of the import path of the packages whose exported functions are checked")
	a.Flags.StringVar(&cfg.names, "names", defaultValidateNames, "regular expression of the names of the methods that validate their receiver")
	a.Flags.BoolVar(&cfg.tests, "tests", false, "also check _test.go files")
	return a
}

// validatecallRules is the compiled form of the flags.
type validatecallRules struct {
	packages, names *regexp.Regexp
}

func compileValidatecall(cfg *validatecallConfig) (*validatecallRules, error) {
	r := &validatecallRules{}
	for _, p := range []struct {
		flag, expr string
		into       **regexp.Regexp
	}{{"packages", cfg.packages, &r.packages}, {"names", cfg.names, &r.names}} {
		re, err := regexp.Compile(p.expr)
		if err != nil {
			return nil, fmt.Errorf("-validatecall.%s: %w", p.flag, err)
		}
		*p.into = re
	}
	return r, nil
}

func runValidatecall(pass *analysis.Pass, cfg *validatecallConfig) (any, error) {
	rules, err := compileValidatecall(cfg)
	if err != nil {
		return nil, err
	}
	if !rules.packages.MatchString(pass.Pkg.Path()) {
		return nil, nil
	}
	var files []*ast.File
	for _, f := range pass.Files {
		if cfg.tests || !isTestFile(pass, f) {
			files = append(files, f)
		}
	}
	vc := &validatecall{pass: pass, rules: rules, validators: validatingFunctions(pass, rules, files)}
	for _, f := range files {
		// An annotation with no reason is reported by readAnnotations; the function is excused either way.
		vc.notes = readAnnotations(pass, f, validatecallMarker)
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && vc.checked(fn) {
				vc.checkFunc(fn)
			}
		}
	}
	return nil, nil
}

// validatecall is the state of one run over one package.
type validatecall struct {
	pass       *analysis.Pass
	rules      *validatecallRules
	validators map[*types.Func]bool
	notes      *annotations // of the file being checked
}

// validatingFunctions is the set of functions and methods of the package that validate: one whose body
// calls a method that validates, or calls another function of the set (a fixed point, so a chain of
// helpers is followed to its end).
func validatingFunctions(pass *analysis.Pass, rules *validatecallRules, files []*ast.File) map[*types.Func]bool {
	decls := map[*types.Func]*ast.FuncDecl{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
				if obj, ok := pass.TypesInfo.Defs[fn.Name].(*types.Func); ok {
					decls[obj] = fn
				}
			}
		}
	}
	set := map[*types.Func]bool{}
	vc := &validatecall{pass: pass, rules: rules, validators: set}
	for changed := true; changed; {
		changed = false
		for obj, fn := range decls {
			if !set[obj] && vc.callsAValidator(fn.Body) {
				set[obj] = true
				changed = true
			}
		}
	}
	return set
}

// callsAValidator reports whether anything in body is a call that validates.
func (vc *validatecall) callsAValidator(body ast.Node) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && vc.isValidation(call) {
			found = true
		}
		return !found
	})
	return found
}

// isValidation reports whether call is a call of a method that validates its receiver, or of a function
// of the package that validates.
func (vc *validatecall) isValidation(call *ast.CallExpr) bool {
	fn, ok := typeutil.Callee(vc.pass.TypesInfo, call).(*types.Func)
	if !ok {
		return false
	}
	return vc.validators[fn.Origin()] || vc.isValidateMethod(fn)
}

// isValidateMethod reports whether fn is a method with a validating name that takes nothing and returns an error.
func (vc *validatecall) isValidateMethod(fn *types.Func) bool {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil || !vc.rules.names.MatchString(fn.Name()) {
		return false
	}
	return sig.Params().Len() == 0 && sig.Results().Len() == 1 && types.Identical(sig.Results().At(0).Type(), types.Universe.Lookup("error").Type())
}

// checked reports whether fn is part of the exported API this check looks at: an exported function with a
// body, or an exported method of an exported type, that is not a validator itself and is not explained.
func (vc *validatecall) checked(fn *ast.FuncDecl) bool {
	if fn.Body == nil || !fn.Name.IsExported() || vc.rules.names.MatchString(fn.Name.Name) || docHasMarker(fn.Doc, validatecallMarker) {
		return false
	}
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return true
	}
	return receiverIsExported(fn.Recv.List[0].Type)
}

// receiverIsExported reports whether the receiver type expression names an exported type (pointer and
// type arguments removed).
func receiverIsExported(e ast.Expr) bool {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.ParenExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return t.IsExported()
		default:
			return false
		}
	}
}

// checkFunc looks at every parameter of fn whose type has a validating method.
func (vc *validatecall) checkFunc(fn *ast.FuncDecl) {
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			obj := vc.pass.TypesInfo.Defs[name]
			if obj == nil || name.Name == "_" || vc.notes.near(name.Pos()) {
				continue
			}
			if typ, method := vc.validatedType(obj.Type()); typ != nil {
				vc.checkParam(fn, name, obj, typ.Obj().Name()+" has a "+method+" method")
			}
		}
	}
}

// validatedType returns the named type of a parameter (or of the elements of a slice, an array or a
// pointer to one) when that type has a validating method, and nil otherwise. An interface type is not
// looked at: what it holds is not known here.
func (vc *validatecall) validatedType(t types.Type) (*types.Named, string) {
	for {
		switch u := t.(type) {
		case *types.Pointer:
			t = u.Elem()
		case *types.Slice:
			t = u.Elem()
		case *types.Array:
			t = u.Elem()
		case *types.Named:
			if _, isInterface := u.Underlying().(*types.Interface); isInterface {
				return nil, ""
			}
			methods := types.NewMethodSet(types.NewPointer(u))
			for m := range methods.Methods() {
				if fn, ok := m.Obj().(*types.Func); ok && vc.isValidateMethod(fn) {
					return u, fn.Name()
				}
			}
			return nil, ""
		default:
			return nil, ""
		}
	}
}

// flow is what one parameter goes through in a function body, in source order.
type flow struct {
	// tainted holds the parameter and every variable taken from it (a range variable, an assignment).
	tainted map[types.Object]bool
	// firstValidation is the first call that validates a value taken from the parameter.
	firstValidation token.Pos
	// validations are the source ranges of those calls; a mention inside one is not a use.
	validations []ast.Node
	// uses are the mentions that hand the value to another call.
	uses []*ast.Ident
}

// checkParam reports a parameter that is never validated, or is used before it is.
func (vc *validatecall) checkParam(fn *ast.FuncDecl, name *ast.Ident, obj types.Object, why string) {
	fl := &flow{tainted: map[types.Object]bool{obj: true}}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.RangeStmt:
			if vc.mentions(fl, n.X) {
				vc.taint(fl, n.Key, n.Value)
			}
		case *ast.AssignStmt:
			if vc.mentionsAny(fl, n.Rhs) && !vc.isValidationResult(n.Rhs) {
				vc.taint(fl, n.Lhs...)
			}
		case *ast.ValueSpec:
			if vc.mentionsAny(fl, n.Values) {
				for _, id := range n.Names {
					vc.taint(fl, id)
				}
			}
		case *ast.CallExpr:
			vc.observe(fl, n)
		}
		return true
	})
	label := fn.Name.Name
	if fn.Recv != nil {
		label = "(" + types.ExprString(fn.Recv.List[0].Type) + ")." + label
	}
	if fl.firstValidation == token.NoPos {
		vc.pass.Reportf(name.Pos(), "%s takes %s (%s) and never validates it: call its validating method (or a helper that does) before the value is used, or add `// validatecall: <reason>`", label, name.Name, why)
		return
	}
	for _, use := range fl.uses {
		if use.Pos() < fl.firstValidation && !insideAny(use, fl.validations) {
			vc.pass.Reportf(use.Pos(), "%s uses %s (%s) before it is validated (line %d): validate first, or add `// validatecall: <reason>`", label, name.Name, why, vc.pass.Fset.Position(fl.firstValidation).Line)
			return
		}
	}
}

// observe records what one call does with the tainted values: a validation of one, or a use of one.
func (vc *validatecall) observe(fl *flow, call *ast.CallExpr) {
	operands := callOperands(call)
	if !vc.mentionsAny(fl, operands) {
		return
	}
	if vc.isValidation(call) {
		if fl.firstValidation == token.NoPos || call.Pos() < fl.firstValidation {
			fl.firstValidation = call.Pos()
		}
		fl.validations = append(fl.validations, call)
		return
	}
	if vc.isBuiltinOrConversion(call) {
		return
	}
	for _, op := range operands {
		ast.Inspect(op, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && fl.tainted[vc.pass.TypesInfo.Uses[id]] {
				fl.uses = append(fl.uses, id)
			}
			return true
		})
	}
}

// isValidationResult reports whether the right-hand side is one call that validates: the error it
// returns is not a value taken from the parameter.
func (vc *validatecall) isValidationResult(rhs []ast.Expr) bool {
	if len(rhs) != 1 {
		return false
	}
	call, ok := rhs[0].(*ast.CallExpr)
	return ok && vc.isValidation(call)
}

// callOperands are the values a call is given: the receiver of a method call (the expression the method is
// selected from) and the arguments.
func callOperands(call *ast.CallExpr) []ast.Expr {
	operands := append([]ast.Expr(nil), call.Args...)
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		operands = append(operands, sel.X)
	}
	return operands
}

// isBuiltinOrConversion reports whether call is `len(x)` and the like, or a conversion `T(x)`: neither
// hands the value to code that could act on it.
func (vc *validatecall) isBuiltinOrConversion(call *ast.CallExpr) bool {
	if tv, ok := vc.pass.TypesInfo.Types[call.Fun]; ok && tv.IsType() {
		return true
	}
	_, builtin := typeutil.Callee(vc.pass.TypesInfo, call).(*types.Builtin)
	return builtin
}

// taint marks the variables an assignment or a range defines from a tainted value.
func (vc *validatecall) taint(fl *flow, targets ...ast.Expr) {
	for _, t := range targets {
		id, ok := t.(*ast.Ident)
		if !ok || id.Name == "_" {
			continue
		}
		if obj := vc.pass.TypesInfo.ObjectOf(id); obj != nil {
			fl.tainted[obj] = true
		}
	}
}

func (vc *validatecall) mentionsAny(fl *flow, exprs []ast.Expr) bool {
	for _, e := range exprs {
		if vc.mentions(fl, e) {
			return true
		}
	}
	return false
}

// mentions reports whether e refers to a tainted variable.
func (vc *validatecall) mentions(fl *flow, e ast.Expr) bool {
	if e == nil {
		return false
	}
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && fl.tainted[vc.pass.TypesInfo.Uses[id]] {
			found = true
		}
		return !found
	})
	return found
}

// insideAny reports whether n lies within one of the nodes.
func insideAny(n ast.Node, nodes []ast.Node) bool {
	for _, outer := range nodes {
		if outer.Pos() <= n.Pos() && n.End() <= outer.End() {
			return true
		}
	}
	return false
}
