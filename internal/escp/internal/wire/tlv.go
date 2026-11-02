package wire

// TLVType is the number that says what a TLV holds (the catalog below). Comparing or passing a
// plain number as a type is a compile error.
type TLVType int16

// TLV types of the ESCP protocol, as used by this client. The catalog is the one
// place that maps a number to its meaning; the settings code in the parent package
// owns the layout of each value.
//
// Discovery and session.
const (
	TLVModel          TLVType = 1
	TLVHostname       TLVType = 2
	TLVMAC            TLVType = 3
	TLVIP             TLVType = 4
	TLVNetmask        TLVType = 5
	TLVGateway        TLVType = 6
	TLVFirmware       TLVType = 7
	TLVHardware       TLVType = 8
	TLVDHCP           TLVType = 9
	TLVPortCount      TLVType = 10 // number of physical ports (1 byte)
	TLVLED            TLVType = 12
	TLVAutoSave       TLVType = 13 // 1 = configuration changes are saved automatically (1 byte)
	TLVFactoryDefault TLVType = 14
	TLVUsername       TLVType = 512
	TLVPassword       TLVType = 514
	TLVRSAKey         TLVType = 528
	TLVConfigFile     TLVType = 768 // configuration backup (read through an authenticated SET)
)

// ConfigMagic is the 4-byte prefix of the switch's configuration file.
var ConfigMagic = []byte{0x23, 0x89, 0x23, 0x89}

// ErrCodeKeyAccepted is returned in the reply to a successful RSA key exchange.
const ErrCodeKeyAccepted = 4098

// Settings.
const (
	// TLVPorts is the Port Setting page: one 7-byte entry per port.
	TLVPorts TLVType = 4096

	TLVIGMPEnabled     TLVType = 4352 // snooping enabled (1 byte)
	TLVIGMPGroups      TLVType = 4353 // learned groups, repeated 10-byte entries
	TLVIGMPSuppression TLVType = 4354 // report message suppression (1 byte)

	// TLVLAG is Switching > Port Trunk: one TLV per group, group id (1 byte) and member port mask (4 bytes).
	TLVLAG TLVType = 4608

	// TLVVLANMTU is VLAN > MTU VLAN: 2 bytes (enabled, uplink port).
	TLVVLANMTU TLVType = 8192

	TLVVLANPortBased    TLVType = 8448 // mode enabled (1 byte)
	TLVVLANPortBasedVID TLVType = 8449 // one TLV per VLAN: id (1 byte), member port mask (4 bytes)
	TLVVLANPortBasedMax TLVType = 8450 // maximum number of VLANs (1 byte)

	TLVVLAN8021Q     TLVType = 8704 // mode enabled (1 byte)
	TLVVLAN8021QVLAN TLVType = 8705 // one TLV per VLAN: id(2) member mask(4) tagged mask(4) name (NUL terminated)
	TLVVLAN8021QPVID TLVType = 8706 // one TLV per port: port(1) pvid(2)
	TLVVLAN8021QMax  TLVType = 8707 // maximum number of VLANs (1 byte)

	TLVQoSMode     TLVType = 12288 // 1 byte
	TLVQoSPriority TLVType = 12289 // one TLV per port: port(1) priority(1), raw 0..3

	// TLVStormControl is QoS > Storm Control: one TLV per port, 9 bytes:
	// port(1) enabled(1) unknown-unicast(1) multicast(1) broadcast(1) rate(4, kbps).
	TLVStormControl TLVType = 12800

	TLVBandwidthIngress TLVType = 12544 // one TLV per port: port(1) enabled(1) rate(4, kbps; 0 = unlimited)
	TLVBandwidthEgress  TLVType = 12545

	// TLVPortStats is Monitoring > Port Statistics: one 19-byte TLV per port.
	TLVPortStats TLVType = 16384

	// TLVMirror is Monitoring > Port Mirror: 10 bytes (enabled, destination port, ingress mask, egress mask).
	TLVMirror TLVType = 16640

	// TLVLoopPrevention is Monitoring > Loop Prevention: 1 byte, 1 = enabled.
	TLVLoopPrevention TLVType = 17152
)
