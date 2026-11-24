package regression

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// target_needs.go states how many test and protected ports the scenarios address (MinTestPorts,
// MinProtectedPorts), and the sample file is tested against that. The scenarios, though, index the target
// directly (TestPorts[3], prot[2]), so a scenario that starts to use a fifth test port would leave
// MinTestPorts and the sample test green. This test closes the gap: it reads the package's own sources
// and holds every index of a target's port list to the stated need.

// portNeed is the stated minimum of one list of ports of a Target: an index i is within the need when i <
// min, and the constant that states it is what to raise otherwise.
type portNeed struct {
	min       int
	constName string
}

// portNeeds are the lists of ports of a Target that the scenarios index, by field name.
var portNeeds = map[string]portNeed{
	"TestPorts":      {MinTestPorts, "MinTestPorts"},
	"ProtectedPorts": {MinProtectedPorts, "MinProtectedPorts"},
}

// portIndexUse is one index expression on a list of ports of a Target.
type portIndexUse struct {
	file  string
	line  int
	field string // TestPorts or ProtectedPorts
	text  string // the index as written
	index int    // its value, when constant is true
	// constant is false for anything that is not an integer literal (a variable, a named constant, an
	// expression): the check cannot know its value, so it reports it.
	constant bool
}

// problem returns what is wrong with the use, or "" when it is within the stated need.
func (u portIndexUse) problem() string {
	need := portNeeds[u.field]
	switch {
	case !u.constant:
		return fmt.Sprintf("%s:%d: %s[%s] has an index that is not an integer literal, so the check cannot tell whether it is below %s (%d); "+
			"write the index as a literal, and it is checked against %s in target_needs.go",
			u.file, u.line, u.field, u.text, need.constName, need.min, need.constName)
	case u.index >= need.min:
		return fmt.Sprintf("%s:%d: %s[%d] needs %d ports, and %s is %d: raise %s to %d in target_needs.go, and give the sample file "+
			"and the target of the fake the ports", u.file, u.line, u.field, u.index, u.index+1, need.constName, need.min, need.constName, u.index+1)
	}
	return ""
}

// portIndexUses parses one Go source and returns every index expression on a selector named TestPorts or
// ProtectedPorts, and on a local variable that was assigned such a selector in the same declaration
// (prot := s.Target.ProtectedPorts; prot[2]). Nothing here knows the type of the selector's receiver
// (that would need the type checker), so a field of that name on another type counts too; there is none in
// the package, and a false report names the line.
func portIndexUses(filename, src string) ([]portIndexUse, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var uses []portIndexUse
	for _, decl := range file.Decls {
		aliases := portAliases(decl)
		ast.Inspect(decl, func(n ast.Node) bool {
			ix, ok := n.(*ast.IndexExpr)
			if !ok {
				return true
			}
			field := portField(ix.X, aliases)
			if field == "" {
				return true
			}
			use := portIndexUse{file: filepath.Base(filename), line: fset.Position(ix.Pos()).Line, field: field}
			use.index, use.constant, use.text = literalIndex(ix.Index)
			uses = append(uses, use)
			return true
		})
	}
	return uses, nil
}

// portField is the list of ports that the expression names, "" when it names none: a selector with the
// field's name, or a local variable that holds one.
func portField(e ast.Expr, aliases map[string]string) string {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return portField(x.X, aliases)
	case *ast.SelectorExpr:
		if _, ok := portNeeds[x.Sel.Name]; ok {
			return x.Sel.Name
		}
	case *ast.Ident:
		return aliases[x.Name]
	}
	return ""
}

// portAliases are the local variables of one declaration that hold a list of ports, by the name of the
// field, found by one pass over its assignments and variable declarations, in the order of the source (so
// an alias of an alias is found too). A name that is later given another value stays an alias: the check
// may report too much, never too little.
func portAliases(decl ast.Decl) map[string]string {
	aliases := map[string]string{}
	bind := func(names []ast.Expr, values []ast.Expr) {
		if len(names) != len(values) {
			return
		}
		for i, v := range values {
			if id, ok := names[i].(*ast.Ident); ok {
				if field := portField(v, aliases); field != "" {
					aliases[id.Name] = field
				}
			}
		}
	}
	ast.Inspect(decl, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			bind(s.Lhs, s.Rhs)
		case *ast.ValueSpec:
			names := make([]ast.Expr, len(s.Names))
			for i, id := range s.Names {
				names[i] = id
			}
			bind(names, s.Values)
		}
		return true
	})
	return aliases
}

// literalIndex reads an index expression: its value when it is an integer literal, and always its text.
func literalIndex(e ast.Expr) (value int, constant bool, text string) {
	if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.INT {
		if v, err := strconv.ParseInt(lit.Value, 0, 32); err == nil {
			return int(v), true, lit.Value
		}
		return 0, false, lit.Value
	}
	return 0, false, exprText(e)
}

// exprText is a short rendering of an index expression for a message.
func exprText(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.BinaryExpr:
		return exprText(x.X) + " " + x.Op.String() + " " + exprText(x.Y)
	case *ast.UnaryExpr:
		return x.Op.String() + exprText(x.X)
	case *ast.BasicLit:
		return x.Value
	case *ast.SelectorExpr:
		return exprText(x.X) + "." + x.Sel.Name
	}
	return "an expression"
}

func TestPortIndexUses(t *testing.T) {
	const field = "s.Target."
	for _, tc := range []struct {
		name string
		body string
		want []string // the problems, in order of the source; none when the indexes are within the needs
	}{
		{name: "an index inside the limit", body: "_ = " + field + "TestPorts[0]"},
		{name: "the last index inside the limit", body: "_ = " + field + "TestPorts[3]; _ = " + field + "ProtectedPorts[3]"},
		{name: "an index at the limit of the test ports", body: "_ = " + field + "TestPorts[4]",
			want: []string{"x.go:4: TestPorts[4] needs 5 ports, and MinTestPorts is 4: raise MinTestPorts to 5"}},
		{name: "an index at the limit of the protected ports", body: "_ = " + field + "ProtectedPorts[4]",
			want: []string{"x.go:4: ProtectedPorts[4] needs 5 ports, and MinProtectedPorts is 4: raise MinProtectedPorts to 5"}},
		{name: "an index far beyond the limit", body: "_ = " + field + "TestPorts[9]",
			want: []string{"TestPorts[9]", "raise MinTestPorts to 10"}},
		{name: "a hexadecimal literal", body: "_ = " + field + "TestPorts[0x4]", want: []string{"TestPorts[4]"}},
		{name: "a variable index", body: "i := 2\n\t_ = " + field + "TestPorts[i]",
			want: []string{"x.go:5: TestPorts[i] has an index that is not an integer literal", "MinTestPorts (4)"}},
		{name: "an expression index", body: "i := 2\n\t_ = " + field + "ProtectedPorts[i+1]",
			want: []string{"ProtectedPorts[i + 1] has an index that is not an integer literal", "MinProtectedPorts (4)"}},
		{name: "a named constant index", body: "_ = " + field + "TestPorts[MinTestPorts]",
			want: []string{"TestPorts[MinTestPorts] has an index that is not an integer literal"}},
		{name: "a negative index", body: "_ = " + field + "TestPorts[-1]", want: []string{"TestPorts[-1] has an index that is not an integer literal"}},
		{name: "a literal too large for an int", body: "_ = " + field + "TestPorts[99999999999]",
			want: []string{"TestPorts[99999999999] has an index that is not an integer literal"}},
		{name: "a slice is not an index", body: "_ = " + field + "TestPorts[:9]; _ = " + field + "ProtectedPorts[9:]"},
		{name: "a range over the list is not an index", body: "for _, p := range " + field + "TestPorts { _ = p }"},
		{name: "an alias of the protected ports", body: "prot := " + field + "ProtectedPorts\n\t_ = prot[2]\n\t_ = prot[4]",
			want: []string{"x.go:6: ProtectedPorts[4]"}},
		{name: "an alias of an alias", body: "a := " + field + "TestPorts\n\tb := a\n\t_ = b[5]", want: []string{"TestPorts[5]"}},
		{name: "a tuple assignment gives each name its list", body: "a, b := " + field + "TestPorts, " + field + "ProtectedPorts\n\t_, _ = a[3], b[4]",
			want: []string{"ProtectedPorts[4]"}},
		{name: "a variable declaration", body: "var a = " + field + "TestPorts\n\t_ = a[4]", want: []string{"TestPorts[4]"}},
		{name: "a parenthesized list", body: "_ = (" + field + "TestPorts)[4]", want: []string{"TestPorts[4]"}},
		{name: "an index inside a closure", body: "func() { _ = " + field + "TestPorts[4] }()", want: []string{"TestPorts[4]"}},
		{name: "an alias used inside a closure", body: "a := " + field + "ProtectedPorts\n\tfunc() { _ = a[4] }()", want: []string{"ProtectedPorts[4]"}},
		{name: "an alias made inside a closure", body: "func() {\n\t\ta := " + field + "TestPorts\n\t\t_ = a[4]\n\t}()", want: []string{"TestPorts[4]"}},
		{name: "a name that is no alias", body: "other := []int{1, 2}\n\t_ = other[7]"},
		{name: "a field of another name", body: "_ = " + field + "DisruptivePorts[7]"},
		{name: "a tuple of another size is not an alias", body: "a, b := f()\n\t_, _ = a[7], b[8]"},
		// the type of the receiver is not known (that would need the type checker): another type with a
		// field of the same name is reported like a Target's
		{name: "the same field name on another type is reported too", body: "var other struct{ TestPorts []int }\n\t_ = other.TestPorts[5]",
			want: []string{"TestPorts[5]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\n\nfunc f() {\n\t" + tc.body + "\n}\n"
			uses, err := portIndexUses("x.go", src)
			if err != nil {
				t.Fatalf("%v\n%s", err, src)
			}
			var got []string
			for _, u := range uses {
				if p := u.problem(); p != "" {
					got = append(got, p)
				}
			}
			joined := strings.Join(got, "\n")
			if len(tc.want) == 0 && len(got) != 0 {
				t.Errorf("reported %q, want nothing", joined)
			}
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("reported %q, want one that says %q", joined, w)
				}
			}
			if len(tc.want) != 0 && len(got) == 0 {
				t.Errorf("reported nothing, want %q", tc.want)
			}
		})
	}
}

func TestPortIndexUsesOfBrokenSource(t *testing.T) {
	if _, err := portIndexUses("x.go", "package p\nfunc ("); err == nil {
		t.Error("a source that does not parse was accepted")
	}
}

// TestScenarioIndexesStayWithinTheStatedNeeds holds the package's own sources to target_needs.go: no
// index of TestPorts or ProtectedPorts reaches past MinTestPorts or MinProtectedPorts.
func TestScenarioIndexesStayWithinTheStatedNeeds(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			uses, err := portIndexUses(name, string(src))
			if err != nil {
				t.Fatal(err)
			}
			for _, u := range uses {
				seen[u.field]++
				if p := u.problem(); p != "" {
					t.Error(p)
				}
			}
		})
	}
	// a scan that finds nothing would pass for the wrong reason (a moved directory, a renamed field)
	for _, name := range []string{"scenario_test.go", "target_needs.go"} {
		t.Run("scans "+name, func(t *testing.T) {
			if !slices.Contains(files, name) {
				t.Errorf("%s was not scanned (found %v)", name, files)
			}
		})
	}
	for field := range portNeeds {
		t.Run("finds an index of "+field, func(t *testing.T) {
			if seen[field] == 0 {
				t.Errorf("no index of %s found in the package: the scan would pass for the wrong reason", field)
			}
		})
	}
}
