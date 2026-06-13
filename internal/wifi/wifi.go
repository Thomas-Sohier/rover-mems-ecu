// Package wifi provides helpers to enable and disable the WiFi radio via rfkill.
package wifi

import (
	"fmt"
	"log"
	"os/exec"
)

// EnableWifi unblocks the WiFi radio: `rfkill unblock wifi`.
func EnableWifi() error {
	if err := run("unblock", "wifi"); err != nil {
		return fmt.Errorf("wifi: enable: %w", err)
	}
	log.Print("wifi: enabled")
	return nil
}

// DisableWifi blocks the WiFi radio: `rfkill block wifi`.
func DisableWifi() error {
	if err := run("block", "wifi"); err != nil {
		return fmt.Errorf("wifi: disable: %w", err)
	}
	log.Print("wifi: disabled")
	return nil
}

func run(args ...string) error {
	out, err := exec.Command("rfkill", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}
