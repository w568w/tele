package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/sorokin-vladimir/tele/internal/ui/keys"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

type discussionNav struct {
	sourceChat  domain.Chat
	sourceMsgID int
	rootMsgID   int
	peer        domain.Peer
}

type joinDiscussionPrompt struct {
	chatID int64
	ref    string
}

type joinDiscussionRetryMsg struct {
	err error
}

type discussionOpenedMsg struct {
	sourceChatID int64
	sourceMsgID  int
	discussion   domain.Discussion
	err          error
}

func openDiscussionCmd(ctx context.Context, owner Owner, sourceChatID int64, sourceMsgID int, discussionChatID int64) tea.Cmd {
	return func() tea.Msg {
		discussion, err := owner.OpenDiscussion(ctx, sourceChatID, sourceMsgID, discussionChatID)
		return discussionOpenedMsg{
			sourceChatID: sourceChatID,
			sourceMsgID:  sourceMsgID,
			discussion:   discussion,
			err:          err,
		}
	}
}

func (m RootModel) openDiscussion(sourceMsgID int, discussionChatID int64) (RootModel, tea.Cmd) {
	m.contextMenu = nil
	if m.owner == nil || m.st == nil || m.currentChatID == 0 {
		return m, nil
	}
	m.chat.SetLoading(true)
	return m, openDiscussionCmd(m.ctx, m.owner, m.currentChatID, sourceMsgID, discussionChatID)
}

func (m RootModel) applyDiscussionOpened(msg discussionOpenedMsg) (RootModel, tea.Cmd) {
	if msg.sourceChatID != m.currentChatID || m.discussion != nil {
		return m, nil
	}
	if msg.err != nil {
		m.chat.SetLoading(false)
		return m, func() tea.Msg { return errStatus("open comments", msg.err) }
	}
	source, ok := m.st.GetChat(msg.sourceChatID)
	if !ok {
		m.chat.SetLoading(false)
		return m, nil
	}

	draftFlush := m.flushCurrentDraftCmd()
	m.discussion = &discussionNav{
		sourceChat:  source,
		sourceMsgID: msg.sourceMsgID,
		rootMsgID:   msg.discussion.RootMsgID,
		peer:        msg.discussion.Chat.Peer,
	}
	m.currentChatID = msg.discussion.Chat.ID
	m.stopGifAnim()
	clear(m.gifFrames)
	m.chat.ClearPendingAction()
	m.pendingJumpMsgID = 0
	m.chat.SetPeer(msg.discussion.Chat.Peer)
	m.chat.SetHeader(screens.ChatHeader{
		ChatID:          msg.discussion.Chat.ID,
		DraftKey:        -int64(msg.discussion.RootMsgID),
		Title:           "Comments · " + source.Title,
		IsGroup:         true,
		ReadOutboxMaxID: msg.discussion.ReadOutboxMaxID,
	})
	m.chat.SetLoading(true)
	m.chat.SetKnownImages(m.imageCache)
	m.focus = FocusChat
	m.chatList.SetFocused(false)
	m.chat.SetFocused(true)
	m.statusBar.SetActivePane("chat")
	m.chatList.SetActiveByID(source.ID)
	if m.owner != nil {
		m.owner.SetFocus(msg.discussion.Chat.ID)
		if m.chatSub != 0 {
			m.owner.Unsubscribe(m.chatSub)
		}
		m.chatWindow = project.ChatWindow{
			ChatID:                msg.discussion.Chat.ID,
			Anchor:                project.Anchor{Kind: project.AnchorNewest},
			Before:                m.historyLimit,
			ThreadRootID:          msg.discussion.RootMsgID,
			ThreadPeer:            msg.discussion.Chat.Peer,
			ThreadTitle:           "Comments · " + source.Title,
			ThreadReadInboxMaxID:  msg.discussion.ReadInboxMaxID,
			ThreadReadOutboxMaxID: msg.discussion.ReadOutboxMaxID,
		}
		m.chatSub = m.owner.Subscribe(m.chatWindow)
	}
	m.requestKittyReset()
	return m, draftFlush
}

func (m RootModel) closeDiscussion() (tea.Model, tea.Cmd) {
	if m.discussion == nil {
		return m, nil
	}
	nav := *m.discussion
	m.discussion = nil
	m.currentChatID = nav.sourceChat.ID
	m.stopGifAnim()
	clear(m.gifFrames)
	m.chat.ClearPendingAction()
	m.pendingJumpMsgID = nav.sourceMsgID
	m.chat.SetPeer(nav.sourceChat.Peer)
	m.chat.SetHeader(screens.ChatHeader{ChatID: nav.sourceChat.ID, Title: nav.sourceChat.Title})
	m.chat.SetLoading(true)
	m.chat.SetKnownImages(m.imageCache)
	m.chatList.SetActiveByID(nav.sourceChat.ID)
	if m.owner != nil {
		m.owner.SetFocus(nav.sourceChat.ID)
		if m.chatSub != 0 {
			m.owner.Unsubscribe(m.chatSub)
		}
		before := m.historyLimit / 2
		after := m.historyLimit - before - 1
		if after < 0 {
			after = 0
		}
		m.chatWindow = project.ChatWindow{
			ChatID: nav.sourceChat.ID,
			Anchor: project.Anchor{Kind: project.AnchorMessage, MsgID: nav.sourceMsgID},
			Before: before,
			After:  after,
		}
		m.chatSub = m.owner.Subscribe(m.chatWindow)
	}
	m.requestKittyReset()
	return m, nil
}

func (m RootModel) handleJoinDiscussionPromptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := keys.NormalizeKey(msg.String())
	switch key {
	case "n", "esc":
		m.joinPrompt = nil
		return m, nil
	case "y", "enter":
		prompt := *m.joinPrompt
		m.joinPrompt = nil
		if m.owner == nil {
			return m, nil
		}
		ctx, owner := m.ctx, m.owner
		return m, func() tea.Msg {
			return joinDiscussionRetryMsg{err: owner.JoinDiscussionAndRetry(ctx, prompt.chatID, prompt.ref)}
		}
	default:
		return m, nil
	}
}

func (m RootModel) handleJoinDiscussionRetry(msg joinDiscussionRetryMsg) (RootModel, tea.Cmd) {
	if msg.err != nil {
		return m, func() tea.Msg { return errStatus("join discussion", msg.err) }
	}
	m.statusBar.SetStatus("joined discussion group; retrying send")
	return m, nil
}

func (m RootModel) joinDiscussionPromptView(content string) string {
	maxW := m.width - 10
	if maxW > 68 {
		maxW = 68
	}
	if maxW < 26 {
		maxW = 26
	}
	box := components.RenderConfirmBox(
		"Join discussion group?",
		"Telegram refused guest sending. Join this group and retry the comment?",
		"join and retry",
		maxW,
	)
	return overlayCenter(dimBackground(content), box, m.width, m.height)
}
