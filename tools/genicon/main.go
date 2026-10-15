// genicon — 生成 ZRemote 应用图标 (与 main.go drawIcon 同款几何图形)。
// 用法: go run ./tools/genicon [尺寸]   输出固定写入当前目录 AppIcon.png
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strconv"
)

const outName = "AppIcon.png" // 固定输出: 当前目录, 不接受路径输入

func rrIn(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Min(math.Max(x, x0+r), x1-r)
	cy := math.Min(math.Max(y, y0+r), y1-r)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

func inTri(px, py, ax, ay, bx, by, cx, cy float64) bool {
	d1 := (px-bx)*(ay-by) - (ax-bx)*(py-by)
	d2 := (px-cx)*(by-cy) - (bx-cx)*(py-cy)
	d3 := (px-ax)*(cy-ay) - (cx-ax)*(py-ay)
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

func drawIcon(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	white := color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	blue := color.RGBA{0x4F, 0x8C, 0xFF, 0xFF}
	sx0, sy0, sx1, sy1 := s*0.25, s*0.24, s*0.75, s*0.70
	rad, bw := s*0.05, s*0.055
	ix0, iy0, ix1, iy1 := sx0+bw, sy0+bw, sx1-bw, sy1-bw
	irad := rad * 0.55
	tx1, ty1, tx2, ty2, tx3, ty3 := s*0.43, s*0.335, s*0.43, s*0.605, s*0.585, s*0.47
	dx0, dy0, dx1, dy1, drad := s*0.34, s*0.755, s*0.66, s*0.815, s*0.03
	for y := 0; y < size; y++ {
		fy := float64(y) + 0.5
		for x := 0; x < size; x++ {
			fx := float64(x) + 0.5
			c := blue
			switch {
			case rrIn(fx, fy, dx0, dy0, dx1, dy1, drad):
				c = white
			case rrIn(fx, fy, sx0, sy0, sx1, sy1, rad) && !rrIn(fx, fy, ix0, iy0, ix1, iy1, irad):
				c = white
			case rrIn(fx, fy, ix0, iy0, ix1, iy1, irad) && inTri(fx, fy, tx1, ty1, tx2, ty2, tx3, ty3):
				c = white
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func main() {
	size := 1024
	if len(os.Args) > 1 {
		if v, err := strconv.Atoi(os.Args[1]); err == nil && v >= 16 && v <= 4096 {
			size = v
		}
	}
	if err := os.WriteFile(outName, drawIcon(size), 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("written:", outName, strconv.Itoa(size)+"x"+strconv.Itoa(size))
}
