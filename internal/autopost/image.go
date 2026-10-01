package autopost

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
)

// Размер поста VK 4:5.
const (
	PostW = 1080
	PostH = 1350
)

var (
	navy  = color.RGBA{20, 40, 68, 255}
	navy2 = color.RGBA{31, 62, 102, 255}
)

// Compose кладёт фото «по размеру» (cover) на холст 1080×1350 и сверху оверлей (PNG с прозрачностью).
// photo == nil — фон фирменным градиентом.
func Compose(photo, overlay []byte) ([]byte, error) {
	dst := image.NewRGBA(image.Rect(0, 0, PostW, PostH))
	if len(photo) > 0 {
		src, _, err := image.Decode(bytes.NewReader(photo))
		if err != nil {
			return nil, fmt.Errorf("фото: %w", err)
		}
		drawCover(dst, src)
	} else {
		gradient(dst)
	}
	if len(overlay) > 0 {
		ov, _, err := image.Decode(bytes.NewReader(overlay))
		if err != nil {
			return nil, fmt.Errorf("оверлей: %w", err)
		}
		if ov.Bounds().Dx() != PostW || ov.Bounds().Dy() != PostH {
			return nil, fmt.Errorf("оверлей должен быть %d×%d, а он %d×%d", PostW, PostH, ov.Bounds().Dx(), ov.Bounds().Dy())
		}
		draw.Draw(dst, dst.Bounds(), ov, ov.Bounds().Min, draw.Over)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func gradient(dst *image.RGBA) {
	for y := 0; y < PostH; y++ {
		t := float64(y) / float64(PostH-1)
		c := color.RGBA{
			uint8(float64(navy.R) + t*float64(int(navy2.R)-int(navy.R))),
			uint8(float64(navy.G) + t*float64(int(navy2.G)-int(navy.G))),
			uint8(float64(navy.B) + t*float64(int(navy2.B)-int(navy.B))),
			255,
		}
		for x := 0; x < PostW; x++ {
			dst.SetRGBA(x, y, c)
		}
	}
}

// drawCover масштабирует src билинейно так, чтобы закрыть весь dst, и обрезает по центру.
func drawCover(dst *image.RGBA, src image.Image) {
	sb := src.Bounds()
	sw, sh := float64(sb.Dx()), float64(sb.Dy())
	scale := max(float64(PostW)/sw, float64(PostH)/sh)
	offX := (sw*scale - PostW) / 2
	offY := (sh*scale - PostH) / 2
	for y := 0; y < PostH; y++ {
		fy := (float64(y)+offY+0.5)/scale - 0.5
		for x := 0; x < PostW; x++ {
			fx := (float64(x)+offX+0.5)/scale - 0.5
			dst.SetRGBA(x, y, bilinear(src, sb, fx, fy))
		}
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func bilinear(src image.Image, b image.Rectangle, fx, fy float64) color.RGBA {
	x0 := int(fx)
	y0 := int(fy)
	if fx < 0 {
		x0 = -1
	}
	if fy < 0 {
		y0 = -1
	}
	dx, dy := fx-float64(x0), fy-float64(y0)
	px := func(x, y int) (float64, float64, float64) {
		x = clampInt(x, 0, b.Dx()-1) + b.Min.X
		y = clampInt(y, 0, b.Dy()-1) + b.Min.Y
		r, g, bb, _ := src.At(x, y).RGBA()
		return float64(r >> 8), float64(g >> 8), float64(bb >> 8)
	}
	r00, g00, b00 := px(x0, y0)
	r10, g10, b10 := px(x0+1, y0)
	r01, g01, b01 := px(x0, y0+1)
	r11, g11, b11 := px(x0+1, y0+1)
	mix := func(a, b, c, d float64) uint8 {
		v := a*(1-dx)*(1-dy) + b*dx*(1-dy) + c*(1-dx)*dy + d*dx*dy
		return uint8(clampInt(int(v+0.5), 0, 255))
	}
	return color.RGBA{mix(r00, r10, r01, r11), mix(g00, g10, g01, g11), mix(b00, b10, b01, b11), 255}
}
