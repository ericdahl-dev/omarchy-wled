// Package color parses Omarchy theme hex entries and scales saturation in HSV space
// before sending sRGB values to WLED.
package color

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
)

// hexAssignmentInColorsToml matches one line of Omarchy colors.toml, e.g. accent = "#82FB9C".
var hexAssignmentInColorsToml = regexp.MustCompile(`(?m)^(\w+)\s*=\s*"#([0-9a-fA-F]{6})"`)

// NamedHexFromColorsToml returns the sRGB triplet for one key in a theme colors.toml file.
func NamedHexFromColorsToml(tomlKey, colorsTomlPath string) ([3]uint8, error) {
	data, err := os.ReadFile(colorsTomlPath)
	if err != nil {
		return [3]uint8{}, err
	}
	for _, m := range hexAssignmentInColorsToml.FindAllStringSubmatch(string(data), -1) {
		key, hex := m[1], m[2]
		if key != tomlKey {
			continue
		}
		r, _ := strconv.ParseUint(hex[0:2], 16, 8)
		g, _ := strconv.ParseUint(hex[2:4], 16, 8)
		b, _ := strconv.ParseUint(hex[4:6], 16, 8)
		return [3]uint8{uint8(r), uint8(g), uint8(b)}, nil
	}
	return [3]uint8{}, fmt.Errorf("%s color not found in %s", tomlKey, colorsTomlPath)
}

// ScaleSaturation converts to HSV, multiplies S by saturationMultiplier, converts back to sRGB.
// multiplier 1.0 leaves hue/value; 0.0 yields grey; >1.0 boosts vividness (S capped at 1).
func ScaleSaturation(rgb [3]uint8, saturationMultiplier float64) [3]uint8 {
	rN := float64(rgb[0]) / 255
	gN := float64(rgb[1]) / 255
	bN := float64(rgb[2]) / 255

	maxChannel := math.Max(rN, math.Max(gN, bN))
	minChannel := math.Min(rN, math.Min(gN, bN))
	chroma := maxChannel - minChannel

	value := maxChannel
	saturation := 0.0
	if maxChannel > 0 {
		saturation = chroma / maxChannel
	}

	hue := 0.0
	if chroma > 0 {
		switch maxChannel {
		case rN:
			hue = (gN - bN) / chroma
			if gN < bN {
				hue += 6
			}
		case gN:
			hue = (bN-rN)/chroma + 2
		default:
			hue = (rN-gN)/chroma + 4
		}
		hue /= 6
	}

	saturation = math.Max(0, math.Min(1, saturation*saturationMultiplier))
	return hsvToRGB(hue, saturation, value)
}

func hsvToRGB(h, s, v float64) [3]uint8 {
	if s == 0 {
		c := uint8(math.Round(v * 255))
		return [3]uint8{c, c, c}
	}
	h6 := h * 6
	if h6 >= 6 {
		h6 = 0
	}
	i := int(h6)
	f := h6 - float64(i)
	p := v * (1 - s)
	q := v * (1 - s*f)
	t := v * (1 - s*(1-f))
	var r, g, b float64
	switch i {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	return [3]uint8{
		uint8(math.Round(r * 255)),
		uint8(math.Round(g * 255)),
		uint8(math.Round(b * 255)),
	}
}
