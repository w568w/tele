package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

// updateNetworkMsg handles messages that involve async data loading (history, media, read state).
func (m RootModel) updateNetworkMsg(msg tea.Msg) (RootModel, tea.Cmd) {
	switch msg := msg.(type) {
	case discussionOpenedMsg:
		return m.applyDiscussionOpened(msg)
	case telegramLinkResolvedMsg:
		return m.handleTelegramLinkResolved(msg)

	case screens.OpenChatMsg:
		m.searchModel = nil
		if msg.ChatID == m.currentChatID && !m.inThread() {
			m.linkOpenSerial++
			m.pageHistory = nil
			m.chatListChatID = msg.ChatID
			if m.owner != nil && m.chatSub != 0 {
				m.owner.MoveWindow(m.chatSub, m.chatWindow)
			}
			result, cmd := m.focusPane(FocusChat)
			return result.(RootModel), cmd
		}
		peer, title := msg.Peer, msg.Title
		if m.st != nil {
			if chat, ok := m.st.GetChat(msg.ChatID); ok {
				peer, title = chat.Peer, chat.Title
			}
		}
		m.pageHistory = nil
		window := project.ChatWindow{
			ChatID: msg.ChatID,
			Peer:   peer,
			Title:  title,
			Anchor: project.Anchor{Kind: project.AnchorFirstUnread},
			Before: m.historyLimit,
		}
		m, draftFlush := m.activatePage(window, msg.ChatID, 0, false)
		reactionsCmd, mentionsCmd := m.clearChatBadgesOnOpen(msg.ChatID)
		return m, tea.Batch(draftFlush, reactionsCmd, mentionsCmd)

	case screens.LoadMoreMsg:
		// Reaching the top of the window asks the owner to widen it. Whether the
		// extra messages come from the store or from Telegram is the owner's
		// business, and one fetch per subscription is in flight at a time (#120).
		if msg.ChatID != m.currentChatID {
			return m, nil
		}
		m.widenChatWindow()
		return m, nil

	case screens.LoadNewerMsg:
		if msg.ChatID != m.currentChatID || m.chatWindow.Anchor.Kind != project.AnchorMessage ||
			m.owner == nil || m.chatSub == 0 {
			return m, nil
		}
		m.chatWindow.After += m.historyLimit
		m.owner.MoveWindow(m.chatSub, m.chatWindow)
		return m, nil

	case PhotoReadyMsg:
		// SetImage owns the cache write: it must measure whether the viewport was
		// at the bottom *before* the image grows the bubble's height. Adding to the
		// shared cache here first would defeat that snapshot (the height would have
		// already grown), so the newest message could scroll out of view. SetImage
		// writes to this same shared cache, so the image still lands in m.imageCache.
		m.chat.SetImage(msg.PhotoID, msg.Image)
		// Transmit is left to reconcile (after this update): the image is only
		// placed on the terminal if it is currently visible. If this thumbnail
		// belongs to the selected GIF, start its animation now (the default
		// newest-message selection fires no key event to trigger it).
		return m.ensureGifAnimForSelection()

	case kittyEncodedMsg:
		// The encode succeeded. Write the placement to the terminal first, then
		// mark the image ready (kittyTransmittedMsg), so the placeholder grid is
		// only painted once the placement exists. A failed encode never reaches
		// here, so the image is never falsely marked ready (#95).
		seq := msg.seq
		photoID, cols := msg.photoID, msg.cols
		return m, tea.Sequence(
			func() tea.Msg { return tea.Raw(seq)() },
			func() tea.Msg { return kittyTransmittedMsg{photoID: photoID, cols: cols} },
		)

	case kittyTransmittedMsg:
		// Placement is now on the terminal; advertise it so the next render emits
		// the placeholder grid over an existing placement. The bubble already
		// reserved the image's full footprint (rendered as a placeholder box), so
		// the image swaps in at the same height — no re-anchor needed.
		m.kittyStore.MarkTransmitted(msg.photoID, msg.cols)
		return m, nil

	case FullPhotoReadyMsg:
		delete(m.fullPhotoInFlight, msg.PhotoID)
		m.fullImageCache.Add(msg.PhotoID, msg.Image)
		return m.handleFullPhotoReady(msg)

	case fullPhotoFailedMsg:
		delete(m.fullPhotoInFlight, msg.photoID)
		return m.handleFullPhotoFailed(msg)

	case modalPhotoEncodedMsg:
		return m.handleModalPhotoEncoded(msg)

	case modalPhotoTransmittedMsg:
		return m.handleModalPhotoTransmitted(msg)

	case components.OpenInViewerRequest:
		// The menu "Open" item mirrors the o key: open the sole target directly or
		// present the picker when a message has several (media plus links).
		model, cmd := m.handleOpen()
		return model.(RootModel), cmd

	case components.OpenExternalRequest:
		if photoID := m.chat.SelectedMessagePhotoID(); photoID != 0 {
			return m.openPhotoExternal(photoID)
		}
		if _, ok := m.chat.SelectedMessageVideo(); ok {
			return m.startDocumentOpen(m.chat.SelectedMessageID(), m.selectedDownloadLabel())
		}
		return m, nil

	case components.DownloadFileRequest:
		return m.handleDownloadSelected()

	case components.PlayVoiceRequest:
		return m.handlePlayVoice()

	case voicePlayReadyMsg:
		if m.voicePlayer != nil {
			if err := m.voicePlayer.Play(msg.docID, msg.data); err == nil {
				return m, voiceTickCmd()
			}
		}
		return m, nil

	case voiceTickMsg:
		if m.voicePlayer == nil {
			return m, nil
		}
		docID, progress, pos, active := m.voicePlayer.State()
		if active {
			m.chat.SetVoicePlayback(docID, progress, pos)
			return m, voiceTickCmd()
		}
		m.chat.SetVoicePlayback(0, 0, 0)
		return m, nil
	}
	return m, nil
}
