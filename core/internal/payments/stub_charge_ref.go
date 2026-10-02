package payments

import "strings"

// StubChargeRefPrefix prefixes the charge refs the PORTAL rail's own stub mints (StubGateway,
// the default simulated gateway). The construction is exact and it is the rail's fingerprint:
// the prefix, then exactly 16 lowercase hex chars (sha256 of the two mint inputs, first 8
// bytes, hex). The printed receipt reads this export to mark simulated card charges on paper:
// a CARD payments row whose gateway ref IS this construction is a charge that never reached a
// processor, and the receipt says so beside the tender.
//
// WHY THE EXACT SHAPE AND NOT THE BARE PREFIX: the live rail (RunGateway) writes the
// processor's own opaque TransID verbatim as gateway_ref, and an external processor's id shape
// is not ours to bound, so a bare prefix match could stamp the simulated word on a LIVE charge.
// No other minter of this exact construction exists in the repository: the till's capture stub
// mints rp_sim_, the live gateway writes foreign ids, and this package's mint is the only
// producer of the prefix followed by 16 lowercase hex.
const StubChargeRefPrefix = "ch_"

// stubChargeRefHexLen is the hex tail the stub's mint always produces: sha256, first 8 bytes,
// hex encoded.
const stubChargeRefHexLen = 16

// IsStubChargeRef reports whether ref is the portal stub's exact charge ref construction: the
// exported prefix followed by exactly 16 lowercase hex chars and nothing else. The receipt's
// simulated stamp asks this of every CARD row, so what the stub mints and what the paper calls
// simulated are one check, and a near-miss shape (a longer tail, uppercase hex, a live
// processor's own id) never carries the word.
func IsStubChargeRef(ref string) bool {
	tail, ok := strings.CutPrefix(ref, StubChargeRefPrefix)
	if !ok || len(tail) != stubChargeRefHexLen {
		return false
	}
	for i := 0; i < len(tail); i++ {
		c := tail[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
