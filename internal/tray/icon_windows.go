package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

// makeIcon returns a 32x32 filled-circle .ico (PNG payload) so no binary assets are needed.
func makeIcon(c color.NRGBA) []byte {
	const n = 32
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)-15.5, float64(y)-15.5
			if dx*dx+dy*dy <= 14*14 {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	var p bytes.Buffer
	png.Encode(&p, img)

	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, []uint16{0, 1, 1}) // ICONDIR: reserved, type=icon, count
	b.Write([]byte{n, n, 0, 0})                              // width, height, palette, reserved
	binary.Write(&b, binary.LittleEndian, []uint16{1, 32})   // planes, bits per pixel
	binary.Write(&b, binary.LittleEndian, uint32(p.Len()))   // image size
	binary.Write(&b, binary.LittleEndian, uint32(22))        // image offset
	b.Write(p.Bytes())
	return b.Bytes()
}
