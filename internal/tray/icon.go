//go:build windows || darwin

package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// circlePNG returns an n x n PNG of a filled circle so no binary assets are needed.
func circlePNG(c color.NRGBA, n int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	mid, r := float64(n-1)/2, float64(n)*14/32
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)-mid, float64(y)-mid
			if dx*dx+dy*dy <= r*r {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	var p bytes.Buffer
	png.Encode(&p, img)
	return p.Bytes()
}
