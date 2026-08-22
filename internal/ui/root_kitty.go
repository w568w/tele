package ui

import (
	"image"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/ui/media"
)

// transmitPhotoCmd transmits one photo to the terminal at the chat's current
// photo width and creates its virtual placement. No-op unless in Kitty mode.
func (m RootModel) transmitPhotoCmd(photoID int64, img image.Image) tea.Cmd {
	if m.imageMode != media.ModeKitty || img == nil {
		return nil
	}
	id := m.kittyStore.IDFor(photoID)
	cols, rows := m.inlineImageBox(photoID, img)
	// Encode asynchronously. On success emit kittyEncodedMsg, which the update
	// loop writes to the terminal and only then marks ready (kittyTransmittedMsg),
	// so the placeholder grid is never painted before the placement exists. On an
	// encode failure the command returns nil — nothing is written and the image is
	// never marked ready, so a later reconcile can retry instead of leaving a
	// permanently blank cell that the store falsely reports as ready (#95).
	return func() tea.Msg {
		seq, err := media.TransmitSeq(id, img, cols, rows)
		if err != nil {
			return nil
		}
		return kittyEncodedMsg{photoID: photoID, cols: cols, seq: seq}
	}
}

// inlineImageBox is the single source of truth for a placement's cell box. A
// sticker shown in the picker and the same document shown in chat deliberately
// use different widths, so a placement can only be reused when Ready matches
// the box returned here.
func (m RootModel) inlineImageBox(photoID int64, img image.Image) (int, int) {
	b := img.Bounds()
	if m.stickerPicker != nil && m.stickerPicker.selectedID() == photoID {
		return stickerPreviewBox(b.Dx(), b.Dy())
	}
	return m.chat.MediaBoxForID(photoID, b.Dx(), b.Dy())
}

// retransmitDebounce is the quiet period after the last photo-width change
// before images are re-transmitted. A resize drag fires many WindowSizeMsgs in
// quick succession; debouncing collapses them into a single retransmit at the
// final width. Without it, overlapping async transmits land out of order and
// leave the Kitty placement at a stale size (photo renders smaller than grid).
const retransmitDebounce = 90 * time.Millisecond

// retransmitOnColsChange schedules a debounced retransmit when the photo content
// width (in cells) actually changed. Photo width is photoContentCols (chat-pane,
// capped), not the window width, so this fires on any layout change that affects
// it (window resize, folder bar show/hide) and skips changes that leave the
// column count unchanged. Only the latest scheduled tick performs the work.
func (m *RootModel) retransmitOnColsChange() tea.Cmd {
	cols := m.chat.PhotoContentCols()
	// A tall photo's effective width depends on the pane height (the 2/3-viewport
	// and 480px height caps shrink cols), so a height-only resize can change a
	// photo's box without changing photoContentCols. Track both.
	paneH := m.chat.PhotoViewHeight()
	if cols == m.lastPhotoCols && paneH == m.lastPaneHeight {
		return nil
	}
	m.lastPhotoCols = cols
	m.lastPaneHeight = paneH
	m.retransmitGen++
	gen := m.retransmitGen
	return tea.Tick(retransmitDebounce, func(time.Time) tea.Msg {
		return retransmitTickMsg{gen: gen}
	})
}

// requestKittyReset asks the next reconcile to delete every placement and
// re-transmit the now-visible images. Used on chat switch and photo-width change
// (the deleted images belong to a different chat or a stale cell width).
func (m *RootModel) requestKittyReset() {
	if m.imageMode == media.ModeKitty {
		m.kittyResetPending = true
	}
}

// defaultKittyPlacementCap is the fallback cap when photos.kitty_placement_cap
// is unset (or non-positive). See PhotosConfig.KittyPlacementCap.
const defaultKittyPlacementCap = 16

// reconcileKittyCmd is the single place that issues Kitty transmits and deletes.
// It transmits visible images that are not yet live and evicts the
// least-recently-visible placements beyond the cap. No-op outside Kitty mode or
// the main screen.
func (m *RootModel) reconcileKittyCmd() tea.Cmd {
	if m.imageMode != media.ModeKitty || m.screen != ScreenMain {
		return nil
	}

	var cmds []tea.Cmd

	var pre tea.Cmd
	if m.kittyResetPending {
		m.kittyResetPending = false
		// Delete each currently-live placement by its own id (d=I) rather than a
		// blanket d=A, which is ambiguous for virtual (U=1) placements. Build the
		// sequence from the live set before clearing it. See #94.
		live := make([]int64, 0, len(m.kittyLive))
		for id := range m.kittyLive {
			live = append(live, id)
		}
		if seq := m.kittyStore.DeleteLiveSeq(live); seq != "" {
			pre = func() tea.Msg { return tea.Raw(seq)() }
		}
		m.kittyStore.Clear()
		m.kittyLive = make(map[int64]bool)
		m.kittyLRU = nil
		// Clear() drops every readiness flag, the profile overlay's included.
		// Its placement is not in the live set, so it was not deleted above and
		// is still on the terminal — but the overlay would stop drawing it and
		// fall back to a monogram mid-resize. Re-transmit instead (#223).
		if m.profile != nil && m.profile.Avatar() != nil {
			cmds = append(cmds, m.transmitAvatarCmd(m.profile.Avatar()))
		}
	}

	// The modal is drawn over the chat, so its selected sticker owns the desired
	// size when the same document is also visible behind it. De-duplicate ids to
	// avoid scheduling two incompatible placements in one reconciliation pass.
	visible := make([]int64, 0, len(m.chat.VisiblePhotoIDs())+1)
	seen := make(map[int64]bool)
	if m.stickerPicker != nil {
		if id := m.stickerPicker.selectedID(); id != 0 {
			visible = append(visible, id)
			seen[id] = true
		}
	}
	for _, id := range m.chat.VisiblePhotoIDs() {
		if !seen[id] {
			visible = append(visible, id)
			seen[id] = true
		}
	}
	visSet := make(map[int64]bool, len(visible))
	for _, id := range visible {
		visSet[id] = true
	}

	for _, id := range visible {
		img, ok := m.imageCache.Get(id)
		if !ok {
			continue
		}
		cols, _ := m.inlineImageBox(id, img)
		if m.kittyLive[id] {
			if !m.kittyStore.Placed(id) || m.kittyStore.Ready(id, cols) {
				// Keep in-flight and correctly sized placements; stale ones are
				// replaced after the current transmission completes.
				m.kittyLRU = touchID(m.kittyLRU, id)
				continue
			}
			// a=T with an existing image id replaces image data and invalidates its
			// placement. Delete the old id first, then transmit+place the new size in
			// sequence so the terminal never retains a stale virtual placement.
			m.kittyStore.Untransmit(id)
			if c := m.transmitPhotoCmd(id, img); c != nil {
				cmds = append(cmds, tea.Sequence(
					tea.Raw(media.DeleteSeq(m.kittyStore.IDFor(id))),
					c,
				))
				m.kittyLRU = touchID(m.kittyLRU, id)
			}
			continue
		}
		if c := m.transmitPhotoCmd(id, img); c != nil {
			cmds = append(cmds, c)
			m.kittyLive[id] = true
			m.kittyLRU = append(m.kittyLRU, id)
		}
	}

	// Evict the least-recently-visible placements beyond the cap; never evict a
	// currently-visible image.
	capN := m.kittyCap
	if capN <= 0 {
		capN = defaultKittyPlacementCap
	}
	for len(m.kittyLive) > capN {
		evicted := false
		for i, id := range m.kittyLRU {
			if visSet[id] {
				continue
			}
			cmds = append(cmds, tea.Raw(media.DeleteSeq(m.kittyStore.IDFor(id))))
			m.kittyStore.Untransmit(id)
			delete(m.kittyLive, id)
			m.kittyLRU = append(m.kittyLRU[:i], m.kittyLRU[i+1:]...)
			evicted = true
			break
		}
		if !evicted {
			break
		}
	}

	body := tea.Batch(cmds...)
	switch {
	case pre != nil && len(cmds) > 0:
		// The delete-all must reach the terminal before the re-transmits, so
		// sequence them; the transmits/deletes among themselves target distinct
		// ids and can run concurrently.
		return tea.Sequence(pre, body)
	case pre != nil:
		return pre
	case len(cmds) > 0:
		return body
	default:
		return nil
	}
}

// touchID moves id to the most-recently-visible end of the LRU order.
func touchID(s []int64, id int64) []int64 {
	out := make([]int64, 0, len(s))
	for _, v := range s {
		if v != id {
			out = append(out, v)
		}
	}
	return append(out, id)
}
