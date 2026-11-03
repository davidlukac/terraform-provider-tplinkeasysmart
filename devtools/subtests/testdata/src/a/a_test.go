package a

import "testing"

var table = []int{1}

func load() []int { return nil }

func TestLiteralListReportedThroughT(t *testing.T) {
	for _, in := range []string{"a", "b"} { // want "TestLiteralListReportedThroughT ranges over its cases without t.Run"
		if in == "" {
			t.Errorf("empty")
		}
	}
}

func TestLocalVariableSetFromALiteral(t *testing.T) {
	cases := []int{1, 2}
	for _, in := range cases { // want "TestLocalVariableSetFromALiteral ranges over its cases"
		t.Log(in)
	}
}

func TestLocalVarDeclaration(t *testing.T) {
	var cases = map[string]int{"a": 1}
	for name := range cases { // want "TestLocalVarDeclaration ranges over its cases"
		t.Log(name)
	}
}

func TestPackageLevelList(t *testing.T) {
	for _, in := range table { // want "TestPackageLevelList ranges over its cases"
		t.Log(in)
	}
}

func TestListSetInAnotherFileOfThePackage(t *testing.T) {
	for _, in := range fromAnotherFile { // want "TestListSetInAnotherFileOfThePackage ranges over its cases"
		t.Log(in)
	}
}

func TestParenthesizedLiteral(t *testing.T) {
	for _, in := range []int{1} { // want "TestParenthesizedLiteral ranges over its cases"
		t.Log(in)
	}
}

func TestVariableWithoutALiteralIsNotAListOfCases(t *testing.T) {
	var cases []int
	cases = load()
	for _, in := range cases {
		t.Log(in)
	}
}

func TestACallResultIsNotWrittenOutInTheTest(t *testing.T) {
	for _, in := range load() {
		t.Log(in)
	}
}

func TestLoopWithRun(t *testing.T) {
	for _, in := range []string{"a"} {
		t.Run(in, func(t *testing.T) { t.Log(in) })
	}
}

func TestLoopThatNeverUsesT(t *testing.T) {
	n := 0
	for range []string{"a"} {
		n++
	}
	t.Log(n)
}

func TestLoopInAClosureWithItsOwnT(t *testing.T) {
	t.Run("x", func(sub *testing.T) {
		for _, in := range []string{"a"} { // want "TestLoopInAClosureWithItsOwnT ranges over its cases"
			sub.Log(in)
		}
	})
}

type runner struct{}

func (runner) Run(string, func()) {}

func TestARunThatIsNotTheTestingOne(t *testing.T) {
	var r runner
	for _, in := range []string{"a"} { // want "TestARunThatIsNotTheTestingOne ranges over its cases"
		t.Log(in)
		r.Run(in, func() {})
	}
}

func TestAShadowedListIsNotTheLiteralOne(t *testing.T) {
	cases := []int{1}
	_ = cases
	{
		cases := load()
		for _, in := range cases {
			t.Log(in)
		}
	}
}

func helper(t *testing.T) {
	for _, in := range []string{"a"} {
		t.Log(in)
	}
}

func ExampleHelper() {
	for range []string{"a"} {
	}
}

func FuzzSeeds(f *testing.F) {
	for _, in := range []string{"a"} {
		f.Add(in)
	}
}

func TestBodyless(t *testing.T)

func TestReasonAboveTheLoop(t *testing.T) {
	// subtests: the cases share one switch
	for _, in := range []string{"a"} {
		t.Log(in)
	}
}

func TestReasonOnTheLine(t *testing.T) {
	for _, in := range []string{"a"} { // subtests: one assertion over all
		t.Log(in)
	}
}

func TestCommentWithoutAReason(t *testing.T) {
	// subtests:
	for _, in := range []string{"a"} { // want "TestCommentWithoutAReason ranges over its cases"
		t.Log(in)
	}
}

func TestAnotherComment(t *testing.T) {
	// the cases
	for _, in := range []string{"a"} { // want "TestAnotherComment ranges over its cases"
		t.Log(in)
	}
}
