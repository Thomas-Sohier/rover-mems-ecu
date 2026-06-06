//go:build linux

package serial

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GetPortsList returns available serial ports on the system.
func GetPortsList() ([]string, error) {
	output := []string{}

	dir, err := os.Open("/dev/")
	if err != nil {
		return nil, fmt.Errorf("open /dev/: %w", err)
	}
	defer dir.Close()

	if err := filepath.Walk(dir.Name(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(info.Name(), "ttyUSB") || strings.HasPrefix(info.Name(), "ttyACM") {
			output = append(output, "/dev/"+info.Name())
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk /dev/: %w", err)
	}

	return output, nil
}
