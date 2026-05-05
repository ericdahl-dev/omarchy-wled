// Package serial implements a USB serial transport for an ESP32-based LED controller.
//
// The binary protocol uses two frame types:
//
//	Solid:    0x53 ('S') + R + G + B + Bri                        = 5 bytes
//	Gradient: 0x47 ('G') + N_hi + N_lo + Bri + R0 G0 B0 … RN GN BN = 4+3·N bytes
//
// The ESP32 custom firmware reads these frames and drives the LED strip accordingly.
// Default baud rate is 115200 (8N1); override with -serial-baud.
package serial

import (
	"fmt"
	"os"
)

const defaultBaud = 115200

// Transport delivers color frames to an ESP32 LED controller over USB serial.
type Transport struct {
	// Port is the serial device path, e.g. /dev/ttyUSB0 or /dev/ttyACM0.
	Port string
	// Baud is the baud rate; 0 defaults to 115200.
	Baud int

	// f is the open serial port file; nil before the first call to open().
	f *os.File
}

func (t *Transport) baud() int {
	if t.Baud > 0 {
		return t.Baud
	}
	return defaultBaud
}

func (t *Transport) open() error {
	if t.f != nil {
		return nil
	}
	var err error
	t.f, err = openPort(t.Port, t.baud())
	return err
}

// PostSolid sends a 5-byte solid-color frame to the ESP32.
func (t *Transport) PostSolid(rgb [3]uint8, brightness int) error {
	if err := t.open(); err != nil {
		return err
	}
	bri := clampBrightness(brightness)
	_, err := t.f.Write([]byte{'S', rgb[0], rgb[1], rgb[2], uint8(bri)})
	return err
}

// PostGradient sends a gradient strip frame to the ESP32.
func (t *Transport) PostGradient(strip [][3]uint8, brightness int) error {
	if err := t.open(); err != nil {
		return err
	}
	n := len(strip)
	if n > 0xFFFF {
		return fmt.Errorf("gradient strip too large: %d LEDs (max 65535)", n)
	}
	bri := clampBrightness(brightness)
	frame := make([]byte, 4+3*n)
	frame[0] = 'G'
	frame[1] = uint8(n >> 8)
	frame[2] = uint8(n)
	frame[3] = uint8(bri)
	for i, rgb := range strip {
		frame[4+3*i] = rgb[0]
		frame[4+3*i+1] = rgb[1]
		frame[4+3*i+2] = rgb[2]
	}
	_, err := t.f.Write(frame)
	return err
}

// ResolveGradientLEDCount returns configured if > 0; serial has no auto-detect.
func (t *Transport) ResolveGradientLEDCount(configured int) (int, error) {
	if configured <= 0 {
		return 0, fmt.Errorf("serial transport requires -gradient-leds (LED count cannot be auto-detected over serial)")
	}
	return configured, nil
}

// Close closes the serial port. It is called by main when the program exits
// (e.g. after -once mode) to release the port cleanly. The daemon's indefinite
// loop does not call Close since the process lifetime equals the port lifetime.
func (t *Transport) Close() error {
	if t.f != nil {
		err := t.f.Close()
		t.f = nil
		return err
	}
	return nil
}

func clampBrightness(b int) int {
	if b < 0 {
		return 0
	}
	if b > 255 {
		return 255
	}
	return b
}
