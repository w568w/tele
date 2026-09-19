package theme

import (
	"image/color"
	"reflect"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// The three shapes a token can have. Everything else on Theme is metadata.
var (
	colorType    = reflect.TypeFor[color.Color]()
	paletteType  = reflect.TypeFor[[]color.Color]()
	gradientType = reflect.TypeFor[[]GradientStop]()
)

var (
	// tokenFields indexes the tokens by their normalized key, which is how a
	// key from a file is matched: surface_overlay, surface-overlay and
	// surfaceOverlay all reach SurfaceOverlay.
	tokenFields = map[string]reflect.StructField{}
	// tokenOrder keeps the tokens in declaration order, so a dump comes out
	// grouped the way the struct is rather than alphabetized into noise.
	tokenOrder []reflect.StructField
)

func init() {
	typ := reflect.TypeFor[Theme]()
	for i := range typ.NumField() {
		f := typ.Field(i)
		switch f.Type {
		case colorType, paletteType, gradientType:
		default:
			continue // Name and anything else that is not a token
		}
		key := normalize(f.Name)
		if prev, ok := tokenFields[key]; ok {
			// Two fields normalizing to one key would make one of them
			// unreachable from a file, silently.
			panic("theme: token key collision between " + prev.Name + " and " + f.Name)
		}
		tokenFields[key] = f
		tokenOrder = append(tokenOrder, f)
	}
}

// TokenKey returns the file spelling of a token: the snake_case form of the
// field name. It is what a dump writes and what the documentation lists.
func TokenKey(fieldName string) string {
	var b strings.Builder
	for i, r := range fieldName {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// TokenKeys lists every token in file spelling, in declaration order. The key
// list is a contract with the theme files users have written, so it is pinned by
// a golden test.
func TokenKeys() []string {
	out := make([]string, 0, len(tokenOrder))
	for _, f := range tokenOrder {
		out = append(out, TokenKey(f.Name))
	}
	return out
}

// IsNone reports whether a token holds the absence of color rather than a
// color: the attribute is not set, and whatever is behind shows through.
func IsNone(c color.Color) bool { return isNone(c) }

// IsDarkColor reports whether white reads better than black on c. It answers
// for one colour the question IsDark answers for the terminal, and it is what a
// component asks when a canvas is painted behind something the theme does not
// own: the slot says what the terminal is, the canvas says what is actually
// behind the pixels, and the two disagree whenever a theme sits in the slot it
// was not written for.
//
// Only meaningful for a colour: none has nothing behind it to judge, so callers
// screen it with IsNone first.
func IsDarkColor(c color.Color) bool { return luminance(c) <= darkColorMax }

// darkColorMax is the relative luminance where contrast against white and
// against black are equal, from the WCAG ratio: 1.05/(L+0.05) == (L+0.05)/0.05.
// It is not the midpoint, because the ratio is not linear in luminance.
const darkColorMax = 0.1791

// isNone reports whether c is the absence of color rather than a color.
func isNone(c color.Color) bool {
	if c == nil {
		return true
	}
	_, ok := c.(lipgloss.NoColor)
	return ok
}
