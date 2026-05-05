package audio

// Constellation-map pairing constants (§7.3 of plano_implementacao.md).
const (
	FanOut         = 5  // max target peaks to pair with each anchor
	TargetZoneTMin = 1  // minimum frame delta for pairing
	TargetZoneTMax = 16 // maximum frame delta for pairing
	TargetZoneF    = 50 // maximum frequency bin distance for pairing
)

// Hash represents a single fingerprint hash.
type Hash struct {
	Value     uint32
	TimeFrame int
}

// GenerateHashes produces fingerprint hashes from spectrogram peaks.
// For each peak (anchor), pairs it with up to FanOut subsequent peaks that fall
// within the target zone: [TargetZoneTMin, TargetZoneTMax] frames ahead and
// within ±TargetZoneF frequency bins.
//
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
			if dt < TargetZoneTMin {
				continue
			}
			if dt > TargetZoneTMax {
				// Peaks sorted by frame; once dt exceeds max we can break.
				break
			}

			df := targetBin - anchorBin
			if df < 0 {
				df = -df
			}
			if df > TargetZoneF {
				continue
			}

			f1 := uint32(anchorBin) & 0x1FF
			f2 := uint32(targetBin) & 0x1FF
			d := uint32(dt) & 0x3FFF

			hashes = append(hashes, Hash{
				Value:     (f1 << 23) | (f2 << 14) | d,
				TimeFrame: anchorFrame,
			})
			paired++
		}
	}

	return hashes
}
