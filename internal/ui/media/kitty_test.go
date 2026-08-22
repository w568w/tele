package media_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/sorokin-vladimir/tele/internal/ui/media"
	"github.com/stretchr/testify/require"
)

func TestPlaceholderLines_Shape(t *testing.T) {
	lines := media.PlaceholderLines(7, 5, 3)
	if len(lines) != 3 {
		t.Fatalf("want 3 rows, got %d", len(lines))
	}
}

func TestKittyStore_IDForStableAndMonotonic(t *testing.T) {
	s := media.NewKittyStore()
	a := s.IDFor(100)
	b := s.IDFor(200)
	require.Equal(t, a, s.IDFor(100), "same photo keeps its id")
	require.NotEqual(t, a, b, "different photos get different ids")
	require.Greater(t, a, uint32(0), "ids are positive")
}

func TestKittyStore_ReadyTracksTransmission(t *testing.T) {
	s := media.NewKittyStore()
	require.False(t, s.Ready(100, 30))
	s.MarkTransmitted(100, 30)
	require.True(t, s.Ready(100, 30))
	require.False(t, s.Ready(100, 40), "different cols is not ready")
}

func TestKittyStore_ClearResetsTransmissionButKeepsIDs(t *testing.T) {
	s := media.NewKittyStore()
	id := s.IDFor(100)
	s.MarkTransmitted(100, 30)
	s.Clear()
	require.False(t, s.Ready(100, 30), "clear marks images untransmitted")
	require.Equal(t, id, s.IDFor(100), "ids remain stable across clear")
}

func TestKittyStore_DeleteSeqIsKittyAPC(t *testing.T) {
	seq := media.DeleteSeq(7)
	require.True(t, strings.HasPrefix(seq, "\x1b_G"), "starts with Kitty APC")
	require.Contains(t, seq, "a=d")
	require.Contains(t, seq, "d=I") // delete by id and free data (unambiguous for virtual placements)
	require.True(t, strings.HasSuffix(seq, "\x1b\\"), "ends with ST")
}

func TestKittyStore_TransmitSeqContainsIDAndPlacement(t *testing.T) {
	s := media.NewKittyStore()
	img := sampleImage(16, 16)
	id := s.IDFor(100)
	seq, err := media.TransmitSeq(id, img, 8, 4)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(seq, "\x1b_G"))
	require.Contains(t, seq, "a=T") // transmit and put
	require.Contains(t, seq, "U=1") // virtual placement
	require.Contains(t, seq, "c=8") // columns
	require.Contains(t, seq, "r=4") // rows
}

// TestKittyStore_TransmitSeqIsDecodable guards the requirement that the
// transmitted image carries its own dimensions: either a self-describing
// format (PNG, f=100) or explicit s= and v= keys. Raw RGBA without s/v is
// undecodable by the terminal and renders nothing.
func TestKittyStore_TransmitSeqIsDecodable(t *testing.T) {
	s := media.NewKittyStore()
	img := sampleImage(16, 16)
	seq, err := media.TransmitSeq(s.IDFor(100), img, 8, 4)
	require.NoError(t, err)
	selfDescribing := strings.Contains(seq, "f=100")
	hasDims := strings.Contains(seq, "s=") && strings.Contains(seq, "v=")
	require.True(t, selfDescribing || hasDims,
		"transmit must be PNG (f=100) or carry s=/v= dimensions")
}

func TestTransmitAnimationFrameSeqEditsRootFrame(t *testing.T) {
	seq, cleanup, err := media.TransmitAnimationFrameSeq(7, sampleImage(256, 256), 256)
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	require.Contains(t, seq, "a=f")
	require.Contains(t, seq, "i=7")
	require.Contains(t, seq, "f=24")
	require.Contains(t, seq, "s=256")
	require.Contains(t, seq, "v=256")
	require.Contains(t, seq, "r=1")
	require.Contains(t, seq, "X=1")
	require.Contains(t, seq, "C=1")
	require.Contains(t, seq, "o=z")
	require.Contains(t, seq, "t=s")
	require.Contains(t, seq, "N=1")
	require.NotContains(t, seq, "a=T")
	require.NotContains(t, seq, "U=1")
	require.NotContains(t, strings.SplitN(seq, ";", 2)[0], "q=")
	require.Equal(t, 1, strings.Count(seq, "\x1b_G"), "shared-memory transmission uses one command")

	payload := strings.TrimSuffix(strings.SplitN(seq, ";", 2)[1], "\x1b\\")
	name, err := base64.StdEncoding.DecodeString(payload)
	require.NoError(t, err)
	path := filepath.Join("/dev/shm", strings.TrimPrefix(string(name), "/"))
	require.FileExists(t, path)
	cleanup()
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestKittyRenderer_NilUntilTransmitted(t *testing.T) {
	s := media.NewKittyStore()
	r := media.NewKittyRenderer(s)
	img := sampleImage(16, 16)
	require.Nil(t, r.Render(100, img, 8), "no output before transmission")
	s.MarkTransmitted(100, 8)
	require.NotNil(t, r.Render(100, img, 8), "output once transmitted")
}

func TestKittyRenderer_FootprintMatchesPhotoRows(t *testing.T) {
	s := media.NewKittyStore()
	r := media.NewKittyRenderer(s)
	img := sampleImage(20, 30)
	cols := 8
	s.MarkTransmitted(100, cols)
	lines := r.Render(100, img, cols)
	b := img.Bounds()
	require.Len(t, lines, media.PhotoRows(b.Dx(), b.Dy(), cols, media.CellAspect()))
}

func TestKittyRenderer_LineWidthEqualsCols(t *testing.T) {
	s := media.NewKittyStore()
	r := media.NewKittyRenderer(s)
	img := sampleImage(20, 20)
	cols := 10
	s.MarkTransmitted(100, cols)
	for _, l := range r.Render(100, img, cols) {
		require.Equal(t, cols, lipgloss.Width(l), "each line measures cols cells")
	}
}

func TestKittyRenderer_CellsCarryPlaceholderAndDiacritics(t *testing.T) {
	s := media.NewKittyStore()
	r := media.NewKittyRenderer(s)
	img := sampleImage(8, 8)
	cols := 4
	s.MarkTransmitted(100, cols)
	first := r.Render(100, img, cols)[0]
	require.Contains(t, first, string(kitty.Placeholder))
	require.Contains(t, first, string(kitty.Diacritic(0)))      // row 0
	require.Contains(t, first, string(kitty.Diacritic(cols-1))) // last column
	require.True(t, strings.HasSuffix(first, "\x1b[0m"), "line resets SGR")
}

func TestKittyStore_DeleteLiveSeq_PerID(t *testing.T) {
	s := media.NewKittyStore()
	id1 := s.IDFor(11)
	id2 := s.IDFor(22)

	seq := s.DeleteLiveSeq([]int64{11, 22})

	require.Equal(t, media.DeleteSeq(id1)+media.DeleteSeq(id2), seq)
	require.NotEmpty(t, seq)
}

func TestKittyStore_DeleteLiveSeq_SkipsUnassigned(t *testing.T) {
	s := media.NewKittyStore()
	// 99 was never assigned an image id, so it contributes nothing.
	require.Empty(t, s.DeleteLiveSeq([]int64{99}))
}

func TestPlaceholderWindowDimensions(t *testing.T) {
	lines := media.PlaceholderWindow(7, 3, 2, 5, 4) // hOff=3 vOff=2 winCols=5 winRows=4
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want winRows 4", len(lines))
	}
	for i, ln := range lines {
		n := 0
		for _, r := range ln {
			if r == kitty.Placeholder {
				n++
			}
		}
		if n != 5 {
			t.Fatalf("line %d has %d placeholder cells, want winCols 5", i, n)
		}
	}
}
