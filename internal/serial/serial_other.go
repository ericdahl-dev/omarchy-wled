//go:build !linux

package serial

import (
	"fmt"
	"os"
)

// openPort is not supported on non-Linux platforms.
func openPort(path string, baud int) (*os.File, error) {
	return nil, fmt.Errorf("serial transport is only supported on Linux")
}
