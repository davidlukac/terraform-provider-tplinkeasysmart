package provider

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The schema is the provider's contract with every configuration written against it: a flipped
// default, an attribute that turns from optional to required, a lost RequiresReplace or a validator
// that gets stricter breaks configurations that planned clean the day before, and none of it shows in
// the Go diff of a resource in a way a reviewer is sure to notice. The snapshot below writes down every
// resource, data source and provider attribute with what decides how it behaves (kind, required,
// optional, computed, sensitive, default, plan modifiers, validators, deprecation, nesting), without
// the descriptions (the generated documentation shows those), so the change is a line of the diff of the
// pull request that makes it, and a change that is not meant fails the test.
//
// It does not see what happens inside ModifyPlan or ValidateConfig: those are pinned by their own tests.

// updateSchemaSnapshot rewrites testdata/schema_snapshot.txt from the schema as it is now.
var updateSchemaSnapshot = flag.Bool("update-schema-snapshot", false, "rewrite internal/provider/testdata/schema_snapshot.txt from the current schema")

const (
	schemaSnapshotPath = "testdata/schema_snapshot.txt"
	// updateSnapshotHint is the command that regenerates the file; the failure message and the header of
	// the file name it.
	updateSnapshotHint = "go test ./internal/provider -run TestSchemaSnapshotIsCurrent -update-schema-snapshot"
	// snapshotContextLines is how many unchanged lines the failure message shows around a change.
	snapshotContextLines = 2
	// snapshotIndent is one level of nesting in the file.
	snapshotIndent = "  "
)

// describer is what defaults, plan modifiers and validators of every attribute type have in common.
type describer interface {
	Description(ctx context.Context) string
}

// snapshotWriter writes the lines of the snapshot with their nesting.
type snapshotWriter struct {
	b strings.Builder
}

func (w *snapshotWriter) line(depth int, format string, args ...any) {
	w.b.WriteString(strings.Repeat(snapshotIndent, depth))
	fmt.Fprintf(&w.b, format, args...)
	w.b.WriteByte('\n')
}

// field returns the named field of a struct value when the struct has it and it is set.
func field(v reflect.Value, name string) (reflect.Value, bool) {
	f := v.FieldByName(name)
	if !f.IsValid() || f.IsZero() {
		return reflect.Value{}, false
	}
	return f, true
}

// flags lists, in a fixed order, the boolean fields of an attribute that are on.
func flags(v reflect.Value) string {
	var on []string
	for _, name := range []string{"Required", "Optional", "Computed", "Sensitive", "WriteOnly"} {
		if f, ok := field(v, name); ok && f.Kind() == reflect.Bool {
			on = append(on, strings.ToLower(name))
		}
	}
	if len(on) == 0 {
		return "none"
	}
	return strings.Join(on, " ")
}

// kind is the name of the attribute's type without its package: StringAttribute, ListNestedBlock.
func kind(v reflect.Value) string {
	return v.Type().Name()
}

// attribute writes one attribute or block, and what is inside it.
func (w *snapshotWriter) attribute(depth int, name string, v reflect.Value) {
	w.line(depth, "%s: %s [%s]", name, kind(v), flags(v))
	w.details(depth+1, "", v)
}

// details writes the fields of an attribute, a block or a nested object that decide its behavior;
// label prefixes them for a nested object ("object validators").
func (w *snapshotWriter) details(depth int, label string, v reflect.Value) {
	if f, ok := field(v, "ElementType"); ok {
		w.line(depth, "%selement type: %v", label, f.Interface())
	}
	if f, ok := field(v, "AttributeTypes"); ok {
		w.line(depth, "%sattribute types: %v", label, f.Interface()) // fmt prints a map with sorted keys
	}
	if f, ok := field(v, "CustomType"); ok {
		w.line(depth, "%scustom type: %T", label, f.Interface())
	}
	if f, ok := field(v, "DeprecationMessage"); ok {
		w.line(depth, "%sdeprecated: %s", label, f.String())
	}
	if f, ok := field(v, "Default"); ok {
		w.line(depth, "%sdefault: %s", label, w.describe(f))
	}
	w.list(depth, label+"plan modifiers", v, "PlanModifiers")
	w.list(depth, label+"validators", v, "Validators")
	if f, ok := field(v, "NestedObject"); ok {
		w.details(depth, "object ", f) // also writes the attributes inside it
		return
	}
	w.children(depth, v)
}

// list writes the plan modifiers or validators of v, in the order the framework runs them.
func (w *snapshotWriter) list(depth int, label string, v reflect.Value, name string) {
	f, ok := field(v, name)
	if !ok {
		return
	}
	for i := range f.Len() {
		w.line(depth, "%s: %s", label, w.describe(f.Index(i)))
	}
}

// describe names a default, a plan modifier or a validator by its type and what it says it does (the
// description carries its parameters: the bounds of a range, the value of a default).
func (w *snapshotWriter) describe(v reflect.Value) string {
	x := v.Interface()
	if d, ok := x.(describer); ok {
		return fmt.Sprintf("%T: %s", x, d.Description(context.Background()))
	}
	return fmt.Sprintf("%T", x)
}

// children writes the attributes and blocks that are inside v (a schema, a single nested attribute or a
// nested object), sorted by name.
func (w *snapshotWriter) children(depth int, v reflect.Value) {
	for _, group := range []string{"Attributes", "Blocks"} {
		f, ok := field(v, group)
		if !ok || f.Kind() != reflect.Map {
			continue
		}
		keys := f.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		for _, k := range keys {
			w.attribute(depth, k.String(), reflect.ValueOf(f.MapIndex(k).Interface()))
		}
	}
}

// schemaSnapshot is the whole file for the provider as it is now.
func schemaSnapshot(ctx context.Context) string {
	w := &snapshotWriter{}
	w.b.WriteString("# The shape of the provider's schema (kind, required/optional/computed/sensitive, defaults, plan\n")
	w.b.WriteString("# modifiers, validators, deprecation), not its descriptions. A change here is a change of the contract:\n")
	w.b.WriteString("# review it, and regenerate with\n")
	w.b.WriteString("#   " + updateSnapshotHint + "\n")
	p := &tplinkProvider{}

	var ps provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &ps)
	w.line(0, "\nprovider")
	w.children(1, reflect.ValueOf(ps.Schema))

	var blocks []string
	for _, newResource := range p.Resources(ctx) {
		r := newResource()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "tplinkeasysmart"}, &meta)
		var s resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &s)
		sub := &snapshotWriter{}
		sub.line(0, "\nresource %s (schema version %d)", meta.TypeName, s.Schema.Version)
		sub.details(1, "schema ", reflect.ValueOf(s.Schema))
		blocks = append(blocks, sub.b.String())
	}
	for _, newSource := range p.DataSources(ctx) {
		d := newSource()
		var meta datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "tplinkeasysmart"}, &meta)
		var s datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &s)
		sub := &snapshotWriter{}
		sub.line(0, "\ndata %s", meta.TypeName)
		sub.details(1, "schema ", reflect.ValueOf(s.Schema))
		blocks = append(blocks, sub.b.String())
	}
	slices.Sort(blocks) // each block starts with a newline and its kind: the data sources, then the resources, by name
	w.b.WriteString(strings.Join(blocks, ""))
	return w.b.String()
}

// lineDiff is a unified-style difference of two texts by their lines: what a reviewer would see in the
// pull request, with snapshotContextLines unchanged lines around each change.
func lineDiff(want, got string) string {
	a, b := strings.Split(want, "\n"), strings.Split(got, "\n")
	// longest common subsequence table, from the end
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i, v := range slices.Backward(a) {
		for j := len(b) - 1; j >= 0; j-- {
			if v == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []string
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			ops = append(ops, "  "+a[i])
			i++
			j++
		case j < len(b) && (i == len(a) || lcs[i][j+1] > lcs[i+1][j]):
			ops = append(ops, "+ "+b[j])
			j++
		default:
			ops = append(ops, "- "+a[i])
			i++
		}
	}
	return withContext(ops)
}

// withContext keeps the changed lines of ops and snapshotContextLines around each, marks what it leaves
// out with "...", and names the section (provider, resource or data source) of each group of changes.
func withContext(ops []string) string {
	sections := make([]string, len(ops))
	section := ""
	keep := make([]bool, len(ops))
	for k, op := range ops {
		if text := op[2:]; text != "" && text[0] != ' ' && text[0] != '#' {
			section = text
		}
		sections[k] = section
		if op[0] == ' ' {
			continue
		}
		for c := max(0, k-snapshotContextLines); c <= min(len(ops)-1, k+snapshotContextLines); c++ {
			keep[c] = true
		}
	}
	var out []string
	for k, op := range ops {
		switch {
		case keep[k] && (k == 0 || !keep[k-1]):
			out = append(out, "@@ "+sections[k]+" @@", op)
		case keep[k]:
			out = append(out, op)
		case len(out) > 0 && out[len(out)-1] != "  ...":
			out = append(out, "  ...")
		}
	}
	return strings.Join(out, "\n")
}

// TestSchemaSnapshotIsCurrent fails when the schema differs from testdata/schema_snapshot.txt. A change
// of the schema that is meant is committed together with the new snapshot (the diff of the file is what a
// reviewer reads); an unmeant one (a default that flipped, an attribute that turned required) is caught
// here.
func TestSchemaSnapshotIsCurrent(t *testing.T) {
	got := schemaSnapshot(context.Background())
	if *updateSchemaSnapshot {
		if err := os.MkdirAll(filepath.Dir(schemaSnapshotPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(schemaSnapshotPath, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(schemaSnapshotPath)
	if err != nil {
		t.Fatalf("cannot read the schema snapshot (%v); create it with\n  %s", err, updateSnapshotHint)
	}
	if want := string(data); want != got {
		t.Fatalf("the schema differs from %s (- the file, + the schema now):\n%s\n\n"+
			"A schema change that is meant is committed with the new snapshot: %s",
			schemaSnapshotPath, lineDiff(want, got), updateSnapshotHint)
	}
}

// What the snapshot promises to show, pinned on a schema of its own so that a framework upgrade that
// adds a field it does not know about, or a change of this writer, cannot silently drop a line.
func TestSchemaSnapshotWritesWhatDecidesBehavior(t *testing.T) {
	s := schema.Schema{
		Version:            2,
		DeprecationMessage: "use another resource",
		Attributes: map[string]schema.Attribute{
			"b_flag": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				PlanModifiers: []planmodifier.Bool{},
			},
			"a_name": schema.StringAttribute{
				Required: true, Sensitive: true, DeprecationMessage: "renamed",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.LengthAtLeast(3)},
			},
			"c_list": schema.ListNestedAttribute{
				Optional: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"port": schema.Int64Attribute{Required: true},
						"tags": schema.SetAttribute{Optional: true, ElementType: types.StringType},
					},
				},
			},
		},
	}
	w := &snapshotWriter{}
	w.details(0, "schema ", reflect.ValueOf(s))
	want := strings.Join([]string{
		"schema deprecated: use another resource",
		"a_name: StringAttribute [required sensitive]",
		"  deprecated: renamed",
		"  plan modifiers: stringplanmodifier.requiresReplaceIfModifier: If the value of this attribute changes, Terraform will destroy and recreate the resource.",
		"  validators: stringvalidator.lengthAtLeastValidator: string length must be at least 3",
		"b_flag: BoolAttribute [optional computed]",
		"  default: booldefault.staticBoolDefault: value defaults to true",
		"c_list: ListNestedAttribute [optional]",
		"  port: Int64Attribute [required]",
		"  tags: SetAttribute [optional]",
		"    element type: basetypes.StringType",
		"",
	}, "\n")
	if got := w.b.String(); got != want {
		t.Errorf("snapshot writer output:\n%s\nwant:\n%s", got, want)
	}
}

func TestLineDiffShowsOnlyTheChangeWithContext(t *testing.T) {
	tests := []struct {
		name, want, got, diff string
	}{
		{"equal text has no difference", "s\n  a\n  b", "s\n  a\n  b", ""},
		{"a changed line, with its section and context", "s\n  a\n  b\n  c\n  d\n  e\n  f\n  g", "s\n  a\n  b\n  X\n  d\n  e\n  f\n  g",
			"@@ s @@\n    a\n    b\n-   c\n+   X\n    d\n    e\n  ..."},
		{"an added line", "s\n  a\n  b", "s\n  a\n  new\n  b", "@@ s @@\n  s\n    a\n+   new\n    b"},
		{"a removed line", "s\n  a\n  old\n  b", "s\n  a\n  b", "@@ s @@\n  s\n    a\n-   old\n    b"},
		{"the section is the last heading before the change", "one\n  a\ntwo\n  b\n  c\n  d\n  e", "one\n  a\ntwo\n  b\n  c\n  d\n  E",
			"@@ two @@\n    c\n    d\n-   e\n+   E"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := lineDiff(tc.want, tc.got); got != tc.diff {
				t.Errorf("lineDiff = %q, want %q", got, tc.diff)
			}
		})
	}
}

// The snapshot must not be vacuous: it names every resource and data source the provider registers, and
// the attributes that matter most to a configuration.
func TestSchemaSnapshotCoversEveryResourceAndDataSource(t *testing.T) {
	ctx := context.Background()
	got := schemaSnapshot(ctx)
	p := &tplinkProvider{}
	for _, newResource := range p.Resources(ctx) {
		r := newResource()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "tplinkeasysmart"}, &meta)
		t.Run("resource "+meta.TypeName, func(t *testing.T) {
			if !strings.Contains(got, "\nresource "+meta.TypeName+" ") {
				t.Errorf("the snapshot has no section for resource %s", meta.TypeName)
			}
		})
	}
	for _, newSource := range p.DataSources(ctx) {
		d := newSource()
		var meta datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "tplinkeasysmart"}, &meta)
		t.Run("data "+meta.TypeName, func(t *testing.T) {
			if !strings.Contains(got, "\ndata "+meta.TypeName+"\n") {
				t.Errorf("the snapshot has no section for data source %s", meta.TypeName)
			}
		})
	}
	for _, needle := range []string{"allow_disruptive: BoolAttribute [optional computed]", "default: ", "plan modifiers: ", "validators: ", "[required sensitive]"} {
		t.Run(needle, func(t *testing.T) {
			if !strings.Contains(got, needle) {
				t.Errorf("the snapshot never says %q: the writer has stopped seeing it", needle)
			}
		})
	}
}
