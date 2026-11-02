package pcslogin

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// RenderQRCode decodes the official image and renders its original payload,
// never the image-download URL. Explicit colors make dark terminals scannable.
func RenderQRCode(out io.Writer, data []byte) error {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("无法读取二维码图片")
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return fmt.Errorf("无法读取二维码像素")
	}
	result, err := qrcode.NewQRCodeReader().Decode(bitmap, map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true})
	if err != nil {
		return fmt.Errorf("无法识别官方二维码图片")
	}
	matrix, err := qrcode.NewQRCodeWriter().Encode(result.GetText(), gozxing.BarcodeFormat_QR_CODE, 0, 0, nil)
	if err != nil {
		return fmt.Errorf("无法显示终端二维码")
	}
	for y := 0; y < matrix.GetHeight(); y += 2 {
		var line strings.Builder
		line.WriteString("\x1b[30;47m")
		for x := 0; x < matrix.GetWidth(); x++ {
			top := matrix.Get(x, y)
			bottom := y+1 < matrix.GetHeight() && matrix.Get(x, y+1)
			switch {
			case top && bottom:
				line.WriteRune('█')
			case top:
				line.WriteRune('▀')
			case bottom:
				line.WriteRune('▄')
			default:
				line.WriteByte(' ')
			}
		}
		line.WriteString("\x1b[0m\n")
		if _, err := io.WriteString(out, line.String()); err != nil {
			return fmt.Errorf("写入终端二维码失败")
		}
	}
	return nil
}
