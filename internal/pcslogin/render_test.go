package pcslogin

import (
	"bytes"
	"image"
	"image/color"
	"io"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

func TestTerminalRenderingDecodesToOriginalLoginPayload(t *testing.T) {
	var out bytes.Buffer
	if err := RenderQRCode(&out, qrImage(t)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	var rows [][]rune
	for _, line := range lines {
		line = strings.TrimPrefix(line, "\x1b[30;47m")
		line = strings.TrimSuffix(line, "\x1b[0m")
		rows = append(rows, []rune(line))
	}
	width := len(rows[0])
	const scale = 5
	img := image.NewGray(image.Rect(0, 0, width*scale, len(rows)*2*scale))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			r := rows[y/(2*scale)][x/scale]
			upper := (y/scale)%2 == 0
			black := r == '█' || r == '▀' && upper || r == '▄' && !upper
			value := uint8(255)
			if black {
				value = 0
			}
			img.SetGray(x, y, color.Gray{Y: value})
		}
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := qrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil {
		t.Fatal("terminal QR cannot be scanned:", err)
	}
	if decoded.GetText() != "https://wappass.baidu.com/wp/?qrlogin&sign=TEST-ONLY" {
		t.Fatal("render changed the official QR payload")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestRenderingErrorsDoNotPrintPayload(t *testing.T) {
	if RenderQRCode(io.Discard, []byte("not an image")) == nil {
		t.Fatal("invalid image accepted")
	}
	if RenderQRCode(failingWriter{}, qrImage(t)) == nil {
		t.Fatal("writer failure ignored")
	}
}
