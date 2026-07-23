package lab

import "math/bits"

// hashInt is deterministic on purpose. The real runtime uses a random seed per
// map and an internal type-specific hasher. A fixed educational hasher makes
// the same key follow the same path on every run, which is much easier to learn.
func hashInt(key int, seed uint64) uint64 {
	x := uint64(int64(key)) + seed + 0x9e3779b97f4a7c15
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return bits.RotateLeft64(x, 17)
}

func h1(hash uint64) uint64 { return hash >> 7 }
func h2(hash uint64) byte   { return byte(hash & 0x7f) }

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
