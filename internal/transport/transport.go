// Package transport defines the Transport interface for delivering colors to an LED device.
package transport

// Transport abstracts how prepared colors are sent to an LED device.
// Implementations include HTTP/WLED (default) and USB serial (ESP32 custom firmware).
type Transport interface {
	// PostSolid sends a single solid RGB color at the given brightness (0–255).
	PostSolid(rgb [3]uint8, brightness int) error
	// PostGradient sends a per-LED color strip at the given brightness (0–255).
	PostGradient(strip [][3]uint8, brightness int) error
	// ResolveGradientLEDCount returns the number of LEDs to use for gradient mode.
	// configured is the user-specified count; 0 means auto-detect if the transport supports it.
	ResolveGradientLEDCount(configured int) (int, error)
}
