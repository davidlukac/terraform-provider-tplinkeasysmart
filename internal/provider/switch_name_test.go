package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSwitchNameOfTreatsNullUnknownAndEmptyAsTheDefault(t *testing.T) {
	for _, v := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue("")} {
		if got := switchNameOf(v); got != "" {
			t.Errorf("switchNameOf(%v) = %q, want the default switch", v, got)
		}
	}
	if switchNameOf(types.StringValue("sw02")) != "sw02" {
		t.Error("a name is the name")
	}
}

func TestSwitchNameValidate(t *testing.T) {
	for _, ok := range []switchName{"a", "sw01", "Sw_01-b", "0lab", switchName(strings.Repeat("x", maxSwitchNameLen))} {
		if err := ok.validate(); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []switchName{"", "-sw", "_sw", "sw 01", "sw:01", "sw.01", "sw/01", "é", switchName(strings.Repeat("x", maxSwitchNameLen+1))} {
		if bad.validate() == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// What qualify makes, splitQualified takes apart again.
func TestQualifyAndSplitAreInverse(t *testing.T) {
	for _, tc := range []struct {
		n    switchName
		id   string
		want string
	}{{"", "led", "led"}, {"sw02", "led", "sw02:led"}, {"sw02", "7", "sw02:7"}} {
		q := tc.n.qualify(tc.id)
		if q != tc.want {
			t.Errorf("%q.qualify(%q) = %q, want %q", tc.n, tc.id, q, tc.want)
		}
		if n, id := splitQualified(q); n != tc.n || id != tc.id {
			t.Errorf("splitQualified(%q) = %q, %q; want %q, %q", q, n, id, tc.n, tc.id)
		}
	}
}

func TestSwitchNameValidatorSkipsWhatIsNotKnownAndNamesTheAttribute(t *testing.T) {
	v := validSwitchName()[0]
	for _, val := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue("sw01")} {
		var resp validator.StringResponse
		v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: val}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("%v must pass: %v", val, resp.Diagnostics)
		}
	}
	var resp validator.StringResponse
	v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue("a:b")}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("a name with a separator must be refused")
	}
	if d := v.Description(context.Background()); d == "" || v.MarkdownDescription(context.Background()) != d {
		t.Errorf("descriptions: %q / %q", d, v.MarkdownDescription(context.Background()))
	}
}
