package mems2j

import (
	"fmt"
	"strings"
	"time"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/pkg/utils"
)

func (m *MEMS2J) parseResponse(actualData []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if utils.SlicesEqual(actualData, wokeResponse) {
		m.logDebug("< ECU woke up")
		m.connected = true
		time.Sleep(50 * time.Millisecond)
		return
	}
	if utils.SlicesEqual(actualData, startDiagResponse) {
		m.logDebug("< Diag mode accepted")
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], seedResponse) {
		m.logDebug("< seed")
		m.seed = int(actualData[2]) << 8
		m.seed += int(actualData[3])
		if m.seed == 0 {
			m.logDebug("Already logged in (seed was 0)")
			m.key = 0
		} else {
			m.key = ecu.GenerateKey(m.seed)
		}
		return
	}
	if utils.SlicesEqual(actualData, keyAcceptResponse) {
		m.logDebug("< Key accepted")
		return
	}
	if utils.SlicesEqual(actualData, pongResponse) {
		m.logDebug("< PONG")
		return
	}
	if utils.SlicesEqual(actualData, faultsClearedResponse) {
		m.logDebug("< FAULTS CLEARED")
		m.alert = "ECU reports faults cleared"
		return
	}

	if utils.SlicesEqual(actualData, responseLearnImmoCommand) {
		m.logDebug("< IMMO CODE LEARN")
		m.alert = "ECU reports set to learn new immo code"
		return
	}

	if len(actualData) >= 2 && utils.SlicesEqual(actualData[0:2], faultsResponse) {
		m.logDebug("< Faults")
		m.parseFaultsLocked(actualData)
		return
	}
	if len(actualData) >= 2 && utils.SlicesEqual(actualData[0:2], responseData00) {
		m.logDebug("got data packet 00")
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], responseData01) {
		m.logDebug("got data packet 01")
		coolant := int(actualData[2]) << 8
		coolant += int(actualData[3])
		coolantFloat := float32(coolant) - 2732
		coolantFloat /= 10
		m.data["coolant_temp"] = coolantFloat
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], responseData02) {
		m.logDebug("got data packet 02")
		oiltemp := int(actualData[2]) << 8
		oiltemp += int(actualData[3])
		oiltempFloat := float32(oiltemp) - 2732
		oiltempFloat /= 10
		m.data["oil_temp"] = oiltempFloat
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], responseData03) {
		m.logDebug("got data packet 03")
		iat := int(actualData[2]) << 8
		iat += int(actualData[3])
		iatFloat := float32(iat) - 2732
		iatFloat /= 10
		m.data["intake_air_temp"] = iatFloat
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], responseData05) {
		m.logDebug("got data packet 05")
		fueltemp := int(actualData[2]) << 8
		fueltemp += int(actualData[3])
		m.data["fuel_temp"] = float32(fueltemp)
		return
	}
	if len(actualData) >= 2 && utils.SlicesEqual(actualData[0:2], responseData06) {
		m.logDebug("got data packet 06")
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], responseData07) {
		m.logDebug("got data packet 07")
		mapkpa := int(actualData[2]) << 8
		mapkpa += int(actualData[3])
		m.data["map_sensor_kpa"] = float32(mapkpa) / 100
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], responseData08) {
		m.logDebug("got data packet 08")
		tps := int(actualData[2]) << 8
		tps += int(actualData[3])
		tpsFloat := float32(tps) / 100
		m.data["tps_degrees"] = tpsFloat
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], responseData09) {
		m.logDebug("got data packet 09")
		rpm := int(actualData[2]) << 8
		rpm += int(actualData[3])
		m.data["rpm"] = float32(rpm)
		return
	}
	if len(actualData) >= 6 && utils.SlicesEqual(actualData[0:2], responseData0A) {
		m.logDebug("got data packet 0A")
		feedback := int(actualData[2]) << 8
		feedback += int(actualData[3])
		feedbackFloat := float32(feedback) / 100
		o2mv := int(actualData[4]) << 8
		o2mv += int(actualData[5])
		airFuel := ((float32(o2mv) / 1000) * 2) + 10
		m.data["fuelling_feedback_percent"] = feedbackFloat
		m.data["o2_mv"] = float32(o2mv)
		m.data["estimate_air_fuel"] = airFuel
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], responseData0B) {
		m.logDebug("got data packet 0B")
		m.data["coil_1_charge_time"] = float32(actualData[2]) / 1000
		m.data["coil_2_charge_time"] = float32(actualData[3]) / 1000
		return
	}
	if len(actualData) >= 6 && utils.SlicesEqual(actualData[0:2], responseData0C) {
		m.logDebug("got data packet 0C")
		m.data["injector_1_pw"] = float32(actualData[2])
		m.data["injector_2_pw"] = float32(actualData[3])
		m.data["injector_3_pw"] = float32(actualData[4])
		m.data["injector_4_pw"] = float32(actualData[5])
		return
	}
	if len(actualData) >= 3 && utils.SlicesEqual(actualData[0:2], responseData0D) {
		m.logDebug("got data packet 0D")
		m.data["vehicle_speed"] = float32(actualData[2])
		return
	}
	if len(actualData) >= 3 && utils.SlicesEqual(actualData[0:2], responseData0F) {
		m.logDebug("got data packet 0F")
		m.data["throttle_switch"] = float32(int(actualData[2]) & 1)
		m.data["ignition"] = float32((int(actualData[2]) >> 1) & 1)
		m.data["ac_button"] = float32((int(actualData[2]) >> 3) & 1)
		return
	}
	if len(actualData) >= 6 && utils.SlicesEqual(actualData[0:2], responseData10) {
		m.logDebug("got data packet 10")
		battery := int(actualData[4]) << 8
		battery += int(actualData[5])
		batteryFloat := float32(battery) / 1000
		m.data["battery_voltage"] = batteryFloat
		return
	}
	if len(actualData) >= 3 && utils.SlicesEqual(actualData[0:2], responseData11) {
		m.logDebug("got data packet 11")
		primaryTriggerSync := actualData[2] & 1
		secondaryTriggerSync := (actualData[2] >> 1) & 1
		m.data["primary_trigger_sync"] = float32(1 - primaryTriggerSync)
		m.data["secondary_trigger_sync"] = float32(1 - secondaryTriggerSync)
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], responseData12) {
		m.logDebug("got data packet 12")
		idleValvePos := int(actualData[2]) << 8
		idleValvePos += int(actualData[3])
		idleValveFloat := float32(idleValvePos) / 2
		m.data["idle_valve_pos"] = idleValveFloat
		return
	}
	if len(actualData) >= 3 && utils.SlicesEqual(actualData[0:2], responseData13) {
		m.logDebug("got data packet 13")
		m.data["closed_loop"] = float32(actualData[2] & 0b00000001)
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], responseData21) {
		m.logDebug("got data packet 21")
		rpmError := int(actualData[2]) << 8
		rpmError += int(actualData[3])
		if rpmError > 32768 {
			rpmError -= 65535
		}
		m.data["rpm_error"] = float32(rpmError)
		return
	}
	if len(actualData) >= 4 && utils.SlicesEqual(actualData[0:2], responseData25) {
		m.logDebug("got data packet 25")
		camPercent := int(actualData[2]) << 8
		camPercent += int(actualData[3])
		m.data["cam_percent"] = float32(camPercent)
		return
	}
	if len(actualData) >= 6 && utils.SlicesEqual(actualData[0:2], responseData3A) {
		m.logDebug("got data packet 3A")
		idleTimingOffset := int(actualData[2]) << 8
		idleTimingOffset += int(actualData[3])
		idleTimingOffsetFloat := float32(idleTimingOffset) / 10
		idleAdjusterRpm := int(actualData[4]) << 8
		idleAdjusterRpm += int(actualData[5])
		m.data["idle_timing_offset"] = idleTimingOffsetFloat
		m.data["idle_adjuster_rpm"] = float32(idleAdjusterRpm)
		return
	}

	if actualData[0] == 0x7F {
		msg := "Negative response - 0x7F"
		if len(actualData) >= 2 {
			msg += fmt.Sprintf(" 0x%x", actualData[1])
		}
		if len(actualData) >= 3 {
			msg += fmt.Sprintf(" 0x%x", actualData[2])
		}
		m.logDebug(msg)
		return
	}

	if actualData[0] == 0x63 {
		var msg strings.Builder
		msg.WriteString("Hex:")
		for x := 1; x < len(actualData); x++ {
			msg.WriteString(fmt.Sprintf(" %x", actualData[x]))
		}
		m.logDebug(msg.String())
		m.logDebug("Not running a ROM dump so going back to ping/data collection")
		return
	}

	var msg strings.Builder
	msg.WriteString("Unknown data received - Hex:")
	for x := 0; x < len(actualData); x++ {
		msg.WriteString(fmt.Sprintf(" %x", actualData[x]))
	}
	m.logDebug(msg.String())
}
