package escp

import (
	"errors"
	"reflect"
	"testing"
)

func TestPortIDValidate(t *testing.T) {
	for _, p := range []PortID{1, 8, MaxPort} {
		if err := p.Validate(); err != nil {
			t.Errorf("port %d: %v", p, err)
		}
	}
	for _, p := range []PortID{-1, 0, MaxPort + 1, 255} {
		if err := p.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("port %d: got %v, want ErrInvalid", p, err)
		}
	}
	if PortID(7).String() != "7" {
		t.Error("a port prints as its number")
	}
}

func TestNewPortSetRefusesAPortOutsideTheMask(t *testing.T) {
	for _, bad := range [][]PortID{{0}, {33}, {1, 2, 40}, {-3}} {
		if s, err := NewPortSet(bad...); !errors.Is(err, ErrInvalid) || s != 0 {
			t.Errorf("NewPortSet(%v) = %v, %v; want the empty set and ErrInvalid", bad, s, err)
		}
	}
	// Repeating a port is not an error and does not make it twice.
	if s, err := NewPortSet(2, 2, 5, 2); err != nil || s.Len() != 2 || s.String() != "2,5" {
		t.Fatalf("NewPortSet(2,2,5,2) = %v, %v", s, err)
	}
}

func TestMustPortSetPanicsOnAnInvalidPort(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustPortSet(0) must panic")
		}
	}()
	MustPortSet(0)
}

// The set is the wire bitmap: bit n-1 is port n, big endian.
func TestPortSetIsTheWireMask(t *testing.T) {
	s := MustPortSet(1, 3, 32)
	if got, want := s.Mask(), []byte{0x80, 0, 0, 0x05}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mask % x, want % x", got, want)
	}
	if back := PortSetFromMask(s.Mask()); back != s {
		t.Fatalf("round trip gave %v", back)
	}
	if PortSetFromMask([]byte{1, 2, 3}) != 0 || PortSetFromMask(nil) != 0 {
		t.Fatal("a mask shorter than four bytes is the empty set")
	}
	if PortSetFromMask([]byte{0, 0, 0, 0x0c, 0xff}) != MustPortSet(3, 4) {
		t.Fatal("bytes after the first four are ignored")
	}
}

func TestPortSetOperations(t *testing.T) {
	a, b := MustPortSet(1, 2, 3), MustPortSet(3, 4)
	cases := []struct {
		name      string
		got, want PortSet
	}{
		{"union", a.Union(b), MustPortSet(1, 2, 3, 4)},
		{"intersect", a.Intersect(b), MustPortSet(3)},
		{"minus", a.Minus(b), MustPortSet(1, 2)},
		{"minus itself", a.Minus(a), 0},
		{"union with the empty set", a.Union(0), a},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if !a.Has(2) || a.Has(4) || a.Has(0) || a.Has(33) || a.Has(-1) {
		t.Error("Has must be true only for members and false, not a panic, for ports outside the mask")
	}
	if a.Len() != 3 || a.IsEmpty() || !PortSet(0).IsEmpty() {
		t.Error("Len and IsEmpty")
	}
}

func TestPortSetListsAndPrints(t *testing.T) {
	s := MustPortSet(8, 2, 5)
	if !reflect.DeepEqual(s.Ports(), []PortID{2, 5, 8}) || !reflect.DeepEqual(s.Ints(), []int{2, 5, 8}) {
		t.Fatalf("ascending lists: %v %v", s.Ports(), s.Ints())
	}
	if s.String() != "2,5,8" || PortSet(0).String() != "none" {
		t.Fatalf("String: %q %q", s.String(), PortSet(0).String())
	}
	if got := PortSet(0).Ports(); got == nil || len(got) != 0 {
		t.Fatalf("the empty set lists as a non-nil empty slice, got %#v", got)
	}
}
