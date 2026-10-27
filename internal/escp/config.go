package escp

import (
	"context"
	"net/netip"
	"time"
)

// Default ports of the protocol: requests go to 29808, replies always come
// back to 29809 regardless of the source port.
const (
	defaultSwitchPort = 29808
	defaultHostPort   = 29809
)

// NoRetries is the Config.Retries value that asks for a single attempt per request. (Zero
// cannot mean that: it selects the default.)
const NoRetries = -1

// defaultRetries is the number of additional attempts when Config.Retries is zero.
const defaultRetries = 2

// Config configures a Client. Zero values select sensible defaults, except Host, which is
// required. A Config holds the switch's password: keep it out of logs and error messages.
type Config struct {
	// Host is the switch's IPv4 address or hostname. It is required unless MAC is set, in which
	// case the address is found by discovery on the first request.
	Host string
	// MAC, when set, pins the identity of the switch: a device that answers with another MAC
	// address is never written to (the request fails with ErrWrongSwitch). With no Host, or when
	// Host does not answer, the switch is looked up by MAC with a discovery probe on the first
	// request and again when it stops answering (its address may have changed); nothing is sent
	// by Dial.
	MAC MAC
	// Username and Password are sent with every write and every authenticated read.
	Username string
	Password string
	Timeout  time.Duration // how long to wait for a reply per attempt, default 3s
	// Retries is the number of additional attempts after the first when the switch stays
	// silent: zero selects the default (2), NoRetries (any negative value) selects none.
	// One request is therefore sent up to Retries+1 times, each waiting up to Timeout. A call
	// that needs a session makes up to four such requests (the key exchange and the request,
	// then both again if the switch stops answering mid-call), plus the discovery that learns
	// the switch's MAC on a client's first call: with the defaults 36 seconds, 45 on the first.
	Retries int

	// SwitchPort and HostPort are the UDP ports of the switch (29808) and of this host
	// (29809). A real switch always replies to 29809 whatever port the request came from,
	// so change them only to talk to a fake switch.
	SwitchPort int
	HostPort   int
	// BroadcastAddr is where discovery probes go besides the switch's own address;
	// default 255.255.255.255.
	BroadcastAddr string

	// WriteGate, when set, is consulted before every write; see WriteGate.
	WriteGate WriteGate
}

// InfoReader is the part of a Client that a WriteGate may use. It only reads.
type InfoReader interface {
	Info(ctx context.Context) (Info, error)
}

// WriteGate decides whether the client may send a write to the switch. Every
// method that changes the switch (each Set* and Delete*) calls it first, with
// the client itself and a description of the write (its method, the ports it
// names and its Impact), and sends nothing when it returns an error: the
// method returns that error unchanged. A gate may read from the switch through
// sw but must never write, and must be safe for concurrent use. Reads are
// never gated.
//
// A method that sends several writes consults the gate before each of them
// with the same operation, so a gate that is expensive should remember its
// verdict.
type WriteGate func(ctx context.Context, sw InfoReader, op Operation) error

// Info is what a discovery reply reports about the switch.
type Info struct {
	Model, Hardware string
	// Hostname is the device description.
	Hostname Hostname
	// Firmware is the version the switch reports, read into its parts.
	Firmware Firmware
	// MAC is the switch's hardware address; the zero value when the reply carried none.
	MAC MAC
	// IP, Netmask and Gateway are the management address the switch reports. An address the reply
	// did not carry is the invalid netip.Addr (Addr.IsValid is false), which is not the same as
	// 0.0.0.0: a gateway of 0.0.0.0 means "no gateway".
	IP, Netmask, Gateway netip.Addr
	DHCP                 bool
	// FactoryDefault is discovery TLV 14: the switch has never been configured
	// (still on factory credentials).
	FactoryDefault bool
}
