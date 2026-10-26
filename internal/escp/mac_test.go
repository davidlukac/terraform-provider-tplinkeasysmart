package escp

import (
	"errors"
	"testing"
)

func TestParseMAC(t *testing.T) {
	want := MAC{0xc0, 0x3a, 0x55, 0x6c, 0x81, 0x61}
	for _, s := range []string{"c0:3a:55:6c:81:61", "C0:3A:55:6C:81:61", "c0-3a-55-6c-81-61", "C0-3a-55-6C-81-61"} {
		got, err := ParseMAC(s)
		if err != nil || got != want {
			t.Errorf("ParseMAC(%q) = %v, %v; want %v", s, got, err, want)
		}
	}
	// Refused: not hex, too short, too long, mixed separators, and the forms net.ParseMAC also reads.
	for _, s := range []string{"", "zz:3a:55:6c:81:61", "c0:3a:55:6c:81", "c0:3a:55:6c:81:61:00:01", "c0:3a-55:6c:81:61", "c03a556c8161",
		"c03a.556c.8161", "02:00:5e:10:00:00:00:01", " c0:3a:55:6c:81:61", "c0:3a:55:6c:81:61\n"} {
		if got, err := ParseMAC(s); !errors.Is(err, ErrInvalid) || !got.IsZero() {
			t.Errorf("ParseMAC(%q) = %v, %v; want the zero MAC and ErrInvalid", s, got, err)
		}
	}
}

func TestMACStringAndZero(t *testing.T) {
	m := MAC{0xc0, 0x3a, 0x55, 0x6c, 0x81, 0x61}
	if m.String() != "c0:3a:55:6c:81:61" || m.IsZero() {
		t.Fatalf("%v zero=%t", m.String(), m.IsZero())
	}
	if (MAC{}).String() != "" || !(MAC{}).IsZero() {
		t.Fatal("the zero MAC is 'no MAC' and prints as nothing")
	}
	if round, err := ParseMAC(m.String()); err != nil || round != m {
		t.Fatalf("a MAC must survive String and ParseMAC: %v, %v", round, err)
	}
}

func TestMACFromBytes(t *testing.T) {
	if m, ok := MACFromBytes([]byte{1, 2, 3, 4, 5, 6}); !ok || m != (MAC{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("six bytes: %v, %t", m, ok)
	}
	for _, b := range [][]byte{nil, {1, 2, 3}, {1, 2, 3, 4, 5, 6, 7}} {
		if m, ok := MACFromBytes(b); ok || !m.IsZero() {
			t.Errorf("%d bytes accepted: %v", len(b), m)
		}
	}
}
