package regression

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestDiff(t *testing.T) {
	a := Readout{"led": "true", "igmp": "on", "gone": "x"}
	b := Readout{"led": "false", "igmp": "on", "new": "y"}
	want := []string{
		`gone: before "x", after ""`,
		`led: before "true", after "false"`,
		`new: before "", after "y"`,
	}
	if got := Diff(a, b); !reflect.DeepEqual(got, want) {
		t.Errorf("Diff = %q, want %q", got, want)
	}
	if got := Diff(a, a); len(got) != 0 {
		t.Errorf("identical readouts differ: %q", got)
	}
}

func TestTake(t *testing.T) {
	ok := Reader{"a", func(context.Context) (string, error) { return "1", nil }}
	got, err := Take(context.Background(), []Reader{ok})
	if err != nil || !reflect.DeepEqual(got, Readout{"a": "1"}) {
		t.Errorf("Take = %v, %v", got, err)
	}
	boom := errors.New("boom")
	_, err = Take(context.Background(), []Reader{ok, {"b", func(context.Context) (string, error) { return "", boom }}})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want it to wrap boom and name the reader", err)
	}
}
