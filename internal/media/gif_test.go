package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
)

var testPalette = color.Palette{color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 255, 0, 255}, color.NRGBA{0, 0, 255, 255}, color.Transparent}

func encodeGIF(t *testing.T, g *gif.GIF) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func framesGIF(n int) *gif.GIF {
	g := &gif.GIF{Config: image.Config{ColorModel: testPalette, Width: 4, Height: 3}, LoopCount: 0}
	for i := 0; i < n; i++ {
		f := image.NewPaletted(image.Rect(0, 0, 4, 3), testPalette)
		for p := range f.Pix {
			f.Pix[p] = uint8(i % 3)
		}
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, i%5)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	return g
}
func requireKind(t *testing.T, err error, kind string) {
	t.Helper()
	var e *PrepareError
	if !errors.As(err, &e) || e.Kind != kind {
		t.Fatalf("期望 %s，实际 %v", kind, err)
	}
}

func TestGIFSamplingAndMetadata(t *testing.T) {
	for _, n := range []int{1, 2, 8, 9, 42, 240} {
		t.Run(string(rune(n+64)), func(t *testing.T) {
			g := framesGIF(n)
			out, err := PrepareMedia(context.Background(), encodeGIF(t, g), true, 8)
			if err != nil {
				t.Fatal(err)
			}
			_, times, total := sampleIndices(g.Delay, 8)
			loop := 0
			if n == 1 {
				loop = -1
			}
			if out.Type != "gif" || out.OriginalFrames != n || len(out.Frames) != min(n, 8) || out.DurationMS != total || out.LoopCount != loop {
				t.Fatalf("元数据错误 %+v", out)
			}
			if out.Frames[0].TimeMS != 0 || out.Frames[len(out.Frames)-1].TimeMS != times[n-1] {
				t.Fatal("首尾帧缺失")
			}
			for i, f := range out.Frames {
				if i > 0 && f.TimeMS <= out.Frames[i-1].TimeMS {
					t.Fatal("时间不唯一有序")
				}
				if f.MIME != "image/png" {
					t.Fatal("MIME错误")
				}
				if _, err := png.Decode(bytes.NewReader(f.Data)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	indices, _, _ := sampleIndices([]int{10000, 0, 0, 0, 0, 0, 0, 0, 0, 0}, 8)
	if len(indices) != 8 || indices[0] != 0 || indices[7] != 9 {
		t.Fatal("不均延时补位失败", indices)
	}
}

func TestGIFCompositingPixels(t *testing.T) {
	for _, disposal := range []byte{gif.DisposalNone, gif.DisposalBackground, gif.DisposalPrevious} {
		// 无透明色的局部调色板：背景恢复必须使用蓝色全局背景，不能清黑。
		palette := testPalette[:3]
		base := image.NewPaletted(image.Rect(0, 0, 3, 1), palette)
		patch := image.NewPaletted(image.Rect(1, 0, 2, 1), palette)
		patch.Pix[0] = 1
		last := image.NewPaletted(image.Rect(2, 0, 3, 1), palette)
		last.Pix[0] = 1
		g := &gif.GIF{Config: image.Config{ColorModel: palette, Width: 3, Height: 1}, BackgroundIndex: 2, Image: []*image.Paletted{base, patch, last}, Delay: []int{1, 1, 1}, Disposal: []byte{1, disposal, 1}}
		out, err := PrepareMedia(context.Background(), encodeGIF(t, g), true, 8)
		if err != nil {
			t.Fatal(err)
		}
		got, err := png.Decode(bytes.NewReader(out.Frames[2].Data))
		if err != nil {
			t.Fatal(err)
		}
		expected := []color.Color{palette[0], palette[1], palette[1]}
		if disposal == gif.DisposalBackground {
			expected[1] = palette[2]
		}
		if disposal == gif.DisposalPrevious {
			expected[1] = palette[0]
		}
		for x, c := range expected {
			if color.NRGBAModel.Convert(got.At(x, 0)) != color.NRGBAModel.Convert(c) {
				t.Fatalf("disposal=%d x=%d got=%v expected=%v", disposal, x, got.At(x, 0), c)
			}
		}
	}
	// 透明局部帧不能抹掉已有红色像素；恢复背景只清该局部区域。
	base := image.NewPaletted(image.Rect(0, 0, 3, 1), testPalette)
	patch := image.NewPaletted(image.Rect(0, 0, 2, 1), testPalette)
	patch.Pix = []byte{3, 1}
	last := image.NewPaletted(image.Rect(2, 0, 3, 1), testPalette)
	last.Pix[0] = 2
	g := &gif.GIF{Config: image.Config{ColorModel: testPalette, Width: 3, Height: 1}, Image: []*image.Paletted{base, patch, last}, Delay: []int{1, 1, 1}, Disposal: []byte{1, 2, 1}}
	out, err := PrepareMedia(context.Background(), encodeGIF(t, g), true, 8)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := png.Decode(bytes.NewReader(out.Frames[1].Data))
	if color.NRGBAModel.Convert(second.At(0, 0)) != testPalette[0] {
		t.Fatal("透明像素覆盖底图")
	}
	third, _ := png.Decode(bytes.NewReader(out.Frames[2].Data))
	_, _, _, a := third.At(0, 0).RGBA()
	if a != 0 {
		t.Fatal("透明背景未恢复")
	}
}

// scanFixture 无需创建大像素缓冲即可验证解码前预算。
func scanFixture(w, h, n, fw, fh int) []byte {
	b := append([]byte("GIF89a"), make([]byte, 7)...)
	binary.LittleEndian.PutUint16(b[6:8], uint16(w))
	binary.LittleEndian.PutUint16(b[8:10], uint16(h))
	for i := 0; i < n; i++ {
		block := make([]byte, 12)
		block[0] = 0x2c
		binary.LittleEndian.PutUint16(block[5:7], uint16(fw))
		binary.LittleEndian.PutUint16(block[7:9], uint16(fh))
		block[10] = 2
		b = append(b, block...)
	}
	return append(b, 0x3b)
}
func TestGIFResourceChecksBeforeDecode(t *testing.T) {
	for _, tc := range []struct {
		data []byte
		kind string
	}{
		{scanFixture(1025, 1, 1, 1, 1), "gif_dimension_limit"},
		{scanFixture(1, 1, 241, 1, 1), "gif_frame_limit"},
		{scanFixture(1024, 1024, 31, 1024, 1024), "gif_pixel_budget"},
		{scanFixture(1024, 1024, 62, 1, 1), "gif_pixel_budget"},
		{scanFixture(1, 1, 1, 2, 1), "gif_invalid"},
	} {
		requireKind(t, scanGIF(context.Background(), tc.data), tc.kind)
	}
	raw := encodeGIF(t, framesGIF(2))
	for i := 0; i < len(raw); i++ {
		if _, err := PrepareMedia(context.Background(), raw[:i], true, 8); err == nil {
			t.Fatalf("截断文件被接纳 %d", i)
		}
	}
	// 结构合法而压缩数据为空，必须在真正解码时拒绝。
	raw = scanFixture(1, 1, 1, 1, 1)
	if err := scanGIF(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	_, err := PrepareMedia(context.Background(), raw, true, 8)
	requireKind(t, err, "gif_invalid")
	_, err = PrepareMedia(context.Background(), encodeGIF(t, framesGIF(1)), false, 8)
	requireKind(t, err, "gif_disabled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = PrepareMedia(ctx, raw, true, 8); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAnimatedPNGRejectedAndStaticPreserved(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 2, 2)))
	raw := b.Bytes()
	out, err := PrepareMedia(context.Background(), raw, true, 8)
	if err != nil || out.Type != "image" || out.Frames[0].MIME != "image/jpeg" {
		t.Fatal(out, err)
	}
	chunk := make([]byte, 12)
	copy(chunk[4:8], "acTL")
	apng := append(append(append([]byte{}, raw[:8]...), chunk...), raw[8:]...)
	_, err = PrepareMedia(context.Background(), apng, true, 8)
	requireKind(t, err, "unsupported_animation")
	_, err = PrepareMedia(context.Background(), []byte("RIFFxxxxWEBP"), true, 8)
	requireKind(t, err, "unsupported_format")
}

func FuzzPrepareGIF(f *testing.F) {
	f.Add(scanFixture(1, 1, 1, 1, 1))
	f.Add([]byte("GIF89a"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) <= MaxDownloadBytes {
			_, _ = PrepareMedia(context.Background(), b, true, 8)
		}
	})
}

func TestGIFEncodedBudgetAndScaling(t *testing.T) {
	// 不可压缩的随机调色板画面：下载大小可接受，但导出 PNG 总量超限。
	state := uint32(1)
	next := func() byte { state ^= state << 13; state ^= state >> 17; state ^= state << 5; return byte(state) }
	palette := make(color.Palette, 256)
	for i := range palette {
		palette[i] = color.NRGBA{next(), next(), next(), 255}
	}
	g := &gif.GIF{}
	for i := 0; i < 8; i++ {
		p := image.NewPaletted(image.Rect(0, 0, 512, 512), palette)
		for j := range p.Pix {
			p.Pix[j] = next()
		}
		g.Image = append(g.Image, p)
		g.Delay = append(g.Delay, 10)
	}
	raw := encodeGIF(t, g)
	if len(raw) > MaxDownloadBytes {
		t.Fatal("样例应在下载预算内")
	}
	_, err := PrepareMedia(context.Background(), raw, true, 8)
	requireKind(t, err, "gif_encoded_size_limit")
	large := image.NewPaletted(image.Rect(0, 0, 1024, 512), testPalette)
	raw = encodeGIF(t, &gif.GIF{Image: []*image.Paletted{large}, Delay: []int{1}})
	out, err := PrepareMedia(context.Background(), raw, true, 8)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out.Frames[0].Data))
	if err != nil || cfg.Width != 512 || cfg.Height != 256 {
		t.Fatal(cfg, err)
	}
}
