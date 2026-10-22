package apptheme

import "image"

// RoundCorners clips the generated square icon, preserving the source artwork.
// Taking the minimum alpha makes normalization idempotent when a package is
// exported and imported again. Four samples per axis smooth the small sizes.
func RoundCorners(dst *image.NRGBA) {
	size := dst.Bounds().Dx()
	radius := float64(size) * 0.18
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			inside := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					px, py := float64(x)+(float64(sx)+0.5)/4, float64(y)+(float64(sy)+0.5)/4
					dx, dy := max(radius-px, px-(float64(size)-radius), 0), max(radius-py, py-(float64(size)-radius), 0)
					if dx*dx+dy*dy <= radius*radius {
						inside++
					}
				}
			}
			i := dst.PixOffset(x, y) + 3
			dst.Pix[i] = min(dst.Pix[i], uint8(inside*255/16))
		}
	}
}
