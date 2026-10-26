package escp

import (
	"bytes"
	"reflect"
	"testing"
)

func TestPortsFromMask(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []int
	}{
		{"too short is nothing", []byte{0xff, 0xff}, nil},
		{"empty is nothing", nil, nil},
		{"no bits", []byte{0, 0, 0, 0}, []int{}},
		{"lowest bit is port 1", []byte{0, 0, 0, 1}, []int{1}},
		{"highest bit is port 32", []byte{0x80, 0, 0, 0}, []int{32}},
		{"ports 1 to 8", []byte{0, 0, 0, 0xff}, []int{1, 2, 3, 4, 5, 6, 7, 8}},
		{"longer input only uses four bytes", []byte{0, 0, 0, 2, 0xff}, []int{2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := portsFromMask(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestMaskFromPorts(t *testing.T) {
	got, err := maskFromPorts([]int{1, 8, 32})
	if err != nil || !bytes.Equal(got, []byte{0x80, 0, 0, 0x81}) {
		t.Fatalf("got %x, %v", got, err)
	}
	for _, bad := range [][]int{{0}, {33}, {1, -1}} {
		if _, err := maskFromPorts(bad); err == nil {
			t.Errorf("%v must be rejected", bad)
		}
	}
}
