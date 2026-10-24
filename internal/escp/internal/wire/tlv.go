package wire

// TLV types of the ESCP protocol, as used by this client. The catalogue is the one
// place that maps a number to its meaning; the settings code in the parent package
// owns the layout of each value.
//
// Discovery and session.
const (
	TLVModel          = 1
	TLVHostname       = 2
	TLVMAC            = 3
	TLVIP             = 4
	TLVNetmask        = 5
	TLVGateway        = 6
	TLVFirmware       = 7
	TLVHardware       = 8
	TLVDHCP           = 9
	TLVPortCount      = 10 // number of physical ports (1 byte)
	TLVLED            = 12
	TLVAutoSave       = 13 // 1 = configuration changes are saved automatically (1 byte)
	TLVFactoryDefault = 14
	TLVUsername       = 512
	TLVPassword       = 514
	TLVRSAKey         = 528
	TLVConfigFile     = 768 // configuration backup (read through an authenticated SET)
)

// ConfigMagic is the 4-byte prefix of the switch's configuration file.
var ConfigMagic = []byte{0x23, 0x89, 0x23, 0x89}

// ErrCodeKeyAccepted is returned in the reply to a successful RSA key exchange.
const ErrCodeKeyAccepted = 4098

// Settings.
const (
	// TLVPorts is the Port Setting page: one 7-byte entry per port.
	TLVPorts = 4096

	TLVIGMPEnabled     = 4352 // snooping enabled (1 byte)
	TLVIGMPGroups      = 4353 // learned groups, repeated 10-byte entries
	TLVIGMPSuppression = 4354 // report message suppression (1 byte)

	// TLVLAG is Switching > Port Trunk: one TLV per group, group id (1 byte) and member port mask (4 bytes).
	TLVLAG = 4608

	// TLVVLANMTU is VLAN > MTU VLAN: 2 bytes (enabled, uplink port).
	TLVVLANMTU = 8192

	TLVVLANPortBased    = 8448 // mode enabled (1 byte)
	TLVVLANPortBasedVID = 8449 // one TLV per VLAN: id (1 byte), member port mask (4 bytes)
	TLVVLANPortBasedMax = 8450 // maximum number of VLANs (1 byte)

	TLVVLAN8021Q     = 8704 // mode enabled (1 byte)
	TLVVLAN8021QVLAN = 8705 // one TLV per VLAN: id(2) member mask(4) tagged mask(4) name (NUL terminated)
	TLVVLAN8021QPVID = 8706 // one TLV per port: port(1) pvid(2)
	TLVVLAN8021QMax  = 8707 // maximum number of VLANs (1 byte)

	TLVQoSMode     = 12288 // 1 byte
	TLVQoSPriority = 12289 // one TLV per port: port(1) priority(1), raw 0..3

	// TLVStormControl is QoS > Storm Control: one TLV per port, 9 bytes:
	// port(1) enabled(1) unknown-unicast(1) multicast(1) broadcast(1) rate(4, kbps).
	TLVStormControl = 12800

	TLVBandwidthIngress = 12544 // one TLV per port: port(1) enabled(1) rate(4, kbps; 0 = unlimited)
	TLVBandwidthEgress  = 12545

	// TLVPortStats is Monitoring > Port Statistics: one 19-byte TLV per port.
	TLVPortStats = 16384

	// TLVMirror is Monitoring > Port Mirror: 10 bytes (enabled, destination port, ingress mask, egress mask).
	TLVMirror = 16640

	// TLVLoopPrevention is Monitoring > Loop Prevention: 1 byte, 1 = enabled.
	TLVLoopPrevention = 17152
)
