package media

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/nfnt/resize"
)

// transmitCellPx approximates a cell's pixel width; used only to bound the
// transmitted image size. The terminal still scales the image into c×r cells.
const transmitCellPx = 12

// KittyStore tracks Kitty image ids per photo and their transmission state.
// It is owned by the root model and shared with KittyRenderer.
type KittyStore struct {
	idByPhoto   map[int64]uint32
	colsByPhoto map[int64]int // cols the current placement was transmitted at
	next        uint32
}

// NewKittyStore returns an empty store. Ids start at 1 (Kitty ids are positive).
func NewKittyStore() *KittyStore {
	return &KittyStore{
		idByPhoto:   make(map[int64]uint32),
		colsByPhoto: make(map[int64]int),
		next:        1,
	}
}

// IDFor returns a stable Kitty image id for a photo, assigning one on first use.
// Ids are kept within 24 bits so they fit the placeholder foreground encoding.
func (s *KittyStore) IDFor(photoID int64) uint32 {
	if id, ok := s.idByPhoto[photoID]; ok {
		return id
	}
	id := s.NewID()
	s.idByPhoto[photoID] = id
	return id
}

// NewID returns an unbound image id for short-lived placements such as modal
// viewers. Unlike IDFor, it does not grow the photo-id mapping.
func (s *KittyStore) NewID() uint32 {
	id := s.next
	s.next++
	if s.next > 0xFFFFFF {
		s.next = 1 // wrap; a TUI session will not hold 16M distinct photos
	}
	return id
}

// Ready reports whether the photo is currently transmitted at the given cols.
func (s *KittyStore) Ready(photoID int64, cols int) bool {
	c, ok := s.colsByPhoto[photoID]
	return ok && c == cols
}

// Placed reports whether the photo has a placement on the terminal, at whatever
// width it went out at. It is what a caller asks when it has to delete a
// placement whose size it no longer knows.
func (s *KittyStore) Placed(photoID int64) bool {
	_, ok := s.colsByPhoto[photoID]
	return ok
}

// MarkTransmitted records that the photo's placement exists at cols.
func (s *KittyStore) MarkTransmitted(photoID int64, cols int) {
	s.colsByPhoto[photoID] = cols
}

// Clear marks every image untransmitted (ids stay stable). Call after deleting
// the live placements (DeleteLiveSeq) so images re-transmit on demand.
func (s *KittyStore) Clear() {
	clear(s.colsByPhoto)
}

// DeleteLiveSeq returns the concatenated delete-by-id sequences (d=I, freeing
// each placement and its data) for the given photo ids that have an assigned
// image id. Unlike a blanket d=A this is unambiguous for the virtual (U=1)
// placements used for Unicode placeholders; see #94. Photo ids without an
// assigned image id are skipped.
func (s *KittyStore) DeleteLiveSeq(photoIDs []int64) string {
	var b strings.Builder
	for _, pid := range photoIDs {
		if id, ok := s.idByPhoto[pid]; ok {
			b.WriteString(DeleteSeq(id))
		}
	}
	return b.String()
}

// Untransmit marks a single photo's placement as gone (after deleting it from
// the terminal), so it re-transmits on demand. The id mapping is kept stable.
func (s *KittyStore) Untransmit(photoID int64) {
	delete(s.colsByPhoto, photoID)
}

// DeleteSeq returns the Kitty sequence that deletes a single image by id and
// frees its data (a=d, d=I), quietly. Used to evict off-screen placements so the
// terminal stays under its image-resource limit.
func DeleteSeq(id uint32) string {
	opts := &kitty.Options{
		Action:          kitty.Delete,
		Delete:          kitty.DeleteID,
		DeleteResources: true,
		ID:              int(id),
		Quite:           2,
	}
	return ansi.KittyGraphics(nil, opts.Options()...)
}

// maxTransmitPx caps the transmitted image width so an extreme cell size or
// column count cannot produce an absurdly large upload.
const maxTransmitPx = 2048

// transmitTargetWidth returns the pixel width to transmit an image at so it
// fills cols cells at the terminal's real cell width. Returns 0 when the width
// should be left unchanged (cell size unknown and no bandwidth bound applies).
//
// When the terminal reports its cell pixel size we size the image to the box's
// true pixel width (scaling up small thumbnails too): terminals such as Ghostty
// render Unicode-placeholder images at the image's own resolution rather than
// stretching them to the c×r cell box, so a small thumbnail otherwise shows up
// shrunken in the corner of its reserved space. When the cell size is unknown we
// fall back to a width bound that only downscales (the prior behavior).
func transmitTargetWidth(curW, cols int, cellW float64) int {
	if cellW > 0 {
		target := int(float64(cols)*cellW + 0.5)
		if target > maxTransmitPx {
			target = maxTransmitPx
		}
		if target <= 0 || target == curW {
			return 0
		}
		return target
	}
	// Unknown cell size: bound bandwidth by downscaling only, never upscale.
	maxPx := cols * transmitCellPx
	if maxPx > 0 && curW > maxPx {
		return maxPx
	}
	return 0
}

// scaleForTransmit resizes img to fill cols cells at the terminal's real cell
// width, preserving aspect ratio. See transmitTargetWidth for the rationale.
func scaleForTransmit(img image.Image, cols int) image.Image {
	cw, _ := CellPx()
	target := transmitTargetWidth(img.Bounds().Dx(), cols, cw)
	if target == 0 {
		return img
	}
	return resize.Resize(uint(target), 0, img, resize.Bilinear) // 0 height preserves aspect
}

// TransmitOriginalSeq sends every source pixel for a full-photo modal. A
// conforming Kitty placement scales the source into its c×r rectangle itself.
func TransmitOriginalSeq(id uint32, img image.Image, cols, rows int) (string, error) {
	var buf bytes.Buffer
	opts := &kitty.Options{
		Action: kitty.TransmitAndPut,
		ID:     int(id),
		// PNG is self-describing: the terminal reads dimensions from the data,
		// so no s=/v= keys are needed (raw RGBA without them is undecodable).
		Format:           kitty.PNG,
		Transmission:     kitty.Direct,
		Chunk:            true,
		VirtualPlacement: true,
		Columns:          cols,
		Rows:             rows,
		Quite:            2,
	}
	if err := kitty.EncodeGraphics(&buf, img, opts); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// TransmitSeq encodes a bounded transmit-and-virtual-place sequence for an
// inline image. Scaling limits bandwidth and also fills terminals that do not
// stretch Unicode-placeholder images to their c×r placement.
func TransmitSeq(id uint32, img image.Image, cols, rows int) (string, error) {
	return TransmitOriginalSeq(id, scaleForTransmit(img, cols), cols, rows)
}

// TransmitAnimationFrameSeq replaces the displayed root animation frame while
// preserving the image and its virtual placement. Retransmitting image data
// with the same id would delete that placement under the Kitty protocol.
func TransmitAnimationFrameSeq(id uint32, img image.Image, cols int) (string, func(), error) {
	img = scaleForTransmit(img, cols)
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	rgb := make([]byte, w*h*3)
	if src, ok := img.(*image.NRGBA); ok {
		for y, dst := bounds.Min.Y, 0; y < bounds.Max.Y; y++ {
			row := src.Pix[src.PixOffset(bounds.Min.X, y):]
			for x := 0; x < w; x++ {
				rgb[dst] = row[x*4]
				rgb[dst+1] = row[x*4+1]
				rgb[dst+2] = row[x*4+2]
				dst += 3
			}
		}
	} else {
		for y, dst := bounds.Min.Y, 0; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				rgb[dst] = byte(r >> 8)
				rgb[dst+1] = byte(g >> 8)
				rgb[dst+2] = byte(b >> 8)
				dst += 3
			}
		}
	}

	data := rgb
	var compressed bytes.Buffer
	zw, err := zlib.NewWriterLevel(&compressed, zlib.BestSpeed)
	if err != nil {
		return "", nil, err
	}
	if _, err := zw.Write(rgb); err != nil {
		return "", nil, err
	}
	if err := zw.Close(); err != nil {
		return "", nil, err
	}
	compressedData := compressed.Len() < len(rgb)
	if compressedData {
		data = compressed.Bytes()
	}

	shm, err := os.CreateTemp("/dev/shm", "tele-kitty-")
	if err != nil {
		return "", nil, fmt.Errorf("create Kitty shared memory: %w", err)
	}
	shmPath := shm.Name()
	cleanup := func() { _ = os.Remove(shmPath) }
	if _, err := shm.Write(data); err != nil {
		_ = shm.Close()
		cleanup()
		return "", nil, fmt.Errorf("write Kitty shared memory: %w", err)
	}
	if err := shm.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close Kitty shared memory: %w", err)
	}

	opts := &kitty.Options{
		Action:       kitty.Frame,
		ID:           int(id),
		Format:       kitty.RGB,
		ImageWidth:   w,
		ImageHeight:  h,
		Transmission: kitty.SharedMemory,
		Size:         len(data),
		Rows:         1, // r=1 edits the root frame.
		OffsetX:      1, // X=1 replaces pixels instead of alpha blending.
		// Kitty 0.45.0-0.48.2 incorrectly reads C instead of X for a=f.
		DoNotMoveCursor: true,
	}
	if compressedData {
		opts.Compression = kitty.Zlib
	}
	shmName := "/" + filepath.Base(shmPath)
	payload := []byte(base64.StdEncoding.EncodeToString([]byte(shmName)))
	options := append(opts.Options(), "N=1") // video frames are transient cache data.
	return ansi.KittyGraphics(payload, options...), cleanup, nil
}

// KittyRenderer emits Unicode-placeholder grids that reference images already
// transmitted to the terminal via KittyStore. Output is cached per (photo, cols).
type KittyRenderer struct {
	store *KittyStore
	cache *renderCache
}

// NewKittyRenderer returns a renderer backed by the given store.
func NewKittyRenderer(store *KittyStore) *KittyRenderer {
	return &KittyRenderer{
		store: store,
		cache: newRenderCache(),
	}
}

// Render returns placeholder lines, or nil if the image is not yet transmitted
// at this width (caller shows the text placeholder meanwhile).
func (r *KittyRenderer) Render(photoID int64, img image.Image, cols int) []string {
	if !r.store.Ready(photoID, cols) {
		return nil
	}
	k := blockKey{photoID: photoID, cols: cols}
	return r.cache.get(k, func() []string {
		b := img.Bounds()
		rows := PhotoRows(b.Dx(), b.Dy(), cols, CellAspect())
		return placeholderLines(r.store.IDFor(photoID), cols, rows)
	})
}

// RenderWindow returns the placeholder cells for a centered sub-rectangle of the
// image transmitted at coverCols wide, or nil until it is transmitted at that
// width. img/coverRows are unused (the placement already holds the pixels); they
// keep the signature uniform with the block renderer.
func (r *KittyRenderer) RenderWindow(photoID int64, _ image.Image, coverCols, _, hOff, vOff, winCols, winRows int) []string {
	if !r.store.Ready(photoID, coverCols) {
		return nil
	}
	return PlaceholderWindow(r.store.IDFor(photoID), hOff, vOff, winCols, winRows)
}

// Reset clears the placeholder cache (call on width change).
func (r *KittyRenderer) Reset() {
	r.cache.reset()
}

// PlaceholderLines exposes the Kitty Unicode-placeholder grid for a transmitted
// image id, for callers (e.g. a fullscreen video overlay) that compose their own
// layout rather than going through KittyRenderer.
func PlaceholderLines(id uint32, cols, rows int) []string {
	return placeholderLines(id, cols, rows)
}

// placeholderLines builds the rows×cols grid of Kitty Unicode placeholder cells.
// Each cell is the placeholder rune followed by its row and column diacritics;
// the image id is carried in the 24-bit foreground color. Every cell carries
// explicit (row, col) so the message list's line slicing shows the correct
// vertical slice when a photo is partially scrolled.
func placeholderLines(id uint32, cols, rows int) []string {
	return PlaceholderWindow(id, 0, 0, cols, rows)
}

// PlaceholderWindow builds winRows lines of winCols Kitty Unicode placeholder
// cells that reference the sub-rectangle [vOff, vOff+winRows) x [hOff,
// hOff+winCols) of the image transmitted under id. Each cell carries explicit
// (row, col) diacritics, so the terminal draws exactly that slice of the
// placement — the same mechanism the message list uses for partial-scroll
// slicing, here used to crop a mosaic tile to a centered window. hOff/vOff of 0
// with winCols/winRows equal to the placement size yields the whole image.
func PlaceholderWindow(id uint32, hOff, vOff, winCols, winRows int) []string {
	fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm",
		byte((id>>16)&0xff), byte((id>>8)&0xff), byte(id&0xff))
	lines := make([]string, winRows)
	for r := 0; r < winRows; r++ {
		var sb strings.Builder
		sb.WriteString(fg)
		rd := kitty.Diacritic(vOff + r)
		for c := 0; c < winCols; c++ {
			sb.WriteRune(kitty.Placeholder)
			sb.WriteRune(rd)
			sb.WriteRune(kitty.Diacritic(hOff + c))
		}
		sb.WriteString("\x1b[0m")
		lines[r] = sb.String()
	}
	return lines
}
