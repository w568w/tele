package ui

import (
	"image"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sorokin-vladimir/tele/internal/domain"
	vmedia "github.com/sorokin-vladimir/tele/internal/media"
	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/sorokin-vladimir/tele/internal/ui/keys"
	"github.com/sorokin-vladimir/tele/internal/ui/media"
	"github.com/sorokin-vladimir/tele/internal/ui/theme"
)

// photoViewer is the in-app photo modal overlay state. Unlike videoPlayer it holds
// a single still image; it swaps from the inline preview to full quality when the
// full download completes.
type photoViewer struct {
	photoID       int64
	chatID        int64
	msgID         int
	ref           domain.PhotoRef
	title         string // sender name, shown on the top border
	timeLabel     string // date + send time, shown on the bottom-right border
	img           image.Image
	full          bool // whether img is the full-quality image
	failed        bool
	retryDownload bool
	cols          int
	rows          int
	spinnerIdx    int // loading-spinner index while downloading or transmitting
	renderGen     int
	kittyID       uint32
	kittyReady    bool
	transmitting  bool
	pendingFull   image.Image
	// album is the full set of media parts when this photo belongs to an album,
	// empty for a lone photo; albumIdx is the index of the shown part. They drive
	// left/right paging across the album.
	album    []components.GroupMediaRef
	albumIdx int
}

// selectedPhotoInfo returns the selected message's sender display name and send
// time, or zero values if unknown.
func (m RootModel) selectedPhotoInfo() (string, time.Time) {
	if m.st == nil || m.chat == nil {
		return "", time.Time{}
	}
	id := m.chat.SelectedMessageID()
	for _, msg := range m.st.Messages(m.currentChatID) {
		if msg.ID == id {
			return msg.SenderName, msg.Date
		}
	}
	return "", time.Time{}
}

// openPhotoModal opens the photo modal showing the best cached image immediately
// (full quality if present, else the inline preview, else a loading spinner) and,
// when full quality is not cached and the photo has one, dispatches the
// full-quality download so it can be swapped in on arrival. ref/msgID/sender/date
// are supplied by the caller (mirrors openVideoModal).
func (m RootModel) openPhotoModal(ref domain.PhotoRef, msgID int, sender string, date time.Time) (RootModel, tea.Cmd) {
	photoID := ref.ID
	timeLabel := ""
	if !date.IsZero() {
		timeLabel = components.FormatDateLabel(date) + " " + date.Format("15:04")
	}

	pv := &photoViewer{
		photoID:   photoID,
		chatID:    m.currentChatID,
		msgID:     msgID,
		ref:       ref,
		title:     sender,
		timeLabel: timeLabel,
		kittyID:   m.kittyStore.NewID(),
	}

	// Best cached image: full first, then the inline preview.
	if img, ok := m.fullImageCache.Get(photoID); ok {
		pv.img, pv.full = img, true
	} else if img, ok := m.imageCache.Get(photoID); ok {
		pv.img = img
	}

	if pv.img != nil {
		b := pv.img.Bounds()
		pv.cols, pv.rows = m.modalImageBox(b.Dx(), b.Dy(), 0)
	} else {
		pv.cols, pv.rows = m.modalImageBox(4, 3, 0) // provisional; resized on arrival
	}
	m.photoViewer = pv

	var cmds []tea.Cmd
	// In Kitty mode, encode the current image for this viewer generation. The
	// placeholder grid stays hidden until the matching placement is queued.
	if m.imageMode == media.ModeKitty && pv.img != nil {
		cmds = append(cmds, m.startModalPhotoTransmit())
	}
	// Fetch full quality if we don't already have it and the photo has one.
	if !pv.full && ref.FullThumbSize != "" {
		cmds = append(cmds, m.startFullPhotoDownload(m.currentChatID, msgID, ref.ID, false))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

// startModalPhotoTransmit begins one replace operation. renderGen makes an
// encoded preview harmless if the full image supersedes it before encoding
// finishes.
func (m *RootModel) startModalPhotoTransmit() tea.Cmd {
	pv := m.photoViewer
	if m.imageMode != media.ModeKitty || pv == nil || pv.img == nil {
		return nil
	}
	pv.renderGen++
	pv.kittyReady = false
	id := pv.kittyID
	renderGen, img := pv.renderGen, pv.img
	cols, rows := pv.cols, pv.rows
	return func() tea.Msg {
		seq, err := media.TransmitSeq(id, img, cols, rows)
		return modalPhotoEncodedMsg{
			renderGen: renderGen,
			id:        id,
			seq:       seq,
			err:       err,
		}
	}
}

// openPhotoModalAlbum opens a photo that is part of an album, recording the full
// album and current index so left/right can page across parts.
func (m RootModel) openPhotoModalAlbum(ref domain.PhotoRef, msgID int, sender string, date time.Time, album []components.GroupMediaRef, idx int) (RootModel, tea.Cmd) {
	m, cmd := m.openPhotoModal(ref, msgID, sender, date)
	if m.photoViewer != nil {
		m.photoViewer.album = album
		m.photoViewer.albumIdx = idx
	}
	return m, cmd
}

// pageModal steps the open album modal by delta (+1 next, -1 previous), clamped
// to the album bounds. The neighbor part opens in the modal that matches its kind,
// so paging can cross photo<->video within one album. A no-op when no album modal
// is open or the album has a single part.
func (m RootModel) pageModal(delta int) (RootModel, tea.Cmd) {
	var album []components.GroupMediaRef
	var cur int
	switch {
	case m.photoViewer != nil && len(m.photoViewer.album) > 1:
		album, cur = m.photoViewer.album, m.photoViewer.albumIdx
	case m.videoPlayer != nil && len(m.videoPlayer.album) > 1:
		album, cur = m.videoPlayer.album, m.videoPlayer.albumIdx
	default:
		return m, nil
	}
	next := cur + delta
	if next < 0 || next >= len(album) {
		return m, nil // clamp at the ends
	}
	p := album[next]

	// Tear down whichever modal is open before opening the neighbor.
	var cmds []tea.Cmd
	if m.photoViewer != nil {
		var c tea.Cmd
		m, c = m.closePhotoModal()
		cmds = append(cmds, c)
	}
	if m.videoPlayer != nil {
		var c tea.Cmd
		m, c = m.closeVideoPlayer()
		cmds = append(cmds, c)
	}

	switch {
	case p.Photo != nil:
		var c tea.Cmd
		m, c = m.openPhotoModalAlbum(*p.Photo, p.MsgID, p.Sender, p.Date, album, next)
		cmds = append(cmds, c)
	case p.Doc != nil && p.Kind.IsVideo():
		var c tea.Cmd
		if useInAppVideoPlayer(m.imageMode, vmedia.HasFFmpeg()) {
			m, c = m.openVideoModalAlbum(*p.Doc, p.MsgID, p.DurSecs, p.Sender, album, next)
		} else {
			m, c = m.startDocumentOpen(p.MsgID, p.Sender)
		}
		cmds = append(cmds, c)
	}
	return m, tea.Batch(cmds...)
}

// closePhotoModal tears down the overlay and drops the transmitted image. In
// Kitty mode it also deletes this viewer generation's private image id. A late
// transmission completion repeats that cleanup, so closing during an encode
// cannot leave an orphaned terminal resource.
func (m RootModel) closePhotoModal() (RootModel, tea.Cmd) {
	if m.photoViewer == nil {
		return m, nil
	}
	var cmd tea.Cmd
	if m.imageMode == media.ModeKitty && m.photoViewer.kittyID != 0 {
		id := m.photoViewer.kittyID
		cmd = func() tea.Msg { return tea.Raw(media.DeleteSeq(id))() }
	}
	m.photoViewer = nil
	return m, cmd
}

// handleFullPhotoReady swaps the modal to full quality when the download lands for
// the open photo, resizing the box and (in Kitty) re-transmitting. No-op for any
// other photo id.
func (m RootModel) handleFullPhotoReady(msg FullPhotoReadyMsg) (RootModel, tea.Cmd) {
	pv := m.photoViewer
	if pv == nil || pv.photoID != msg.PhotoID || pv.full {
		return m, nil
	}
	if m.imageMode == media.ModeKitty && pv.transmitting {
		pv.pendingFull = msg.Image
		return m, nil
	}
	return m, m.applyFullPhoto(msg.Image)
}

func (m *RootModel) applyFullPhoto(img image.Image) tea.Cmd {
	pv := m.photoViewer
	if pv == nil {
		return nil
	}
	pv.img = img
	pv.full = true
	pv.failed = false
	pv.retryDownload = false
	b := img.Bounds()
	pv.cols, pv.rows = m.modalImageBox(b.Dx(), b.Dy(), 0)
	return m.startModalPhotoTransmit()
}

func (m RootModel) handleFullPhotoFailed(msg fullPhotoFailedMsg) (RootModel, tea.Cmd) {
	if pv := m.photoViewer; pv != nil && pv.photoID == msg.photoID && !pv.full {
		pv.failed = true
		pv.retryDownload = true
	}
	var status tea.Msg
	if msg.quiet {
		status = errStatusBackground("full photo download", msg.err)
	} else {
		status = errStatus("full photo download", msg.err)
	}
	if status == nil {
		return m, nil
	}
	return m, func() tea.Msg { return status }
}

func (m RootModel) handleModalPhotoEncoded(msg modalPhotoEncodedMsg) (RootModel, tea.Cmd) {
	pv := m.photoViewer
	if pv == nil || pv.kittyID != msg.id || pv.renderGen != msg.renderGen {
		return m, nil
	}
	if msg.err != nil {
		pv.failed = true
		pv.retryDownload = false
		status := errStatus("render photo", msg.err)
		if status == nil {
			return m, nil
		}
		return m, func() tea.Msg { return status }
	}
	pv.transmitting = true
	seq := media.DeleteSeq(msg.id) + msg.seq
	renderGen := msg.renderGen
	return m, tea.Sequence(
		func() tea.Msg { return tea.Raw(seq)() },
		func() tea.Msg {
			return modalPhotoTransmittedMsg{renderGen: renderGen, id: msg.id}
		},
	)
}

func (m RootModel) handleModalPhotoTransmitted(msg modalPhotoTransmittedMsg) (RootModel, tea.Cmd) {
	pv := m.photoViewer
	if pv == nil || pv.kittyID != msg.id {
		return m, func() tea.Msg { return tea.Raw(media.DeleteSeq(msg.id))() }
	}
	if pv.renderGen != msg.renderGen {
		return m, nil
	}
	pv.transmitting = false
	if pv.pendingFull != nil {
		img := pv.pendingFull
		pv.pendingFull = nil
		return m, m.applyFullPhoto(img)
	}
	pv.kittyReady = true
	if !pv.retryDownload {
		pv.failed = false
	}
	return m, nil
}

// updatePhotoSpinner advances the modal's loading spinner while no image has been
// shown yet. Driven off the existing SpinnerTickMsg cadence — no extra ticker.
func (m *RootModel) updatePhotoSpinner() {
	if pv := m.photoViewer; pv != nil && !pv.failed &&
		(pv.img == nil || (m.imageMode == media.ModeKitty && !pv.kittyReady)) {
		pv.spinnerIdx++
	}
}

// handlePhotoModalKey handles keys while the photo modal is open: esc/q close, O
// opens the photo in the external viewer (modal stays open).
func (m RootModel) handlePhotoModalKey(keyStr string) (RootModel, tea.Cmd) {
	// Normalize so keys work regardless of keyboard layout (e.g. Russian).
	switch keys.NormalizeKey(keyStr) {
	case "esc", "q":
		return m.closePhotoModal()
	case "right", "l":
		return m.pageModal(1)
	case "left", "h":
		return m.pageModal(-1)
	case "O":
		if m.photoViewer != nil {
			return m.openPhotoExternal(m.photoViewer.photoID)
		}
	case "r":
		pv := m.photoViewer
		if pv == nil || !pv.failed {
			return m, nil
		}
		pv.failed = false
		if pv.retryDownload && !pv.full && pv.ref.FullThumbSize != "" {
			pv.retryDownload = false
			return m, m.startFullPhotoDownload(pv.chatID, pv.msgID, pv.photoID, false)
		}
		if m.imageMode == media.ModeKitty && pv.img != nil {
			return m, m.startModalPhotoTransmit()
		}
	}
	return m, nil
}

// photoFooterHints renders the modal hint bar (bottom-border left label) in the
// app's overlay-hint style: O opens externally, esc closes.
func photoFooterHints(hasAlbum, canRetry bool) string {
	hints := [][2]string{}
	if hasAlbum {
		hints = append(hints, [2]string{"←/→", "browse"})
	}
	if canRetry {
		hints = append(hints, [2]string{"r", "retry"})
	}
	hints = append(hints, [2]string{"O", "external"}, [2]string{"esc", "close"})
	return components.OverlayHint(hints, nil)
}

// photoViewerView composites the bordered photo modal over base (the chat),
// centered, using integer stamping so Kitty placeholders are never measured with
// lipgloss.
func (m RootModel) photoViewerView(base string) string {
	pv := m.photoViewer
	if pv == nil {
		return base
	}

	var content []string
	cols, rows := pv.cols, pv.rows
	waitingForKitty := m.imageMode == media.ModeKitty && !pv.kittyReady
	switch {
	case pv.failed && (pv.img == nil || waitingForKitty):
		blank := theme.Pad(cols)
		content = make([]string, rows)
		for i := range content {
			content[i] = blank
		}
		if rows > 0 {
			line := theme.S().Body.Render("couldn't load photo")
			if lp := (cols - lipgloss.Width(line)) / 2; lp > 0 {
				line = theme.Pad(lp) + line
			}
			line += theme.PadTo(lipgloss.Width(line), cols)
			content[rows/2] = line
		}
	case pv.img == nil || waitingForKitty:
		// Loading or encoding: reserve the final cols×rows box, but do not emit
		// placeholders until their matching Kitty placement has been queued.
		blank := theme.Pad(cols)
		content = make([]string, rows)
		for i := range content {
			content[i] = blank
		}
		if rows > 0 {
			line := theme.S().Body.Render(videoSpinnerGlyph(pv.spinnerIdx) + " loading…")
			if lp := (cols - lipgloss.Width(line)) / 2; lp > 0 {
				line = theme.Pad(lp) + line
			}
			line += theme.PadTo(lipgloss.Width(line), cols)
			content[rows/2] = line
		}
	case m.imageMode == media.ModeKitty:
		content = media.PlaceholderLines(pv.kittyID, cols, rows)
	default:
		// Block-art: render at the box width; its line count sets the box height.
		content = media.RenderBlockArt(pv.img, cols)
	}

	box := modalBoxLines(content, cols, pv.title, photoFooterHints(len(pv.album) > 1, pv.failed), pv.timeLabel)

	boxW := cols + 2
	left := (m.width - boxW) / 2
	if left < 0 {
		left = 0
	}
	top := (m.height - len(box)) / 2
	if top < 0 {
		top = 0
	}
	return stampBoxOverlay(base, box, top, left, boxW, m.height)
}
