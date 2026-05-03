// Package wallpaper decodes the Omarchy wallpaper symlink and runs the same γ pipeline as Python.
package wallpaper

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"

	"github.com/ericdahl-dev/omarchy-wled/internal/paths"
	xdraw "golang.org/x/image/draw"
)

// AlternateSymlinkPath skips the real ~/.config/.../background path (unit tests point at a temp symlink).
var AlternateSymlinkPath string

// CurrentSymlink returns the path we watch (live Omarchy tree or test override).
func CurrentSymlink() string {
	if AlternateSymlinkPath != "" {
		return AlternateSymlinkPath
	}
	return paths.BackgroundLink
}

var wallpaperSrgbToLinearByte, wallpaperLinearToSrgbByte [256]uint8

func init() {
	for channel := 0; channel < 256; channel++ {
		v := float64(channel)
		wallpaperSrgbToLinearByte[channel] = uint8(math.Round(math.Pow(v/255.0, 2.2) * 255.0))
		wallpaperLinearToSrgbByte[channel] = uint8(math.Round(math.Pow(v/255.0, 1.0/2.2) * 255.0))
	}
}

// AverageSRGBFromFile is the wallpaper “average” after γ decode → 1×1 Catmull-Rom → γ encode.
func AverageSRGBFromFile(wallpaperSymlinkPath string) ([3]uint8, error) {
	resolvedImagePath, err := filepath.EvalSymlinks(wallpaperSymlinkPath)
	if err != nil {
		return [3]uint8{}, fmt.Errorf("cannot resolve wallpaper symlink: %w", err)
	}

	file, err := os.Open(resolvedImagePath)
	if err != nil {
		return [3]uint8{}, err
	}
	defer file.Close()

	decoded, _, err := image.Decode(file)
	if err != nil {
		return [3]uint8{}, fmt.Errorf("cannot decode image %s: %w", resolvedImagePath, err)
	}

	bounds := decoded.Bounds()
	if bounds.Dx()*bounds.Dy() == 0 {
		return [3]uint8{}, fmt.Errorf("empty image")
	}

	rgbaWorking := image.NewRGBA(bounds)
	draw.Draw(rgbaWorking, bounds, decoded, bounds.Min, draw.Src)
	return averageFromRGBA(rgbaWorking)
}

func averageFromRGBA(rgba *image.RGBA) ([3]uint8, error) {
	bounds := rgba.Bounds()
	if bounds.Dx()*bounds.Dy() == 0 {
		return [3]uint8{}, fmt.Errorf("empty image")
	}
	work := image.NewRGBA(bounds)
	draw.Draw(work, bounds, rgba, bounds.Min, draw.Src)
	pixels := work.Pix
	for i := 0; i < len(pixels); i += 4 {
		pixels[i+0] = wallpaperSrgbToLinearByte[pixels[i+0]]
		pixels[i+1] = wallpaperSrgbToLinearByte[pixels[i+1]]
		pixels[i+2] = wallpaperSrgbToLinearByte[pixels[i+2]]
	}
	onePixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	xdraw.CatmullRom.Scale(onePixel, onePixel.Bounds(), work, bounds, draw.Src, nil)
	linearAverage := onePixel.RGBAAt(0, 0)
	return [3]uint8{
		wallpaperLinearToSrgbByte[linearAverage.R],
		wallpaperLinearToSrgbByte[linearAverage.G],
		wallpaperLinearToSrgbByte[linearAverage.B],
	}, nil
}

func cropRGBA(src *image.RGBA, r image.Rectangle) *image.RGBA {
	r = r.Intersect(src.Bounds())
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
	return dst
}

// LeftRightHalvesLinearAvg returns linear-space averages for the left and right image halves.
func LeftRightHalvesLinearAvg(wallpaperSymlinkPath string) (left, right [3]uint8, err error) {
	resolvedImagePath, err := filepath.EvalSymlinks(wallpaperSymlinkPath)
	if err != nil {
		return left, right, fmt.Errorf("cannot resolve wallpaper symlink: %w", err)
	}
	file, err := os.Open(resolvedImagePath)
	if err != nil {
		return left, right, err
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	if err != nil {
		return left, right, fmt.Errorf("cannot decode image %s: %w", resolvedImagePath, err)
	}
	bounds := decoded.Bounds()
	if bounds.Dx()*bounds.Dy() == 0 {
		return left, right, fmt.Errorf("empty image")
	}
	rgbaFull := image.NewRGBA(bounds)
	draw.Draw(rgbaFull, bounds, decoded, bounds.Min, draw.Src)
	if bounds.Dx() < 2 {
		c, err := averageFromRGBA(rgbaFull)
		return c, c, err
	}
	midX := bounds.Min.X + bounds.Dx()/2
	leftRect := image.Rect(bounds.Min.X, bounds.Min.Y, midX, bounds.Max.Y)
	rightRect := image.Rect(midX, bounds.Min.Y, bounds.Max.X, bounds.Max.Y)
	left, err = averageFromRGBA(cropRGBA(rgbaFull, leftRect))
	if err != nil {
		return left, right, err
	}
	right, err = averageFromRGBA(cropRGBA(rgbaFull, rightRect))
	return left, right, err
}

func columnAverageRow(rgbaFull *image.RGBA) *image.RGBA {
	b := rgbaFull.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, 1))
	n := float64(h)
	for x := 0; x < w; x++ {
		var sumR, sumG, sumB float64
		for y := 0; y < h; y++ {
			c := rgbaFull.RGBAAt(b.Min.X+x, b.Min.Y+y)
			sumR += float64(wallpaperSrgbToLinearByte[c.R])
			sumG += float64(wallpaperSrgbToLinearByte[c.G])
			sumB += float64(wallpaperSrgbToLinearByte[c.B])
		}
		lr := uint8(math.Round(sumR / n))
		lg := uint8(math.Round(sumG / n))
		lb := uint8(math.Round(sumB / n))
		out.Set(x, 0, color.RGBA{
			R: wallpaperLinearToSrgbByte[lr],
			G: wallpaperLinearToSrgbByte[lg],
			B: wallpaperLinearToSrgbByte[lb],
			A: 255,
		})
	}
	return out
}

// ColumnStripForLEDCount builds one sample per wallpaper column, then resamples to ledCount LEDs.
func ColumnStripForLEDCount(wallpaperSymlinkPath string, ledCount int) ([][3]uint8, error) {
	if ledCount <= 0 {
		return nil, fmt.Errorf("ledCount must be positive")
	}
	resolvedImagePath, err := filepath.EvalSymlinks(wallpaperSymlinkPath)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve wallpaper symlink: %w", err)
	}
	file, err := os.Open(resolvedImagePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("cannot decode image %s: %w", resolvedImagePath, err)
	}
	bounds := decoded.Bounds()
	if bounds.Dx()*bounds.Dy() == 0 {
		return nil, fmt.Errorf("empty image")
	}
	rgbaFull := image.NewRGBA(bounds)
	draw.Draw(rgbaFull, bounds, decoded, bounds.Min, draw.Src)
	rowRgba := columnAverageRow(rgbaFull)

	pix := rowRgba.Pix
	for i := 0; i < len(pix); i += 4 {
		pix[i+0] = wallpaperSrgbToLinearByte[pix[i+0]]
		pix[i+1] = wallpaperSrgbToLinearByte[pix[i+1]]
		pix[i+2] = wallpaperSrgbToLinearByte[pix[i+2]]
	}

	outStrip := image.NewRGBA(image.Rect(0, 0, ledCount, 1))
	xdraw.CatmullRom.Scale(outStrip, outStrip.Bounds(), rowRgba, rowRgba.Bounds(), draw.Src, nil)

	out := make([][3]uint8, ledCount)
	for x := 0; x < ledCount; x++ {
		c := outStrip.RGBAAt(x, 0)
		out[x] = [3]uint8{
			wallpaperLinearToSrgbByte[c.R],
			wallpaperLinearToSrgbByte[c.G],
			wallpaperLinearToSrgbByte[c.B],
		}
	}
	return out, nil
}
