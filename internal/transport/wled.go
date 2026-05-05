package transport

import "github.com/ericdahl-dev/omarchy-wled/internal/wled"

// WLEDTransport delivers colors to a WLED device over HTTP.
type WLEDTransport struct {
	IP string
}

// PostSolid sends a solid color to WLED via HTTP POST /json/state.
func (t *WLEDTransport) PostSolid(rgb [3]uint8, brightness int) error {
	return wled.PostSolidJSON(t.IP, rgb, brightness)
}

// PostGradient sends a per-LED spatial gradient to WLED via HTTP POST /json/state.
func (t *WLEDTransport) PostGradient(strip [][3]uint8, brightness int) error {
	return wled.PostSpatialGradientJSON(t.IP, strip, brightness)
}

// ResolveGradientLEDCount returns configured if > 0, else queries WLED /json/info.
func (t *WLEDTransport) ResolveGradientLEDCount(configured int) (int, error) {
	return wled.ResolveGradientLEDCount(t.IP, configured)
}
