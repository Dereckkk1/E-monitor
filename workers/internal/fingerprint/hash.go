package fingerprint

import "radiocheck/pkg/audio"

// Hash is the canonical fingerprint hash record produced by GenerateHashes.
// Value is a 32-bit packed (f1, f2, dt) triple — see pkg/audio/hashes.go for
// the bit layout (§7.3).
type Hash = audio.Hash

// GenerateHashes builds (hash, time_frame) pairs from the constellation map
// peaks using fan-out pairing within the configured target zone.
func GenerateHashes(peaks [][2]int) []Hash {
	return audio.GenerateHashes(peaks)
}
