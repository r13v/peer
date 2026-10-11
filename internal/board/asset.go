package board

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif" // registers the decoder for DecodeConfig
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
)

// Asset is a stored image: its path inside the board and its size.
type Asset struct {
	Asset  string `json:"asset"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// importAsset stores data as assets/<sha256>.<ext>; the caller holds the
// lock. Importing the same bytes again reuses the file.
func importAsset(dir string, data []byte) (Asset, error) {
	if len(data) > MaxAssetSize {
		return Asset{}, &Error{Kind: KindTooLarge, Limit: MaxAssetSize}
	}
	ext, w, h, err := sniff(data)
	if err != nil {
		return Asset{}, err
	}
	assets := filepath.Join(dir, "assets")
	st, err := os.Lstat(assets)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(assets, 0o755); err != nil {
			return Asset{}, err
		}
	case err != nil:
		return Asset{}, err
	case !st.IsDir():
		return Asset{}, invalid("assets", "assets must be a directory, not a symlink or a file")
	}
	// A symlinked parent would put the file outside the board.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Asset{}, err
	}
	if realAssets, err := filepath.EvalSymlinks(assets); err != nil || realAssets != filepath.Join(realDir, "assets") {
		return Asset{}, invalid("assets", "assets resolves outside the board")
	}
	sum := sha256.Sum256(data)
	a := Asset{Asset: "assets/" + hex.EncodeToString(sum[:]) + "." + ext, Width: w, Height: h}
	target := filepath.Join(dir, filepath.FromSlash(a.Asset))
	if st, err := os.Lstat(target); err == nil {
		if !st.Mode().IsRegular() {
			return Asset{}, invalid("assets", "%s is not a regular file", a.Asset)
		}
		return a, nil
	}
	return a, writeAtomic(target, data)
}

var (
	pngMagic  = []byte("\x89PNG\r\n\x1a\n")
	jpegMagic = []byte{0xff, 0xd8, 0xff}
)

// sniff tells the image type by content, never by name, and reads its size.
func sniff(data []byte) (ext string, w, h int, err error) {
	switch {
	case bytes.HasPrefix(data, pngMagic):
		ext = "png"
	case bytes.HasPrefix(data, jpegMagic):
		ext = "jpg"
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		ext = "gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		w, h, err = webpSize(data)
		return "webp", w, h, err
	default:
		return "", 0, 0, unsupportedMedia("only png, jpeg, gif and webp images are accepted")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, unsupportedMedia("cannot read the %s image: %v", ext, err)
	}
	return ext, cfg.Width, cfg.Height, nil
}

// webpSize reads the canvas size from the first chunk header of a WebP
// file; the standard library has no WebP decoder.
func webpSize(data []byte) (int, int, error) {
	bad := unsupportedMedia("cannot read the webp image header")
	if len(data) < 30 {
		return 0, 0, bad
	}
	le24 := func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
	switch string(data[12:16]) {
	case "VP8X": // extended: 24-bit canvas width-1 and height-1
		return 1 + le24(data[24:27]), 1 + le24(data[27:30]), nil
	case "VP8L": // lossless: signature 0x2f, then 14-bit width-1 and height-1
		if data[20] != 0x2f {
			return 0, 0, bad
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		return 1 + int(bits&0x3fff), 1 + int(bits>>14&0x3fff), nil
	case "VP8 ": // lossy: frame tag, start code 9d 01 2a, 14-bit sizes
		if data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, bad
		}
		return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff), nil
	}
	return 0, 0, bad
}
