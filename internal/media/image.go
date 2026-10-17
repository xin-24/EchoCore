package media

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
)

var ErrImage = errors.New("图片格式不支持或超过资源限制")

// Prepare 先检查尺寸再解码；统一为缩放后的 JPEG，避免携带原始文件元数据。
func Prepare(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > MaxDownloadBytes {
		return nil, ErrImage
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16000000 {
		return nil, ErrImage
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrImage
	}
	w, h := cfg.Width, cfg.Height
	const edge = 1600
	if w > edge || h > edge {
		if w >= h {
			h, w = max(1, h*edge/w), edge
		} else {
			w, h = max(1, w*edge/h), edge
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, a := src.At(src.Bounds().Min.X+x*cfg.Width/w, src.Bounds().Min.Y+y*cfg.Height/h).RGBA()
			offset := dst.PixOffset(x, y)
			dst.Pix[offset], dst.Pix[offset+1], dst.Pix[offset+2], dst.Pix[offset+3] = uint8((r+65535-a)>>8), uint8((g+65535-a)>>8), uint8((b+65535-a)>>8), 255
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil || out.Len() > MaxDownloadBytes {
		return nil, ErrImage
	}
	return out.Bytes(), nil
}
