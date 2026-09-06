package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

type pageLocation struct {
	window         project.ChatWindow
	chatListChatID int64
	selectedMsgID  int
}

func (m RootModel) inThread() bool { return m.chatWindow.ThreadRootID != 0 }

func anchorSides(limit int) (int, int) {
	before := limit / 2
	return before, max(0, limit-before-1)
}

func (m RootModel) currentMessageTarget(msgID int) domain.MessageTarget {
	peer, title := m.chatWindow.Peer, m.chatWindow.Title
	if m.inThread() {
		peer, title = m.chatWindow.ThreadPeer, m.chatWindow.ThreadTitle
	} else if m.st != nil {
		if chat, ok := m.st.GetChat(m.currentChatID); ok {
			peer, title = chat.Peer, chat.Title
		}
	}
	if peer.ID == 0 {
		peer = m.chat.CurrentPeer()
	}
	if title == "" {
		title = m.chat.Title()
	}
	return domain.MessageTarget{ChatID: m.currentChatID, Peer: peer, Title: title, MsgID: msgID}
}

func (m RootModel) snapshotPage() pageLocation {
	window := m.chatWindow
	selected := m.chat.SelectedMessageID()
	if selected != 0 {
		window.Anchor = project.Anchor{Kind: project.AnchorMessage, MsgID: selected}
		window.Before, window.After = anchorSides(m.historyLimit)
	}
	return pageLocation{window: window, chatListChatID: m.chatListChatID, selectedMsgID: selected}
}

// activatePage is the single transition into a normal chat or thread. Callers
// decide whether the current page belongs on the back stack.
func (m RootModel) activatePage(window project.ChatWindow, chatListChatID int64, selectedMsgID int, push bool) (RootModel, tea.Cmd) {
	if window.ChatID == 0 {
		return m, nil
	}
	m.linkOpenSerial++
	m.importantUnread = domain.ImportantUnread{Loading: true}
	m.importantTimer, m.importantReading = false, false
	m.importantJumping = false
	m.importantRetryAt = time.Time{}
	m.importantSeen = ""
	draftFlush := m.flushCurrentDraftCmd()
	if push && m.currentChatID != 0 {
		m.pageHistory = append(m.pageHistory, m.snapshotPage())
	}

	m.currentChatID = window.ChatID
	m.chatWindow = window
	m.chatListChatID = chatListChatID
	if m.chatListChatID == 0 {
		m.chatListChatID = window.ChatID
	}
	m.stopGifAnim()
	clear(m.gifFrames)
	m.chat.ClearPendingAction()
	m.pendingJumpMsgID = selectedMsgID
	peer, title := window.Peer, window.Title
	header := screens.ChatHeader{
		ChatID:  window.ChatID,
		Title:   title,
		IsUser:  peer.IsUser(),
		IsGroup: peer.IsGroup() || peer.IsChannel(),
	}
	if window.ThreadRootID != 0 {
		peer, title = window.ThreadPeer, window.ThreadTitle
		header.DraftKey = -int64(window.ThreadRootID)
		header.Title = title
		header.IsUser = false
		header.IsGroup = true
		header.ReadOutboxMaxID = window.ThreadReadOutboxMaxID
	}
	m.chat.SetPeer(peer)
	m.chat.SetHeader(header)
	m.chat.SetLoading(true)
	m.chat.SetKnownImages(m.imageCache)
	m.focus = FocusChat
	m.chatList.SetFocused(false)
	m.chat.SetFocused(true)
	m.statusBar.SetActivePane("chat")
	m.chatList.SetActiveByID(m.chatListChatID)
	if m.owner != nil {
		m.owner.SetFocus(window.ChatID)
		if m.chatSub != 0 {
			m.owner.Unsubscribe(m.chatSub)
		}
		m.chatSub = m.owner.Subscribe(window)
	}
	m.requestKittyReset()
	return m, tea.Batch(draftFlush, m.importantPollTick())
}

func (m RootModel) navigateToMessage(target domain.MessageTarget, push bool) (RootModel, tea.Cmd) {
	if target.ChatID == m.currentChatID && !m.inThread() {
		return m.jumpWithinPage(target.MsgID)
	}
	window := project.ChatWindow{
		ChatID: target.ChatID,
		Peer:   target.Peer,
		Title:  target.Title,
		Before: m.historyLimit,
	}
	if target.MsgID != 0 {
		window.Anchor = project.Anchor{Kind: project.AnchorMessage, MsgID: target.MsgID}
		window.Before, window.After = anchorSides(m.historyLimit)
	} else {
		window.Anchor = project.Anchor{Kind: project.AnchorNewest}
	}
	m, cmd := m.activatePage(window, target.ChatID, target.MsgID, push)
	return m, cmd
}

func (m RootModel) jumpWithinPage(msgID int) (RootModel, tea.Cmd) {
	if msgID == 0 {
		return m, nil
	}
	if m.chat.ScrollToMessage(msgID) {
		return m, m.startMessageHighlight(msgID)
	}
	if m.owner == nil || m.chatSub == 0 {
		m.statusBar.SetStatus("Not in buffer")
		return m, nil
	}
	m.pendingJumpMsgID = msgID
	m.chatWindow.Anchor = project.Anchor{Kind: project.AnchorMessage, MsgID: msgID}
	m.chatWindow.Before, m.chatWindow.After = anchorSides(m.historyLimit)
	m.chat.SetLoading(true)
	m.owner.MoveWindow(m.chatSub, m.chatWindow)
	return m, nil
}

func (m RootModel) popPage() (RootModel, tea.Cmd) {
	if len(m.pageHistory) == 0 {
		return m, nil
	}
	last := len(m.pageHistory) - 1
	page := m.pageHistory[last]
	m.pageHistory = m.pageHistory[:last]
	return m.activatePage(page.window, page.chatListChatID, page.selectedMsgID, false)
}
