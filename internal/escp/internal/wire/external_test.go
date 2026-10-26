package wire_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestUnmarshalRejectsPacketsThatLieAboutTheirStructure(t *testing.T) {
	good := wire.Marshal(wire.Header{Op: wire.OpReply, Seq: 7}, []wire.TLV{{Type: 5, Value: []byte{1, 2}}})
	if _, tlvs, err := wire.Unmarshal(good); err != nil || len(tlvs) != 1 {
		t.Fatalf("control packet must parse: %v %v", tlvs, err)
	}
	t.Run("TLV longer than the packet", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[wire.HeaderLen+2], bad[wire.HeaderLen+3] = 0, 100 // claims 100 value bytes, 2 present
		if _, _, err := wire.Unmarshal(bad); err == nil || !strings.Contains(err.Error(), "truncated TLV type 5") {
			t.Fatalf("want a truncation error, got %v", err)
		}
	})
	t.Run("too short for a header", func(t *testing.T) {
		if _, _, err := wire.Unmarshal(good[:20]); err == nil || !strings.Contains(err.Error(), "not a valid packet") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("declared length does not match", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[21]++
		if _, _, err := wire.Unmarshal(bad); err == nil {
			t.Fatal("a wrong total length must be rejected")
		}
	})
	t.Run("unknown version", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[0] = 2
		if _, _, err := wire.Unmarshal(bad); err == nil {
			t.Fatal("version 2 must be rejected")
		}
	})
	t.Run("opcode out of range", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[1] = 5
		if _, _, err := wire.Unmarshal(bad); err == nil {
			t.Fatal("opcode 5 must be rejected")
		}
	})
}

func TestCString(t *testing.T) {
	for in, want := range map[string]string{"abc\x00def": "abc", "abc": "abc", "": "", "\x00": ""} {
		if got := wire.CString([]byte(in)); got != want {
			t.Errorf("cstring(%q) = %q, want %q", in, got, want)
		}
	}
}

// A ciphertext whose most significant words are zero is sent with a smaller
// word count, exactly like the vendor utility. The key was found by search and
// the expected TLV is cross-checked by decrypting it with the private key.
func TestRSASessionTLVTrimsLeadingZeroWords(t *testing.T) {
	key := []byte("AAAAahqn")
	want, _ := hex.DecodeString("0003569d648da2df0000")
	got := wire.RSASessionTLV(key)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
	if got[1] != 3 {
		t.Fatalf("word count must be trimmed to 3, got %d", got[1])
	}
	if back := escptest.DecryptSessionKey(got); !bytes.Equal(back, key) {
		t.Fatalf("the trimmed TLV must still decrypt to the key, got %q", back)
	}
}
