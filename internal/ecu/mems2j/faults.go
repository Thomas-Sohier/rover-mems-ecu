package mems2j

// parseFaultsLocked parses fault codes from the ECU response.
// Must be called with m.mu already held.
func (m *MEMS2J) parseFaultsLocked(buffer []byte) {
	faults := []string{}

	// RAM 594h 4h
	if len(buffer) >= 5 {
		if (buffer[4] & 0b01000000) > 0 {
			faults = append(faults, "Outside air temp (low voltage)")
		}
		if (buffer[4] & 0b00100000) > 0 {
			faults = append(faults, "Power supply (low voltage)")
		}
		if (buffer[4] & 0b00010000) > 0 {
			faults = append(faults, "Engine oil temp (low voltage)")
		}
		if (buffer[4] & 0b00000100) > 0 {
			faults = append(faults, "Coolant temp (low voltage)")
		}
		if (buffer[4] & 0b00000001) > 0 {
			faults = append(faults, "System (low voltage)")
		}
	}
	if len(buffer) >= 6 {
		if (buffer[5] & 0b10000000) > 0 {
			faults = append(faults, "Battery (low voltage)")
		}
		if (buffer[5] & 0b00010000) > 0 {
			faults = append(faults, "Lambda 1 bank 1 (low voltage)")
		}
		if (buffer[5] & 0b00000100) > 0 {
			faults = append(faults, "Throttle pot (low voltage)")
		}
		if (buffer[5] & 0b00000010) > 0 {
			faults = append(faults, "Air intake (low voltage)")
		}
		if (buffer[5] & 0b00000001) > 0 {
			faults = append(faults, "MAP sensor (low voltage)")
		}
	}

	// RAM 590h 4h
	if len(buffer) >= 9 {
		if (buffer[8] & 0b01000000) > 0 {
			faults = append(faults, "Outside air temp (high voltage)")
		}
		if (buffer[8] & 0b00100000) > 0 {
			faults = append(faults, "Power supply (high voltage)")
		}
		if (buffer[8] & 0b00010000) > 0 {
			faults = append(faults, "Oil temperature (high voltage)")
		}
		if (buffer[8] & 0b00000100) > 0 {
			faults = append(faults, "Coolant temperature (high voltage)")
		}
		if (buffer[8] & 0b00000001) > 0 {
			faults = append(faults, "System (high voltage)")
		}
	}
	if len(buffer) >= 10 {
		if (buffer[9] & 0b10000000) > 0 {
			faults = append(faults, "Battery (high voltage)")
		}
		if (buffer[9] & 0b10000) > 0 {
			faults = append(faults, "Lambda 1 bank 1 (high voltage)")
		}
		if (buffer[9] & 0b100) > 0 {
			faults = append(faults, "Throttle pot (high voltage)")
		}
		if (buffer[9] & 0b10) > 0 {
			faults = append(faults, "Intake air temp (high voltage)")
		}
		if (buffer[9] & 0b1) > 0 {
			faults = append(faults, "MAP sensor (high voltage)")
		}
	}

	// 14h 4h
	if len(buffer) >= 13 {
		if ((buffer[12] >> 6) & 1) > 0 {
			faults = append(faults, "Outside temp sensor (present)")
		}
		if ((buffer[12] >> 5) & 1) > 0 {
			faults = append(faults, "Power supply (present)")
		}
		if ((buffer[12] >> 4) & 1) > 0 {
			faults = append(faults, "Oil temp (present)")
		}
		if ((buffer[12] >> 2) & 1) > 0 {
			faults = append(faults, "Coolant temp (present)")
		}
		if ((buffer[12] >> 2) & 1) > 0 {
			faults = append(faults, "System voltage (present)")
		}
	}
	if len(buffer) >= 14 {
		if ((buffer[13] >> 7) & 1) > 0 {
			faults = append(faults, "Battery voltage (present)")
		}
		if ((buffer[13] >> 4) & 1) > 0 {
			faults = append(faults, "Lambda 1 bank 1 (present)")
		}
		if ((buffer[13] >> 2) & 1) > 0 {
			faults = append(faults, "Throttle pot (present)")
		}
		if ((buffer[13] >> 1) & 1) > 0 {
			faults = append(faults, "Intake air temp (present)")
		}
		if ((buffer[13] >> 0) & 1) > 0 {
			faults = append(faults, "MAP sensor (present)")
		}
	}

	// 598h 4h
	if len(buffer) >= 24 {
		if (buffer[23] & 0b1000) > 0 {
			faults = append(faults, "MAP sensor (present 2)")
		}
		if (buffer[23] & 0b100) > 0 {
			faults = append(faults, "Oil temp (present 2)")
		}
		if (buffer[23] & 0b10) > 0 {
			faults = append(faults, "Intake air temp (present 2)")
		}
		if (buffer[23] & 0b1) > 0 {
			faults = append(faults, "Coolant temp (present 2)")
		}
	}

	// 374h 2h
	if len(buffer) >= 26 {
		if (buffer[25] & 0b1000) > 0 {
			faults = append(faults, "MAP sensor (historic)")
		}
		if (buffer[25] & 0b100) > 0 {
			faults = append(faults, "Oil temp (historic)")
		}
		if (buffer[25] & 0b10) > 0 {
			faults = append(faults, "Intake air temp (historic)")
		}
		if (buffer[25] & 0b1) > 0 {
			faults = append(faults, "Coolant temp (historic)")
		}
	}

	// 5B0h 2h
	if len(buffer) >= 27 {
		if ((buffer[26] >> 0) & 1) > 0 {
			faults = append(faults, "Road speed sensor (present)")
		}
		if ((buffer[26] >> 1) & 1) > 0 {
			faults = append(faults, "Comm. with AT (present)")
		}
		if ((buffer[26] >> 4) & 1) > 0 {
			faults = append(faults, "Bank 1 fuel feedback (present)")
		}
		if ((buffer[26] >> 5) & 1) > 0 {
			faults = append(faults, "Bank 2 fuel feedback (present)")
		}
	}

	// 513h 1h
	if len(buffer) >= 29 {
		if (buffer[28] & 0b00000001) > 0 {
			faults = append(faults, "Road speed sensor (historic)")
		}
		if (buffer[28] & 0b00000010) > 0 {
			faults = append(faults, "Comm. with AT (historic)")
		}
		if (buffer[28] & 0b00010000) > 0 {
			faults = append(faults, "Feedback (historic)")
		}
	}

	m.faults = faults
}
