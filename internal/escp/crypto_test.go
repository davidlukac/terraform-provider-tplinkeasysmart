package escp

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Vectors produced by the Python prototype (tools/escp/escp.py), which was
// validated against a real TL-SG108E v6.

func seq(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func TestStaticRC4(t *testing.T) {
	want, _ := hex.DecodeString("5c75680779bbb6b5f3620cc81898709a523ab0e6c3bab8fa48965c26de1f8e857b32a3e5030f0d11")
	if got := rc4Static(seq(40)); !bytes.Equal(got, want) {
		t.Fatalf("static rc4 mismatch:\n got %x\nwant %x", got, want)
	}
	if got := rc4Static(rc4Static(seq(40))); !bytes.Equal(got, seq(40)) {
		t.Fatal("static rc4 is not an involution")
	}
}

func TestSessionRC4(t *testing.T) {
	want, _ := hex.DecodeString("01070706000300060c0d0d0b0d0c0c0810141711141317111a1e191c1e1a1a1c2727212625262321")
	if got := rc4Session(seq(40), []byte("AbCdEf12")); !bytes.Equal(got, want) {
		t.Fatalf("session rc4 mismatch:\n got %x\nwant %x", got, want)
	}
}

func TestRSASessionTLV(t *testing.T) {
	want, _ := hex.DecodeString("0004c1aaafc576532c70")
	if got := RSASessionTLV([]byte("AbCdEf12")); !bytes.Equal(got, want) {
		t.Fatalf("rsa tlv mismatch: got %x want %x", got, want)
	}
}
