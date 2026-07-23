package lab

import "math/bits"

// hashInt computes the repeatable educational hash used by both safe models.
//
// The real runtime chooses a random seed for every map and dispatches to a
// type-specific hasher. Repeating that behaviour here would make the same key
// jump to different groups between launches, which is inconvenient for a
// step-by-step laboratory. A fixed seed and SplitMix64-style mixing preserve the
// important property — well-distributed bits — while keeping every scenario
// deterministic.
func hashInt(key MapKey, seed HashSeed) FullHash {
	// Convert through int64 so negative int keys keep their two's-complement bit
	// pattern when widened to uint64.
	x := uint64(int64(key)) + seed + 0x9e3779b97f4a7c15

	// The xor-shift/multiply rounds spread a small change in the key across the
	// whole 64-bit result. The constants are mixing constants, not map metadata.
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31

	// A final rotation makes the chosen high/low split easier to demonstrate
	// without changing the amount of information in the hash.
	return bits.RotateLeft64(x, 17)
}

// h1 returns the part of the hash used for routing and probing.
//
// Swiss Table reserves the lower seven bits for H2, therefore H1 is everything
// above them. It chooses the initial group and then participates in the probe
// sequence when collisions occur.
func h1(hash FullHash) ProbeHash {
	return hash >> 7
}

// h2 returns the seven-bit fingerprint stored in a full slot's control byte.
//
// Comparing eight H2 values is cheaper than comparing eight complete keys. Only
// slots whose fingerprint matches become candidates for a full key comparison.
func h2(hash FullHash) ControlFingerprint {
	return ControlFingerprint(hash & 0x7f)
}

// bitString formats the lowest width bits as a fixed-width binary prefix for
// the directory visualization.
func bitString(value, width int) string {
	if width == 0 {
		return "—"
	}

	result := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		if value&1 == 1 {
			result[i] = '1'
		} else {
			result[i] = '0'
		}
		value >>= 1
	}
	return string(result)
}
