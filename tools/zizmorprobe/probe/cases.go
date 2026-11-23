package probe

// The words a refusal of the script says, which a probe looks for in its output: a run that fails for another
// reason does not pass as a refusal.
const (
	markerFindings = "zizmor findings"
	markerLoad     = "failed to load"
	markerLinks    = "symbolic links"
	markerNotList  = "not a list"
	markerBadCount = "not a plain non-negative integer"
	markerNoCount  = "set it to the number"
	markerNoMore   = "allowed exactly 0"
)

// want is what a run must do: pass, or fail and say marker.
type want struct {
	pass   bool
	marker string
}

// fails is the expectation of a refusal that says marker.
func fails(marker string) want { return want{marker: marker} }

// passes is the expectation of a run that passes.
var passes = want{pass: true}

// kind is the kind of an assertion on what the stub zizmor recorded.
type kind int

// The kinds of assertion.
const (
	hasArg kind = iota
	lacksArg
	hasEnv
	lacksEnv
)

// assertion is one statement about the recorded call: the stub got (or did not get) an argument as a whole
// line, or an environment entry as a whole line (hasEnv), or no variable of the name (lacksEnv).
type assertion struct {
	kind  kind
	value string
}

// probeCase is one run of scripts/zizmor.sh over a scratch repository.
type probeCase struct {
	name string
	// setup changes the scratch copy of .github/, in order.
	setup []fixture
	// stub: run with the stub zizmor first on the PATH and an otherwise empty environment, and the
	// variables in vars, instead of with the real zizmor. The stub answers answer (an empty list when
	// empty), the script is given count as the expected number of findings (0 when empty).
	stub   bool
	vars   []string
	answer string
	count  string
	// want is what the run must do.
	want want
	// recorded are the assertions on the call the stub recorded (stub runs that pass).
	recorded []assertion
	// noLineStartsWith are the prefixes that no line of the output may have, and outputHas the text the output
	// must hold: what the log of the run must look like whatever the tree holds.
	noLineStartsWith []string
	outputHas        []string
	// control: a failure ends the probes, because the ones after it prove nothing.
	control bool
}

// Names of the files that zizmor reads as configuration or as ignore files, and that the probes add.
var (
	configNames = []string{
		"zizmor.yml", "zizmor.yaml", ".zizmor.yml", ".zizmor.yaml",
		".github/zizmor.yml", ".github/zizmor.yaml", ".github/.zizmor.yml", ".github/.zizmor.yaml",
	}
	ignoreNames = []string{
		".gitignore", ".ignore", ".github/.gitignore", ".github/.ignore", ".github/workflows/.gitignore", ".github/workflows/.ignore",
	}
)

// auditVariables are the environment variables the script removes because they change what zizmor audits or
// where it sends the token; the stub is run with every one of them set, and none may reach it.
var auditVariables = []string{"ZIZMOR_OFFLINE", "ZIZMOR_NO_ONLINE_AUDITS", "ZIZMOR_CONFIG", "GH_HOST", "ZIZMOR_GITHUB_TOKEN"}

// hostile are those variables, set to values that would change the audit.
var hostile = []string{
	"ZIZMOR_OFFLINE=true", "ZIZMOR_NO_ONLINE_AUDITS=true", "ZIZMOR_CONFIG=/nonexistent", "GH_HOST=example.invalid", "ZIZMOR_GITHUB_TOKEN=z",
}

// fixedOptions are the assertions that hold for every run: the options the script always passes.
func fixedOptions() []assertion {
	var out []assertion
	for _, arg := range []string{"--no-config", "--collect=all", "--strict-collection", "--no-progress", "--no-ignores", "--no-exit-codes", "."} {
		out = append(out, assertion{hasArg, arg})
	}
	return append(out, assertion{hasArg, "json-v1"})
}

// noAuditVariable are the assertions that none of the variables that change the audit reached the stub.
func noAuditVariable() []assertion {
	out := make([]assertion, 0, len(auditVariables))
	for _, name := range auditVariables {
		out = append(out, assertion{lacksEnv, name})
	}
	return out
}

// join concatenates lists of assertions.
func join(lists ...[]assertion) []assertion {
	var out []assertion
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// scriptCases are the probes in the order they run: the two controls first, then one probe per input the
// audited tree can carry, then the stub runs.
func scriptCases() []probeCase {
	cases := []probeCase{
		{name: "control: the copy of .github/ alone is clean", want: passes, control: true},
		{name: "control: the finding alone is reported", setup: []fixture{addFinding}, want: fails(findingRule)},
	}
	for _, name := range configNames {
		cases = append(cases, probeCase{name: "configuration file " + name, setup: []fixture{addFinding, configFile(name)}, want: fails(findingRule)})
	}
	for _, name := range ignoreNames {
		cases = append(cases, probeCase{name: "ignore file " + name, setup: []fixture{addFinding, ignoreFile(name)}, want: fails(findingRule)})
	}
	for _, variant := range []struct {
		name string
		f    fixture
	}{
		{"bom", breakWithBOM}, {"second_document", breakWithSecondDocument},
		{"duplicate_key", breakWithDuplicateKey}, {"schema_error", breakWithSchemaError},
	} {
		cases = append(cases, probeCase{name: "workflow zizmor cannot load (" + variant.name + ")", setup: []fixture{addFinding, variant.f}, want: fails(markerLoad)})
	}
	return append(cases, treeCases()...)
}

// treeCases are the probes on links, actions, the number of allowed findings, and the program that runs zizmor.
func treeCases() []probeCase {
	return []probeCase{
		{name: "an extra inline suppression", setup: []fixture{addFinding, suppressIn(findingFile)}, want: fails(markerFindings)},
		{name: "a symbolic link to a workflow", setup: []fixture{linkAWorkflow}, want: fails(markerLinks)},
		{name: "a symbolic link to a workflow without a finding", setup: []fixture{linkACleanWorkflow}, want: fails(markerLinks)},
		{name: "a symbolic link to an action", setup: []fixture{linkAnAction}, want: fails(markerLinks)},
		{name: "a composite action outside .github/", setup: []fixture{addActionOutsideGithub}, want: fails(findingRule)},
		{name: "a composite action behind a linked directory", setup: []fixture{linkADirectory}, want: fails(markerLinks)},
		{name: "one allowed finding fewer", setup: []fixture{removeOneSetupUse}, want: fails(markerFindings)},
		{name: "one allowed finding more", setup: []fixture{addOneSetupUse}, want: fails(markerFindings)},
		{name: "one allowed finding fewer and a suppressed one more", setup: []fixture{removeOneSetupUse, addFinding, suppressIn(findingFile)}, want: fails(markerFindings)},
		{name: "the rule named in an ignore comment is not read: the findings are what counts", setup: []fixture{nameAnotherRuleOnSetupUses}, want: passes},
		{name: "a file name with a new line and a workflow command does not start a line of the log", setup: []fixture{addHostileFileName}, want: passes,
			noLineStartsWith: []string{"::"}, outputHas: []string{"zizmor: ::warning::x.yml"}},
		{name: "a copy of the program that runs zizmor in an audited tree other than the script's", setup: []fixture{addFinding, shadowTheRunner}, want: fails(findingRule)},
	}
}

// stubCases are the probes on the command line and the environment of the call, and on the refusal of an
// answer or an expectation that cannot be trusted. The stub answers an empty list of findings unless a case
// says otherwise, and the script is run with an expectation of none.
func stubCases() []probeCase {
	offline := []assertion{{hasArg, "--offline"}, {lacksEnv, "GH_TOKEN"}, {lacksEnv, "GITHUB_TOKEN"}}
	return []probeCase{
		{name: "no token: offline, the fixed options, no variable that changes the audit", stub: true, want: passes,
			recorded: join(fixedOptions(), noAuditVariable(), offline)},
		{name: "empty token variables count as no token", stub: true, vars: []string{"GH_TOKEN=", "GITHUB_TOKEN="}, want: passes,
			recorded: join(fixedOptions(), noAuditVariable(), offline)},
		{name: "GH_TOKEN: online, the fixed options, no variable that changes the audit", stub: true, vars: []string{"GH_TOKEN=first"}, want: passes,
			recorded: join(fixedOptions(), noAuditVariable(), []assertion{{lacksArg, "--offline"}, {hasEnv, "GH_TOKEN=first"}, {lacksEnv, "GITHUB_TOKEN"}})},
		{name: "GITHUB_TOKEN alone is passed on as GH_TOKEN", stub: true, vars: []string{"GITHUB_TOKEN=second"}, want: passes,
			recorded: join(fixedOptions(), noAuditVariable(), []assertion{{lacksArg, "--offline"}, {hasEnv, "GH_TOKEN=second"}, {lacksEnv, "GITHUB_TOKEN"}})},
		{name: "an answer that is an object and not a list is refused", stub: true, answer: "{}", want: fails(markerNotList)},
		{name: "an answer with a finding of another rule is refused", stub: true, answer: `[{"ident":"template-injection","desc":"d"}]`, want: fails(findingRule)},
		{name: "an answer with one allowed finding too many is refused", stub: true, answer: `[{"ident":"self-repository"}]`, want: fails(markerNoMore)},
		{name: "an expected count that is not a plain number is refused", stub: true, count: "1x", want: fails(markerBadCount)},
		{name: "an empty expected count is refused", stub: true, count: emptyCount, want: fails(markerNoCount)},
	}
}

// emptyCount stands for an expected count that is set and empty (an empty count field means 0).
const emptyCount = "<empty>"
