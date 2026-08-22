package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/sorokin-vladimir/tele/internal/store"
	"github.com/sorokin-vladimir/tele/internal/ui/media"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUseInAppVideoPlayer(t *testing.T) {
	assert.True(t, useInAppVideoPlayer(media.ModeKitty, true), "kitty + ffmpeg uses the in-app player")
	assert.False(t, useInAppVideoPlayer(media.ModeBlocks, true), "block mode falls back to external")
	assert.False(t, useInAppVideoPlayer(media.ModeKitty, false), "no ffmpeg falls back to external")
}

func TestVideoModalBox_PreservesWithinViewport(t *testing.T) {
	m := NewRootModel(store.NewMemory(), 50, false)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = m2.(RootModel)
	cols, rows := m.videoModalBox(1920, 1080)
	assert.Positive(t, cols)
	assert.Positive(t, rows)
	assert.LessOrEqual(t, cols, 100, "box must fit the terminal width")
	assert.LessOrEqual(t, rows, 40, "box must fit the terminal height")
}

func TestVideoModalBox_PortraitStaysPortrait(t *testing.T) {
	m := NewRootModel(store.NewMemory(), 50, false)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(RootModel)
	cols, rows := m.videoModalBox(1080, 1920) // portrait source
	assert.Less(t, cols, rows*2, "portrait video must not be sized as landscape")
}

func TestTogglePlay_FlipsPlaying(t *testing.T) {
	m := NewRootModel(store.NewMemory(), 50, false)
	m.videoPlayer = &videoPlayer{playing: true}
	m = m.togglePlay()
	assert.False(t, m.videoPlayer.playing, "space pauses a playing video")
	m = m.togglePlay()
	assert.True(t, m.videoPlayer.playing, "space resumes a paused video")
}

func TestHandleVideoTick_StaleGenIgnored(t *testing.T) {
	m := NewRootModel(store.NewMemory(), 50, false)
	m.videoPlayer = &videoPlayer{playing: true, gen: 3}
	_, cmd := m.handleVideoTick(videoTickMsg{gen: 2})
	assert.Nil(t, cmd, "a tick from a previous generation must not re-arm")
}

func TestVideoFrame_WaitsForKittyAckBeforeAdvancing(t *testing.T) {
	m := NewRootModel(store.NewMemory(), 50, false)
	oldFrame := solidImage(8, 8)
	newFrame := solidImage(16, 9)
	m.videoPlayer = &videoPlayer{
		playing:      true,
		gen:          3,
		frame:        oldFrame,
		kittyID:      9,
		transmitting: true,
		nextFrameAt:  time.Now(),
	}

	m, cmd := m.handleVideoFrameEncoded(videoFrameEncodedMsg{
		gen: 3, id: 9, frame: newFrame, seq: "frame", cleanup: func() {},
	})
	require.NotNil(t, cmd)
	assert.Same(t, oldFrame, m.videoPlayer.frame, "keep the last frame visible while Kitty is processing the next one")
	assert.Same(t, newFrame, m.videoPlayer.pendingFrame)
	assert.Zero(t, m.videoPlayer.posFrames)

	m, next := m.handleVideoGraphicsResponse(uv.KittyGraphicsEvent{
		Options: kitty.Options{ID: 9}, Payload: []byte("OK"),
	})
	require.NotNil(t, next, "a Kitty acknowledgement schedules the next frame")
	assert.Same(t, newFrame, m.videoPlayer.frame)
	assert.Nil(t, m.videoPlayer.pendingFrame)
	assert.Equal(t, 1, m.videoPlayer.posFrames)
	assert.False(t, m.videoPlayer.transmitting)
}

func TestVideoFrame_KittyErrorStopsPlayback(t *testing.T) {
	m := NewRootModel(store.NewMemory(), 50, false)
	m.videoPlayer = &videoPlayer{playing: true, transmitting: true, kittyID: 9}

	m, cmd := m.handleVideoGraphicsResponse(uv.KittyGraphicsEvent{
		Options: kitty.Options{ID: 9}, Payload: []byte("EINVAL:bad frame"),
	})
	require.NotNil(t, cmd)
	assert.False(t, m.videoPlayer.playing)
	assert.False(t, m.videoPlayer.transmitting)
	status, ok := cmd().(StatusErrMsg)
	require.True(t, ok)
	assert.Contains(t, status.Text, "EINVAL:bad frame")
}

func TestModalBorder_WidthAndLabels(t *testing.T) {
	line := modalBorder("┌", "─", "┐", "─ Alice ", " 0:00 ", 40)
	assert.Equal(t, 42, lipgloss.Width(line), "border width must be innerW + 2 corners")
	assert.Contains(t, line, "Alice")
	assert.Contains(t, line, "0:00")
}

func TestModalBorder_TruncatesWhenLabelsExceedWidth(t *testing.T) {
	line := modalBorder("┌", "─", "┐", "─ a very long sender name ", " 1:23 / 4:56 ", 10)
	assert.Equal(t, 12, lipgloss.Width(line), "must still fit innerW + 2 even when labels overflow")
}

func TestVideoFooterHints_ReflectPlayState(t *testing.T) {
	assert.Contains(t, videoFooterHints(true, false), "pause", "playing shows the pause action")
	assert.Contains(t, videoFooterHints(false, false), "play", "paused shows the play action")
	assert.NotContains(t, videoFooterHints(false, false), "browse", "no browse hint for a lone video")
	assert.Contains(t, videoFooterHints(false, true), "browse", "album video shows the left/right browse hint")
}

func TestVideoLoadingSpinnerGlyph(t *testing.T) {
	m := NewRootModel(store.NewMemory(), 50, false)
	m.videoPlayer = &videoPlayer{spinnerIdx: 0}
	g0 := videoSpinnerGlyph(m.videoPlayer.spinnerIdx)
	m.updateVideoSpinner()
	g1 := videoSpinnerGlyph(m.videoPlayer.spinnerIdx)
	assert.NotEqual(t, g0, g1, "spinner glyph advances while loading")
}

func TestCloseVideoPlayer_Clears(t *testing.T) {
	m := NewRootModel(store.NewMemory(), 50, false)
	m.videoPlayer = &videoPlayer{docID: 5}
	m, _ = m.closeVideoPlayer()
	assert.Nil(t, m.videoPlayer, "closing clears the overlay")
}
