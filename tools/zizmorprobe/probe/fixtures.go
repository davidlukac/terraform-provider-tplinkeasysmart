package probe

// The paths and texts the fixtures use.
const (
	// findingFile is the workflow that carries the deliberate finding, and findingRule the rule zizmor names for it.
	findingFile = ".github/workflows/zz-probe.yml"
	findingRule = "template-injection"
	// findingStep is the step with the template injection, the text an inline suppression is added after.
	findingStep = `echo "${{ github.head_ref }}"`
	// setupUse is how the workflows use the local setup action: the allowed findings are those uses.
	setupUse = "uses: ./.github/actions/setup"
	// fuzzWorkflow is a workflow that has one of those uses.
	fuzzWorkflow = ".github/workflows/fuzz.yml"
	// suppression is an inline suppression for the finding.
	suppression = " # zizmor: ignore[" + findingRule + "] probe"

	// findingWorkflow is a workflow with a template injection, the finding of the probes.
	findingWorkflow = `name: Probe
on: pull_request
permissions: {}
jobs:
  probe:
    runs-on: ubuntu-24.04
    permissions: {}
    steps:
      - run: ` + findingStep + "\n"

	// cleanWorkflow is a workflow without a finding, for a link whose target is fine in itself.
	cleanWorkflow = `name: Clean
on: pull_request
permissions: {}
jobs:
  clean:
    runs-on: ubuntu-24.04
    permissions: {}
    steps:
      - run: echo clean
`

	// extraWorkflow is a workflow that uses the local setup action, one allowed finding more.
	extraWorkflow = `name: Extra
on: pull_request
permissions: {}
jobs:
  extra:
    runs-on: ubuntu-24.04
    permissions: {}
    steps:
      - ` + setupUse + "\n"

	// injectedAction is a composite action with the same template injection.
	injectedAction = `name: Probe
description: Probe
runs:
  using: composite
  steps:
    - shell: bash
      run: ` + findingStep + "\n"

	// disableRule is a configuration file that turns the rule of the finding off.
	disableRule = "rules:\n  " + findingRule + ":\n    disable: true\n"
	// ignoredFile is an ignore file that names the workflow of the finding.
	ignoredFile = "zz-probe.yml\n"
	// byteOrderMark is the UTF-8 byte order mark.
	byteOrderMark = "\xef\xbb\xbf"

	// shadowModule and shadowMain are a Go module of the repository's path with a program in the place of the
	// program that runs zizmor, which accepts everything.
	shadowModule = "module github.com/davidlukac/terraform-provider-tplinkeasysmart\n\ngo 1.22\n"
	shadowMain   = "package main\n\nfunc main() {}\n"
)

// fixture changes a scratch tree.
type fixture func(tree) error

// all is the fixtures one after the other, up to the first that fails.
func all(fixtures ...fixture) fixture {
	return func(t tree) error {
		for _, f := range fixtures {
			if err := f(t); err != nil {
				return err
			}
		}
		return nil
	}
}

// addFinding adds the workflow with the deliberate finding.
func addFinding(t tree) error { return t.write(findingFile, findingWorkflow) }

// configFile adds a configuration file under the name that turns the rule of the finding off.
func configFile(name string) fixture {
	return func(t tree) error { return t.write(name, disableRule) }
}

// ignoreFile adds an ignore file under the name that names the workflow of the finding.
func ignoreFile(name string) fixture {
	return func(t tree) error { return t.write(name, ignoredFile) }
}

// breakWithBOM puts a byte order mark in front of the workflow of the finding, which zizmor cannot load.
func breakWithBOM(t tree) error {
	content, err := t.read(findingFile)
	if err != nil {
		return err
	}
	return t.write(findingFile, byteOrderMark+content)
}

// breakWithSecondDocument adds a second YAML document to the workflow of the finding.
func breakWithSecondDocument(t tree) error {
	content, err := t.read(findingFile)
	if err != nil {
		return err
	}
	return t.write(findingFile, content+"---\n"+content)
}

// breakWithDuplicateKey repeats a top-level key in the workflow of the finding.
func breakWithDuplicateKey(t tree) error {
	content, err := t.read(findingFile)
	if err != nil {
		return err
	}
	return t.write(findingFile, content+"permissions: {}\n")
}

// breakWithSchemaError gives the runner of the workflow of the finding a number, which the schema refuses.
func breakWithSchemaError(t tree) error { return t.replace(findingFile, "ubuntu-24.04", "5") }

// suppressIn adds an inline suppression for the finding to a file.
func suppressIn(rel string) fixture {
	return func(t tree) error { return t.replace(rel, findingStep, findingStep+suppression) }
}

// removeOneSetupUse removes the uses of the setup action from one workflow (it has one).
func removeOneSetupUse(t tree) error { return t.dropLines(fuzzWorkflow, setupUse) }

// addOneSetupUse adds a workflow with a use of the setup action.
func addOneSetupUse(t tree) error { return t.write(".github/workflows/zz-extra.yml", extraWorkflow) }

// nameAnotherRuleOnSetupUses changes the rule named in the ignore comments of one workflow's setup uses.
func nameAnotherRuleOnSetupUses(t tree) error {
	return t.replace(fuzzWorkflow, "ignore[self-repository]", "ignore["+findingRule+"]")
}

// linkAWorkflow links a workflow with the finding, and an ignore comment for it, from outside .github/.
func linkAWorkflow(t tree) error {
	return all(
		func(t tree) error { return t.mkdir("elsewhere") },
		addFinding,
		func(t tree) error { return t.move(findingFile, "elsewhere/zz.yml") },
		suppressIn("elsewhere/zz.yml"),
		func(t tree) error { return t.symlink(".github/workflows/zz.yml", "../../elsewhere/zz.yml") },
	)(t)
}

// linkACleanWorkflow links a workflow without a finding from outside .github/.
func linkACleanWorkflow(t tree) error {
	return all(
		func(t tree) error { return t.write("elsewhere/clean.yml", cleanWorkflow) },
		func(t tree) error { return t.symlink(".github/workflows/clean.yml", "../../elsewhere/clean.yml") },
	)(t)
}

// linkAnAction links the file of a composite action with the finding from outside .github/.
func linkAnAction(t tree) error {
	return all(
		func(t tree) error { return t.write("elsewhere/action.yml", injectedAction) },
		func(t tree) error { return t.mkdir(".github/actions/linked") },
		func(t tree) error {
			return t.symlink(".github/actions/linked/action.yml", "../../../elsewhere/action.yml")
		},
	)(t)
}

// addActionOutsideGithub adds a composite action with the finding outside .github/, which a workflow can use
// with `uses: ./ci`.
func addActionOutsideGithub(t tree) error { return t.write("ci/action.yml", injectedAction) }

// linkADirectory links the directory of that action from .github/actions/.
func linkADirectory(t tree) error {
	return all(
		addActionOutsideGithub,
		func(t tree) error { return t.symlink(".github/actions/linked", "../../ci") },
	)(t)
}

// addHostileFileName adds a workflow without a finding whose file name has a new line and, after it, what a
// job log reads as a workflow command: zizmor writes the name of every file it audits on its standard error.
func addHostileFileName(t tree) error {
	return t.write(".github/workflows/a\n::warning::x.yml", cleanWorkflow)
}

// shadowTheRunner puts a copy of the program that runs zizmor and accepts everything at the root of the tree.
func shadowTheRunner(t tree) error {
	return all(
		func(t tree) error { return t.write("go.mod", shadowModule) },
		func(t tree) error { return t.write("tools/zizmorrun/main.go", shadowMain) },
	)(t)
}
