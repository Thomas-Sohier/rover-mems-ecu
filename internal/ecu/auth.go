package ecu

func bit(bitNum int, theByte int) int {
	// returns the requested bit, counting from 0-7, 0-15 for doubles
	return (theByte >> bitNum) & 1
}

// GenerateKey computes the security-access key from the seed the ECU returns
// during the 27 01 / 27 02 handshake (used by both MEMS 2J and MEMS 3).
//
// This reproduces the ECU's own key algorithm so it accepts our 27 02 reply and
// unlocks the diagnostic session. The number of mixing rounds is selected by
// four bits of the seed (15, 7, 4, 0); each round shifts the seed right by one,
// forces the LSB based on bits 13 and 3, and feeds bits 9/8/2/1 through XOR into
// the MSB — a 16-bit linear-feedback style scramble. The values and bit
// positions are fixed by the ECU firmware and must match exactly; do not "tidy"
// them. A seed of 0 means the ECU is already unlocked and no key is needed.
func GenerateKey(seed int) int {
	key := 0
	loops := 1

	if bit(15, seed) > 0 {
		loops += 8
	}
	if bit(7, seed) > 0 {
		loops += 4
	}
	if bit(4, seed) > 0 {
		loops += 2
	}
	if bit(0, seed) > 0 {
		loops += 1
	}

	for loops > 0 {
		key = seed >> 1 // take the seed shifted right by 1 (each loop changes seed)

		if bit(13, seed) > 0 && bit(3, seed) > 0 {
			key &= 0b11111111111111110 // unset LSB
		} else {
			key |= 0b0000000000000001 // set LSB
		}

		xors := bit(9, seed) ^ bit(8, seed) ^ bit(2, seed) ^ bit(1, seed)
		if xors > 0 {
			key |= 0b1000000000000000 // set msb
		}

		seed = key
		loops--
	}

	return key
}
