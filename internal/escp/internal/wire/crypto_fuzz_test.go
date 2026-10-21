package wire

import (
	"bytes"
	"math/big"
	"testing"
)

// RC4 is its own inverse for both state sizes, whatever the key and data.
func FuzzRC4IsAnInvolution(f *testing.F) {
	f.Add([]byte("hello"), []byte("AbCdEf12"))
	f.Add([]byte{}, []byte("AbCdEf12"))
	f.Fuzz(func(t *testing.T, data, keyIn []byte) {
		key := make([]byte, SessionKeyLen)
		copy(key, keyIn)
		if got := RC4Session(RC4Session(data, key), key); !bytes.Equal(got, data) {
			t.Fatal("session RC4 is not an involution")
		}
		if got := RC4Static(RC4Static(data)); !bytes.Equal(got, data) {
			t.Fatal("static RC4 is not an involution")
		}
	})
}

// Whatever session key the client picks (printable, 8 bytes), the switch's
// private key must recover it from the TLV.
func FuzzRSASessionTLVRoundTrip(f *testing.F) {
	f.Add([]byte("AbCdEf12"))
	f.Add([]byte("zzzzzzzz"))
	f.Add([]byte("00000000"))
	phi := new(big.Int).Mul(big.NewInt(3443843008), big.NewInt(3765816958)) // (p-1)(q-1) of the public modulus
	d := new(big.Int).ModInverse(big.NewInt(RSAExponent), phi)
	f.Fuzz(func(t *testing.T, raw []byte) {
		key := make([]byte, SessionKeyLen)
		for i := range key {
			c := byte('A')
			if i < len(raw) {
				c = 'A' + raw[i]%58 // letters, which is within the alphabet the client draws from
			}
			key[i] = c
		}
		tl := RSASessionTLV(key)
		if len(tl) != rsaTLVLen || tl[0] != 0 || int(tl[1]) < 1 || int(tl[1]) > rsaWords {
			t.Fatalf("malformed key exchange value %x", tl)
		}
		c := new(big.Int)
		for i := rsaWords - 1; i >= 0; i-- {
			c.Lsh(c, 16)
			c.Or(c, big.NewInt(int64(tl[2+2*i])|int64(tl[3+2*i])<<8))
		}
		m := new(big.Int).Exp(c, d, rsaN)
		if got := ReverseBytes(m.FillBytes(make([]byte, SessionKeyLen))); !bytes.Equal(got, key) {
			t.Fatalf("key %q came back as %q", key, got)
		}
	})
}
