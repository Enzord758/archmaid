package runview

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RGB is a colour as the view draws it.
type RGB struct{ R, G, B uint8 }

// Hex is "#rrggbb".
func (c RGB) Hex() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// Palette is the 16 ANSI colours: 0–7 normal, 8–15 bright.
type Palette [16]RGB

// FallbackPalette is used when DMS's dank16 can't be read: a fixed
// GNOME-style table (close to GNOME's terminal colours), picked to read on
// both a light and a dark background. Colours 0 and 7 double as the
// background and foreground of reversed text, as they do in dank16.
var FallbackPalette = Palette{
	{0x17, 0x14, 0x21}, {0xc0, 0x1c, 0x28}, {0x26, 0xa2, 0x69}, {0xa2, 0x73, 0x4c},
	{0x12, 0x48, 0x8b}, {0xa3, 0x47, 0xba}, {0x2a, 0xa1, 0xb3}, {0xd0, 0xcf, 0xcc},
	{0x5e, 0x5c, 0x64}, {0xf6, 0x61, 0x51}, {0x33, 0xda, 0x7a}, {0xe9, 0xad, 0x0c},
	{0x2a, 0x7b, 0xde}, {0xc0, 0x61, 0xcb}, {0x33, 0xc7, 0xde}, {0xff, 0xff, 0xff},
}

// Color resolves c (not ColorDefault) to RGB: 0–15 from the palette,
// 16–231 the xterm 6×6×6 cube, 232–255 its grey ramp, RGB as given.
func (p *Palette) Color(c Color) RGB {
	if c.Kind == ColorRGB {
		return RGB{c.R, c.G, c.B}
	}
	n := int(c.Index)
	switch {
	case n < 16:
		return p[n]
	case n < 232:
		n -= 16
		level := func(v int) uint8 {
			if v == 0 {
				return 0
			}
			return uint8(55 + 40*v)
		}
		return RGB{level(n / 36), level(n / 6 % 6), level(n % 6)}
	default:
		v := uint8(8 + 10*(n-232))
		return RGB{v, v, v}
	}
}

// Dank16Path is where DMS writes its colours, dank16 among them:
// $XDG_CACHE_HOME/DankMaterialShell/dms-colors.json, or ~/.cache/… when
// XDG_CACHE_HOME is unset. "" when neither is known.
func Dank16Path() string {
	dir := os.Getenv("XDG_CACHE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".cache")
	}
	return filepath.Join(dir, "DankMaterialShell", "dms-colors.json")
}

// LoadDank16 reads the dank16 palette from DMS's dms-colors.json (read
// only; DMS regenerates it with matugen when the wallpaper or theme
// changes). Each colorN has a "dark", "light" and "default" value; the
// file's "mode" picks dark or light, "default" otherwise. Any colour
// missing or malformed fails the whole load, so a half-read palette never
// mixes with the fallback.
func LoadDank16(path string) (Palette, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Palette{}, err
	}
	var f struct {
		Dank16 map[string]map[string]string `json:"dank16"`
		Mode   string                       `json:"mode"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return Palette{}, err
	}
	mode := f.Mode
	if mode != "dark" && mode != "light" {
		mode = "default"
	}
	var p Palette
	for i := range p {
		v := f.Dank16["color"+strconv.Itoa(i)]
		hex, ok := v[mode]
		if !ok {
			hex, ok = v["default"]
		}
		if !ok {
			return Palette{}, fmt.Errorf("dank16: no color%d", i)
		}
		c, err := parseHex(hex)
		if err != nil {
			return Palette{}, fmt.Errorf("dank16: color%d: %w", i, err)
		}
		p[i] = c
	}
	return p, nil
}

// LoadPalette is dank16 if DMS has one, else FallbackPalette.
func LoadPalette() Palette {
	if path := Dank16Path(); path != "" {
		if p, err := LoadDank16(path); err == nil {
			return p
		}
	}
	return FallbackPalette
}

func parseHex(s string) (RGB, error) {
	h, ok := strings.CutPrefix(s, "#")
	if !ok || len(h) != 6 {
		return RGB{}, errors.New("not #rrggbb: " + strconv.Quote(s))
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return RGB{}, errors.New("not #rrggbb: " + strconv.Quote(s))
	}
	return RGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
}
