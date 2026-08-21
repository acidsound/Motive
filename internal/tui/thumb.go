package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"

	// Register decoders so image.Decode handles the common web formats.
	_ "image/gif"
	_ "image/jpeg"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// thumbMaxDim is the longest edge (pixels) of a generated thumbnail. Keeping
// it small keeps the inline payload tiny even though the escape sequence is
// re-emitted on every render frame.
const thumbMaxDim = 64

// kittyChunkSize is the payload limit per kitty graphics chunk. The protocol
// requires chunks at most 4096 bytes of encoded data.
const kittyChunkSize = 4096

// inlineImagesSupported reports whether the current terminal can display
// inline images: iTerm2 via OSC 1337, kitty/ghostty/WezTerm via the kitty
// graphics protocol. Detection is environment-based; terminals that do not
// advertise themselves simply get text chips instead of thumbnails.
func inlineImagesSupported() bool {
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "kitty", "ghostty", "WezTerm":
		return true
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" || os.Getenv("KITTY_PID") != "" {
		return true
	}
	return strings.HasPrefix(os.Getenv("TERM"), "kitty")
}

// thumbnail returns the terminal escape sequence that renders the image file
// at path inline as a small thumbnail, or "" when the terminal does not
// support inline images or the file is not a decodable image. The escape
// sequence is self-contained: emitting it inside a line draws the thumbnail
// at that position.
func thumbnail(path string) string {
	if !inlineImagesSupported() {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 {
		return ""
	}
	if w > thumbMaxDim || h > thumbMaxDim {
		scale := float64(thumbMaxDim) / float64(max(w, h))
		nw, nh := int(float64(w)*scale), int(float64(h)*scale)
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	enc := base64.StdEncoding.EncodeToString(buf.Bytes())
	if os.Getenv("TERM_PROGRAM") == "iTerm.app" {
		return fmt.Sprintf("\x1b]1337;File=inline=1;width=%dpx;height=%dpx;preserveAspectRatio=1:%s\x07", thumbMaxDim, thumbMaxDim, enc)
	}
	return kittyImage(enc)
}

// kittyImage wraps base64-encoded PNG data in the kitty graphics protocol.
// Payloads are chunked at kittyChunkSize with m=1 continuation frames so the
// image also renders in terminals that enforce the 4096-byte chunk limit.
func kittyImage(enc string) string {
	var b strings.Builder
	chunks := 1 + len(enc)/kittyChunkSize
	for i := 0; i < chunks; i++ {
		start := i * kittyChunkSize
		end := min(start+kittyChunkSize, len(enc))
		more := 0
		if end < len(enc) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,m=%d;%s\x1b\\", more, enc[start:end])
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, enc[start:end])
		}
	}
	return b.String()
}
