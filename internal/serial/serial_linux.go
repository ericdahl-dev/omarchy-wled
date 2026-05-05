//go:build linux

package serial

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

var baudTable = map[int]uint32{
	9600:   unix.B9600,
	19200:  unix.B19200,
	38400:  unix.B38400,
	57600:  unix.B57600,
	115200: unix.B115200,
}

// openPort opens and configures a serial port for raw 8N1 I/O at the given baud rate.
func openPort(path string, baud int) (*os.File, error) {
	baudBit, ok := baudTable[baud]
	if !ok {
		return nil, fmt.Errorf("unsupported baud rate %d (supported: 9600 19200 38400 57600 115200)", baud)
	}

	f, err := os.OpenFile(path, os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	fd := int(f.Fd())

	// Switch from non-blocking to blocking I/O.
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("serial set blocking: %w", err)
	}

	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("serial get termios: %w", err)
	}

	// Raw mode (equivalent to cfmakeraw).
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB
	t.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL

	// Set baud rate (clear CBAUD, then OR in the target speed).
	t.Cflag &^= unix.CBAUD
	t.Cflag |= baudBit
	t.Ispeed = baudBit
	t.Ospeed = baudBit

	// Blocking read: return when at least 1 byte is available.
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(fd, unix.TCSETSF, t); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("serial set termios: %w", err)
	}

	return f, nil
}
