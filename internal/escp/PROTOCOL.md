# ESCP: the TP-Link Easy Smart UDP management protocol

This is the wire protocol the provider speaks (`internal/escp`). It was worked out from the vendor's configuration utility, from earlier open-source clients ([smrt](https://github.com/rgl/smrt) and its forks) and by observing a TL-SG108E hardware v6 (firmware 1.0.0 Build 20230218). The firmware released in 2023 added the RSA key exchange described below.

The notes are descriptive, written for people who maintain this provider or want to build a compatible client. Layouts marked "verified" were exercised against a real switch; the others come from the vendor utility.

## Transport
- UDP. Client binds `29809`, switch listens on `29808`. Same L2 segment only. Requests can be sent to the broadcast address **or** unicast to the switch IP; replies come back to port 29809 from the switch IP.
- Header is 32 bytes, network byte order (`!bb6s6shihhhhi`): version(=1), op, switch MAC, host MAC, sequence (short), error code (int), total length (short, header + TLVs + end marker), fragment offset, flag, token id, checksum. Payload is TLVs `type(short) len(short) value`, terminated by `ff ff 00 00`. Strings are NUL-terminated, integers big-endian.
- Op codes: 0 DISCOVERY, 1 GET, 2 reply to GET/DISCOVERY, 3 SET/LOGIN, 4 reply to SET. A reply echoes the request's sequence number; the client drops anything else.

## Encryption (two RC4 variants)
1. **Static RC4 (256-byte state)**, used for discovery and the key-exchange packet. Its key is the one the vendor tooling ships with (public in several open-source clients); the state is re-initialised for every packet, the stream is not carried between packets.
2. **Session RC4 with an 8-byte state**: the same algorithm with state size 8, keyed by a random 8-character alphanumeric string generated per client run. Fresh state per packet.

Sending: discovery and the RSA key-exchange packet use static RC4; everything else uses the session RC4 once the exchange succeeded. Receiving: decrypt with static RC4; if the result looks like a header (`byte0 == 1 && byte1 < 5`) accept it, otherwise decrypt with the session RC4.

## Login handshake (new in fw ≥ 2023-02)
1. **Key exchange:** op 3 (SET) packet with one TLV, type **528**, value = RSA-encrypted session key, encrypted with the *static* RC4.
   - RSA is textbook (no padding), e = 65537, operands little-endian. The TL-SG108E v6 uses a **64-bit** modulus (see `internal/escp`); the vendor utility carries a 1024-bit modulus for some other models.
   - TLV value layout (10 bytes): `00`, number of 16-bit words (4), then the ciphertext as 4 little-endian 16-bit words, least significant word first.
2. **Reply:** op 4 with **error code 4098** means "session key accepted"; the reply is already encrypted with the session RC4.
3. **Login:** op 3 with TLV 512 (username) and TLV 514 (password), both NUL-terminated strings, session RC4. Error code **0** = success.
4. **Credentials ride on every SET**: the client prepends TLV 512/514 to each SET request. There is no token; a SET without valid credentials is rejected. **GET requests need no credentials** in this protocol version (the LED GET worked straight after the key exchange; whether it works without the key exchange was not tested).

## Known TLVs
| Type | Meaning | GET | SET |
|---|---|---|---|
| 1, 2, 3, 4, 5, 6, 7, 8, 9, 13 | model, hostname, MAC, IP, mask, gateway, firmware, hardware, DHCP, v4 flag | discovery | – |
| 14 | unknown 1-byte flag, `00` or `01` depending on the unit | discovery | – |
| **12** | **LED** (1 byte, 1 = on) | empty TLV | `01` / `00` |
| 10 | port count (login GET) | empty TLV | – |
| 512 / 514 | username / password | – | NUL string |
| 528 | RSA-encrypted session key | – | 10 bytes |

## Settings TLVs
All GETs below need the session-key exchange first; a GET of several types in one packet works. Multi-row settings come back as one TLV per row. Port numbers are 1-based; "mask" is a 32-bit big-endian bitmap, bit n-1 = port n.

| Type | Setting | Value layout |
|---|---|---|
| 4096 | Port Setting | port, state(1=on), LAG id (0=none), cfg speed, actual speed, cfg flow control, actual flow control. Speed: 0 link-down, 1 auto, 2 10M-half, 3 10M-full, 4 100M-half, 5 100M-full, 6 1000M-full |
| 4096 (SET) | Port Setting write | same 7-byte layout as the read, with the lag and actual-speed bytes zero: port, state, 0, cfg speed, 0, flow control, 0. One TLV per port; verified live on an unused port (flow control, speed, admin disable, all restored). |
| 12288 / 12289 (SET) | QoS write | mode: SET 12288 with 1 byte. Priorities: SET one 12289 TLV (port, raw 0..3) for **every** port, as the vendor utility does. **Priorities are accepted but ignored unless the mode is port-based (0)**; verified live on an unused port (mode switched to port-based and back). |
| 12544 / 12545 (SET) | Bandwidth Control write | one TLV per request, ingress SET first, then egress SET: port(1), enable flag(1: 1 if rate != 0), rate(4, kbps; the UI rounds to multiples of 64, max 1000000). The "reserved" byte of the read is this enable flag. Verified live on an unused port. |
| 12800 (SET) | Storm Control write | one TLV per port, 9 bytes: port, enable flag (1 if any type selected), unknown-unicast, multicast, broadcast flags, rate(4, kbps, multiple of 64, >= 64 when a type is selected; 0 and no types to disable). Verified live on an unused port. |
| 16640 (SET) | Port Mirror write | 10 bytes: enabled, destination port, ingress mask(4), egress mask(4); all zero to disable. Verified live: mirror port 4 -> unused port 7, then disabled. |
| 4608 (SET) | Port Trunk / LAG write | one TLV: group id (1 byte), member port mask (4 bytes); mask 0 removes the group. The switch refuses ports that conflict with mirroring, VLANs, rate limits or storm control. Verified live: LAG2 = ports 6+7 created and removed, LAG1 untouched. |
| 8449 (SET) | Port Based VLAN group write | one TLV: VLAN id (1 byte), member mask (4 bytes); mask 0 removes the group. **Membership is exclusive**: adding ports to a group takes them out of their old group (VLAN 1), and removing the group returns them to VLAN 1 (verified live with VLAN 2 = ports 6+7). The mode (8448) is never written. |
| 768 (SET, read) | Config backup | an authenticated SET (user name 512 + password 514) carrying an **empty** TLV 768 returns the configuration file in TLV 768 values spread over several packets: every packet but the last has the header fragment field (bytes 22-23) set to 1. Total 2258 bytes, magic `23 89 23 89`, byte-identical to the web download. Nothing on the switch changes. |
| 2 (SET) | Device description write | SET TLV 2 (NUL string) plus 9 (DHCP flag), 4, 5, 6 (current IP, mask, gateway, 4 bytes each) as the vendor utility sends them. Verified live: renamed and restored, IP configuration unchanged. Letters, digits, hyphens only. |
| 16384 | Port Statistics | port, state, link speed (as above), tx good, tx bad, rx good, rx bad: four 4-byte big-endian counters, 19 bytes total |
| 4608 | Port Trunk / LAG | one TLV per group: group id (1 byte), member port mask (4 bytes); unused group = mask 0 |
| 4352 / 4354 / 4353 | IGMP Snooping | 4352: enabled (1 byte); 4354: report message suppression (1 byte, comes back with the 4352 GET); 4353: learned groups, repeated 10-byte entries ip(4) vlan(2) port mask(4). **Request 4353 separately**: a combined GET of 4352+4354+4353 is rejected with error code 1. SET carries 4352 and 4354 together (verified with a same-value write). |
| 17152 | Loop Prevention | 1 byte, 1 = enabled; SET with credentials (same-value write verified) |
| 16640 | Port Mirror | 10 bytes: enabled, destination port, ingress mask(4), egress mask(4); all zero when disabled. Enabled layout comes from the vendor code (disabled state verified live). Read-only in the provider on purpose. |
| 8192 | MTU VLAN | 2 bytes: enabled, uplink port (1 when disabled). TLVs 8708/8709 (MTU management ports) get no reply on this model. |
| 8448 / 8449 / 8450 | Port Based VLAN | one GET of 8448 returns all three. 8448: mode enabled (1 byte); 8449: one TLV per VLAN: id (1 byte), member port mask (4 bytes); 8450: maximum number of VLANs (1 byte). 8710/8711 (management ports) get no reply on this model. |
| 8704 / 8705 / 8706 / 8707 | 802.1Q VLAN + PVID | 8704: mode (1 byte); 8705: one TLV per VLAN: id(2) member mask(4) tagged mask(4) name (NUL string), tagged = member and in tagged mask, else untagged (layout from vendor code; a unit with 802.1Q off has none); 8706: one TLV per port: port(1) pvid(2) (verified live); 8707: max VLANs (1 byte). 8705 and 8706 are requested separately. 8708-8713 (management VLAN etc.) get no reply on this model. |
| 12288 / 12289 | QoS Basic | 12288: mode (0 port-based, 1 802.1p, 2 DSCP/802.1p); 12289: one TLV per port: port(1) priority(1), raw 0..3 shown as 1 (lowest)..4 (highest) in the UI. Requested separately. Verified live. |
| 12544 / 12545 | Bandwidth Control | ingress / egress, requested separately. One TLV per port, 6 bytes: port(1), reserved(1), rate(4, kbps, 0 = unlimited). Verified live (all unlimited). |
| 12800 | Storm Control | one TLV per port, 9 bytes: port(1), reserved(1), unknown-unicast(1), multicast(1), broadcast(1), rate(4, kbps). Active in the web UI when rate != 0 and a type is set. Verified live (all off). |
| 10 / 13 / 14 | System extras | 10: port count (1 byte); 13: auto-save (1 byte, 1 = on); 14: factory-default flag (1 byte, returned by discovery only; a GET of 14, 15 or 16 gets no reply). 11 (management VLAN) answers error -2 on this model. |

## Caveats
- **No integrity or real confidentiality**: static key is public, the 64-bit RSA is trivially breakable, and a SET carries the password. Anyone on the LAN who sees the key exchange can recover the session key and the credentials. Treat ESCP as LAN-management-VLAN-only.
- Same-L2 only. macOS needs the interface bound for broadcast; unicast to the switch IP works too.

## Compatibility notes
- Verified on hardware v6, firmware 20230218. Newer firmware (20250710) and other Easy Smart models are not yet verified; run the read-only data sources against a spare unit first.
- Only GETs are safe to experiment with: an unknown TLV is usually answered with silence or an error code, but a SET can change the device.
