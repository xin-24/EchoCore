package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"sort"
)

// Prepared 只包含经过校验并重新编码的画面，不携带来源地址。
type Prepared struct {
	Type                                  string
	Frames                                []Frame
	OriginalFrames, DurationMS, LoopCount int
}
type Frame struct {
	Data   []byte
	MIME   string
	TimeMS int
}
type PrepareError struct{ Kind, Stage string }

func (e *PrepareError) Error() string { return "媒体预处理失败: " + e.Kind }
func fail(kind, stage string) error   { return &PrepareError{kind, stage} }

// PrepareMedia 依据真实字节分类；不将 APNG 的首帧冒充完整动画。
func PrepareMedia(ctx context.Context, data []byte, enabled bool, frames int) (Prepared, error) {
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}
	if len(data) == 0 || len(data) > MaxDownloadBytes {
		return Prepared{}, fail("invalid_size", "validate")
	}
	if bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")) {
		if !enabled {
			return Prepared{}, fail("gif_disabled", "validate")
		}
		if frames < 2 || frames > 8 {
			return Prepared{}, fail("invalid_frame_limit", "validate")
		}
		return prepareGIF(ctx, data, frames)
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return Prepared{}, fail("unsupported_format", "validate")
	}
	if bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		for p := 8; p < len(data); {
			if len(data)-p < 12 {
				return Prepared{}, fail("invalid_png", "validate")
			}
			n := uint64(binary.BigEndian.Uint32(data[p : p+4]))
			if n > uint64(len(data)-p-12) {
				return Prepared{}, fail("invalid_png", "validate")
			}
			if string(data[p+4:p+8]) == "acTL" {
				return Prepared{}, fail("unsupported_animation", "validate")
			}
			p += int(n) + 12
		}
	}
	raw, err := Prepare(data)
	if err != nil {
		return Prepared{}, fail("unsupported_or_invalid", "validate")
	}
	if err = ctx.Err(); err != nil {
		return Prepared{}, err
	}
	return Prepared{Type: "image", OriginalFrames: 1, Frames: []Frame{{Data: raw, MIME: "image/jpeg"}}}, nil
}

// scanGIF 在标准库分配像素前检查结构和预算；压缩内容仍由标准库校验。
func scanGIF(ctx context.Context, data []byte) error {
	bad := func() error { return fail("gif_invalid", "validate") }
	if len(data) < 13 {
		return bad()
	}
	w, h := int(binary.LittleEndian.Uint16(data[6:8])), int(binary.LittleEndian.Uint16(data[8:10]))
	if w < 1 || h < 1 || w > 1024 || h > 1024 {
		return fail("gif_dimension_limit", "validate")
	}
	p := 13
	if data[10]&128 != 0 {
		p += 3 * (1 << ((data[10] & 7) + 1))
	}
	blocks := func() bool {
		for p < len(data) {
			n := int(data[p])
			p++
			if n == 0 {
				return true
			}
			if n > len(data)-p {
				return false
			}
			p += n
		}
		return false
	}
	count, pixels := 0, int64(0)
	for p < len(data) {
		if err := ctx.Err(); err != nil {
			return err
		}
		b := data[p]
		p++
		switch b {
		case 0x3b:
			if count == 0 || p != len(data) {
				return bad()
			}
			return nil
		case 0x21:
			if p >= len(data) {
				return bad()
			}
			p++
			if !blocks() {
				return bad()
			}
		case 0x2c:
			if len(data)-p < 9 {
				return bad()
			}
			x, y := int(binary.LittleEndian.Uint16(data[p:p+2])), int(binary.LittleEndian.Uint16(data[p+2:p+4]))
			fw, fh := int(binary.LittleEndian.Uint16(data[p+4:p+6])), int(binary.LittleEndian.Uint16(data[p+6:p+8]))
			flags := data[p+8]
			p += 9
			if fw < 1 || fh < 1 || x+fw > w || y+fh > h {
				return bad()
			}
			count++
			pixels += int64(fw) * int64(fh)
			if count > 240 {
				return fail("gif_frame_limit", "validate")
			}
			if pixels > 32000000 || int64(w)*int64(h)*int64(count) > 64000000 {
				return fail("gif_pixel_budget", "validate")
			}
			if flags&128 != 0 {
				p += 3 * (1 << ((flags & 7) + 1))
			}
			if p >= len(data) {
				return bad()
			}
			p++
			if !blocks() {
				return bad()
			}
		default:
			return bad()
		}
	}
	return bad()
}

// sampleIndices 使用时间覆盖，补位时选距已选时间最远的帧，平局选较早帧。
func sampleIndices(delay []int, limit int) ([]int, []int, int) {
	times := make([]int, len(delay))
	total := 0
	for i, d := range delay {
		times[i] = total
		total += max(1, d) * 10
	}
	chosen := map[int]bool{0: true, len(delay) - 1: true}
	if len(delay) <= limit {
		for i := range delay {
			chosen[i] = true
		}
	} else {
		for j := 1; j < limit-1; j++ {
			target := total * j / (limit - 1)
			i := sort.Search(len(times), func(i int) bool { return times[i] > target }) - 1
			chosen[max(0, i)] = true
		}
		for len(chosen) < limit {
			best, dist := -1, -1
			for i, t := range times {
				if chosen[i] {
					continue
				}
				nearest := total
				for j := range chosen {
					d := t - times[j]
					if d < 0 {
						d = -d
					}
					nearest = min(nearest, d)
				}
				if nearest > dist {
					best, dist = i, nearest
				}
			}
			chosen[best] = true
		}
	}
	out := make([]int, 0, len(chosen))
	for i := range chosen {
		out = append(out, i)
	}
	sort.Ints(out)
	return out, times, total
}

func prepareGIF(ctx context.Context, data []byte, limit int) (Prepared, error) {
	if err := scanGIF(ctx, data); err != nil {
		return Prepared{}, err
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return Prepared{}, fail("gif_invalid", "decode")
	}
	if err = ctx.Err(); err != nil {
		return Prepared{}, err
	}
	indices, times, total := sampleIndices(g.Delay, limit)
	out := Prepared{Type: "gif", OriginalFrames: len(g.Image), DurationMS: total, LoopCount: g.LoopCount}
	bounds := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	canvas := image.NewNRGBA(bounds)
	background := func(frame *image.Paletted) color.Color {
		// 当前帧具有透明索引时恢复透明背景，否则采用全局背景色。
		for _, c := range frame.Palette {
			_, _, _, a := c.RGBA()
			if a == 0 {
				return color.Transparent
			}
		}
		if p, ok := g.Config.ColorModel.(color.Palette); ok && int(g.BackgroundIndex) < len(p) {
			return p[g.BackgroundIndex]
		}
		return color.Transparent
	}
	draw.Draw(canvas, bounds, &image.Uniform{C: background(g.Image[0])}, image.Point{}, draw.Src)
	next, size := 0, 0
	for i, frame := range g.Image {
		if err = ctx.Err(); err != nil {
			return Prepared{}, err
		}
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		var previous *image.NRGBA
		if disposal == gif.DisposalPrevious {
			previous = image.NewNRGBA(bounds)
			copy(previous.Pix, canvas.Pix)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		if next < len(indices) && indices[next] == i {
			w, h := bounds.Dx(), bounds.Dy()
			if w > 512 || h > 512 {
				if w >= h {
					h, w = max(1, h*512/w), 512
				} else {
					w, h = max(1, w*512/h), 512
				}
			}
			dst := image.NewNRGBA(image.Rect(0, 0, w, h))
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					dst.SetNRGBA(x, y, canvas.NRGBAAt(x*bounds.Dx()/w, y*bounds.Dy()/h))
				}
			}
			var buf bytes.Buffer
			if err = png.Encode(&buf, dst); err != nil {
				return Prepared{}, fail("gif_encode_failed", "encode")
			}
			if err = ctx.Err(); err != nil {
				return Prepared{}, err
			}
			size += buf.Len()
			if size > 4<<20 {
				return Prepared{}, fail("gif_encoded_size_limit", "encode")
			}
			out.Frames = append(out.Frames, Frame{Data: buf.Bytes(), MIME: "image/png", TimeMS: times[i]})
			next++
		}
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, frame.Bounds(), &image.Uniform{C: background(frame)}, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvas = previous
		}
	}
	return out, ctx.Err()
}
