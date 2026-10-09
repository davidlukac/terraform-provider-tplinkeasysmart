// Package escp implements the TP-Link Easy Smart UDP management protocol as
// spoken by TL-SG108E v6 switches. See docs/escp-protocol.md in the
// repository root for how it was reverse-engineered.
package escp

import (
	"encoding/binary"
	"encoding/hex"
	"math/big"
)

// staticKeyBlob is the vendor utility's TEA-encrypted static RC4 key.
const staticKeyBlob = "ca4388b75599454c45f7a9b9e356b5482f08ff00956f181301fa4921354fcbc4c1a81e648654e5d4df089157dadd6bda56f7507627088b6853c5575778d37f6115acc50747ec0775b60d03a9bde73a5a242bcbd23bf99f5809c94bb5cab3a026e60957f01ae1444786d03fa65362ed7d09fe859e9973f505ef0d7499b37594fa4636adeba50f9c006f708c1c1d6302a901f7617d9252f7b33b31074eb8764df76735cae64cef031d784729c76603207f54f655c5e80dd6128eb7735bc9f5ee6e1393e0de4e4d826547e9db965563acdc00f29c72f66ef7e6b83afb044c1a1110ff507f90685aacf5c3e31eca0e93489b19f89a83142ea3933eb88b2bea51e729fcc1b6441b4806d5"

// teaKey holds the signed key words from the jar.
var teaKey = [4]uint32{2023708229, 0xF68BD5A4 /* -158607964 */, 0x81963FFA /* -2120859654 */, 1167043672}

func teaDecryptBlock(b []byte) []byte {
	v0 := binary.BigEndian.Uint32(b[:4])
	v1 := binary.BigEndian.Uint32(b[4:])
	sum := uint32(0xC6EF3720)
	for i := 0; i < teaRounds; i++ {
		v1 -= ((v0 << 4) + teaKey[2]) ^ (v0 + sum) ^ (uint32(int32(v0)>>5) + teaKey[3])
		v0 -= ((v1 << 4) + teaKey[0]) ^ (v1 + sum) ^ (uint32(int32(v1)>>5) + teaKey[1])
		sum += 0x61C88647
	}
	out := make([]byte, 8)
	binary.BigEndian.PutUint32(out[:4], v0)
	binary.BigEndian.PutUint32(out[4:], v1)
	return out
}

// staticKeyFrom decrypts the TEA-encrypted key blob (hex). It panics on invalid
// hex: the blob is a compile-time constant, so that is a programming error.
func staticKeyFrom(blobHex string) []byte {
	blob, err := hex.DecodeString(blobHex)
	if err != nil {
		panic(err)
	}
	var plain []byte
	for i := 0; i+8 <= len(blob); i += 8 {
		plain = append(plain, teaDecryptBlock(blob[i:i+8])...)
	}
	return plain[plain[0]:]
}

// rc4State runs the key schedule for a state of n entries. Key bytes are
// signed, as in the Java original.
func rc4State(key []byte, n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	j := 0
	for i := 0; i < n; i++ {
		j = ((j+s[i]+int(int8(key[i%len(key)])))%n + n) % n
		s[i], s[j] = s[j], s[i]
	}
	return s
}

// rc4 is the jar's RC4: standard PRGA over a state of n entries (256 for the
// static key, 8 for the per-session key), restarted for every packet.
func rc4(data []byte, state []int, n int) []byte {
	s := append([]int(nil), state...)
	out := make([]byte, len(data))
	i, j := 0, 0
	for k := range data {
		i = (i + 1) % n
		j = (j + s[i]) % n
		s[i], s[j] = s[j], s[i]
		out[k] = data[k] ^ byte(s[(s[i]+s[j])%n])
	}
	return out
}

// staticStateSize is the state size of the static RC4 (the session RC4 uses SessionKeyLen).
const staticStateSize = 256

var staticState = rc4State(staticKeyFrom(staticKeyBlob), staticStateSize)

func rc4Static(data []byte) []byte { return rc4(data, staticState, staticStateSize) }

// SessionKeyLen is the length of the random per-session key.
const SessionKeyLen = 8

func rc4Session(data, key []byte) []byte {
	return rc4(data, rc4State(key, SessionKeyLen), SessionKeyLen)
}

// rsaN is the 64-bit modulus used by TL-SG108E v6 (little-endian in the jar).
var rsaN = new(big.Int).SetBytes(reverse([]byte{191, 78, 109, 100, 214, 185, 250, 179}))

const rsaE = 65537

// RSA key-exchange value (TLV 528): a zero byte, the word count, then the
// ciphertext as little-endian 16-bit words, least significant first. The
// 64-bit modulus gives four words.
const (
	rsaWords  = 4
	rsaTLVLen = 2 + 2*rsaWords
)

// teaRounds is the number of TEA rounds protecting the static key blob.
const teaRounds = 32

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[len(b)-1-i] = b[i]
	}
	return out
}

// RSASessionTLV encrypts the session key (textbook RSA, no padding) and
// returns the 10-byte TLV 528 value: 00, word count, 4 little-endian words.
func RSASessionTLV(key []byte) []byte {
	c := new(big.Int).Exp(new(big.Int).SetBytes(reverse(key)), big.NewInt(rsaE), rsaN)
	words := make([]uint16, rsaWords)
	mask := big.NewInt(0xFFFF)
	for i := range words {
		words[i] = uint16(new(big.Int).And(new(big.Int).Rsh(c, uint(16*i)), mask).Uint64())
	}
	n := rsaWords
	for n > 1 && words[n-1] == 0 {
		n--
	}
	out := make([]byte, rsaTLVLen)
	out[1] = byte(n)
	for i, w := range words {
		out[2+2*i] = byte(w)
		out[3+2*i] = byte(w >> 8)
	}
	return out
}
