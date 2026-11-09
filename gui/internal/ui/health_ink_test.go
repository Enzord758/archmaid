package ui

import (
	"image"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// iconInk renders the window and returns the colour of the icon's most
// ink-like pixel (the one furthest from the background around it, which
// is what the icon's solid parts are painted with) and the colour the
// icon's CSS resolves to. ok is false in a build without screenshots.
func iconInk(c *covEnv) (ink [3]int, css [3]int, ok bool) {
	if !shotsBuilt {
		return ink, css, false
	}
	w := c.w
	img, err := windowImage(w)
	x, y, r, b, has := alignBounds(w, w.health.icon)
	if err != nil || !has {
		c.ok(false, "no image of the icon: %v (bounds %v)", err, has)
		return ink, css, false
	}
	rgb := func(img image.Image, px, py int) [3]int {
		pr, pg, pb, _ := img.At(px, py).RGBA()
		return [3]int{int(pr >> 8), int(pg >> 8), int(pb >> 8)}
	}
	bg := rgb(img, int(x)-3, int(y)-3)
	best := -1
	for py := int(y); py < int(b); py++ {
		for px := int(x); px < int(r); px++ {
			p := rgb(img, px, py)
			if d := inkDist(p, bg); d > best {
				best, ink = d, p
			}
		}
	}
	col := gtk.BaseWidget(w.health.icon).Color()
	a := float64(col.Alpha())
	for i, v := range colourOf(w.health.icon) {
		css[i] = int(float64(v)*a + float64(bg[i])*(1-a) + 0.5) // as painted over the background
	}
	return ink, css, true
}

func inkDist(a, b [3]int) int {
	d := 0
	for i := range a {
		v := a[i] - b[i]
		if v < 0 {
			v = -v
		}
		d += v
	}
	return d
}

// inkIs reports whether the rendered ink is the colour the CSS names (a
// few levels of slack for the anti-aliased edges).
func inkIs(ink, want [3]int) bool { return inkDist(ink, want) <= 36 }
