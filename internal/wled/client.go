// Package wled posts JSON state to a WLED device (solid color, per-LED spatial strip, LED count).
package wled

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TestStatePostURL, when set, is used instead of constructing http://<ip>/json/state (integration tests).
var TestStatePostURL string

const maxGradientLEDChunk = 256

// PostSolidJSON sends a single solid RGB with brightness; clears gradient freeze (fx=Solid, frz=false).
func PostSolidJSON(ip string, rgb [3]uint8, brightness int) error {
	url := TestStatePostURL
	if url == "" {
		url = "http://" + ip + "/json/state"
	}
	payload, err := json.Marshal(map[string]any{
		"on":  true,
		"bri": max(0, min(255, brightness)),
		"seg": []map[string]any{
			{
				"fx":  0,
				"frz": false,
				"col": [][]int{{int(rgb[0]), int(rgb[1]), int(rgb[2])}},
			},
		},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMultiStatus {
		return fmt.Errorf("WLED returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func jsonGETURL(ip string, name string) string {
	if TestStatePostURL != "" {
		base := strings.TrimSuffix(TestStatePostURL, "/json/state")
		return base + "/json/" + name
	}
	return "http://" + ip + "/json/" + name
}

var (
	cachedGradientLEDCount    int
	cachedGradientLEDCountFor string
)

// FetchLEDCountFromInfo calls GET /json/info and returns leds.count.
func FetchLEDCountFromInfo(ip string) (int, error) {
	url := jsonGETURL(ip, "info")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("WLED /json/info returned HTTP %d", resp.StatusCode)
	}
	var info struct {
		Leds struct {
			Count int `json:"count"`
		} `json:"leds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return 0, err
	}
	if info.Leds.Count <= 0 {
		return 0, fmt.Errorf("WLED reported invalid LED count")
	}
	return info.Leds.Count, nil
}

// ResolveGradientLEDCount returns configured count if >0, else caches GET /json/info for ip.
func ResolveGradientLEDCount(ip string, configured int) (int, error) {
	if configured > 0 {
		return configured, nil
	}
	if cachedGradientLEDCount > 0 && cachedGradientLEDCountFor == ip {
		return cachedGradientLEDCount, nil
	}
	n, err := FetchLEDCountFromInfo(ip)
	if err != nil {
		return 0, err
	}
	cachedGradientLEDCount = n
	cachedGradientLEDCountFor = ip
	return n, nil
}

func rgbToHex(r, g, b uint8) string {
	return fmt.Sprintf("%02X%02X%02X", r, g, b)
}

// PostSpatialGradientJSON paints one hex per physical LED via seg[].i (chunked for firmware limits).
func PostSpatialGradientJSON(ip string, stripRGB [][3]uint8, brightness int) error {
	if len(stripRGB) == 0 {
		return fmt.Errorf("no strip colors")
	}
	hexes := make([]string, len(stripRGB))
	for i, rgb := range stripRGB {
		hexes[i] = rgbToHex(rgb[0], rgb[1], rgb[2])
	}
	url := jsonGETURL(ip, "state")
	bri := max(0, min(255, brightness))

	for offset := 0; offset < len(hexes); offset += maxGradientLEDChunk {
		end := min(offset+maxGradientLEDChunk, len(hexes))
		chunk := hexes[offset:end]
		var iArr []any
		if offset > 0 {
			iArr = append(iArr, offset)
		}
		for _, h := range chunk {
			iArr = append(iArr, h)
		}
		payload, err := json.Marshal(map[string]any{
			"on":  true,
			"bri": bri,
			"seg": []map[string]any{
				{"i": iArr},
			},
		})
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMultiStatus {
			return fmt.Errorf("WLED returned HTTP %d", resp.StatusCode)
		}
	}
	return nil
}
