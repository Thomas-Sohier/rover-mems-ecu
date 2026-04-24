//go:build linux

package serial

import (
	"os"
	"path/filepath"
	"strings"
)

// GetPortsList returns available serial ports on the system.
func GetPortsList() ([]string, error) {
	output := []string {}

    dir, err := os.Open("/dev/")
    if err != nil {
        panic(err)
    }
    defer dir.Close()

    filepath.Walk(dir.Name(), func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }
        if (strings.HasPrefix(info.Name(), "ttyUSB") || strings.HasPrefix(info.Name(), "ttyACM")) {
        	output = append(output, "/dev/"+info.Name())
        }
        return nil
    })

	return output, nil
}
