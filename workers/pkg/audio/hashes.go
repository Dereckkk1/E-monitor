package audio

// Hash constants (32-bit hash encoding)
const (
	FanOut   = 15  // number of target peaks to pair with each anchor
	MaxDelta = 200 // maximum frame delta for pairing
)

// Hash represents a single fingerprint hash.
type Hash struct {
	Value     uint32
	TimeFrame int
}

// GenerateHashes produces fingerprint hashes from spectrogram peaks.
// For each peak (anchor), pairs it with up to FanOut subsequent peaks within MaxDelta frames.
// Hash encoding (32-bit):
//
//	bits 23-31 (9 bits): anchor frequency bin (f1 & 0x1FF)
//	bits 14-22 (9 bits): target frequency bin (f2 & 0x1FF)
//	bits 0-13  (14 bits): frame delta (dt & 0x3FFF)
func GenerateHashes(peaks [][2]int) []Hash {
	if len(peaks) == 0 {
		return nil
	}

	var hashes []Hash

	for i, anchor := range peaks {
		anchorFrame := anchor[0]
		anchorBin := anchor[1]

		paired := 0
		for j := i + 1; j < len(peaks) && paired < FanOut; j++ {
			target := peaks[j]
			targetFrame := target[0]
			targetBin := target[1]

			dt := targetFrame - anchorFrame
			if dt <= 0 {
				continue
			}
			if dt > MaxDelta {
				// Peaks are sorted by frame; once delta exceeds MaxDelta we can break
				break
			}

			f1 := uint32(anchorBin) & 0x1FF
			f2 := uint32(targetBin) & 0x1FF
			d := uint32(dt) & 0x3FFF

			value := (f1 << 23) | (f2 << 14) | d

			hashes = append(hashes, Hash{
				Value:     value,
				TimeFrame: anchorFrame,
			})
			paired++
		}
	}

	return hashes
}
