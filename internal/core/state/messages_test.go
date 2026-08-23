package state_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core/state"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
)

func newState(t *testing.T) (*state.State, store.Store) {
	t.Helper()
	st := store.NewMemory()
	return state.New(st), st
}

func TestApplyIncoming_AppendsCountsAndReportsChange(t *testing.T) {
	s, st := newState(t)
	st.SetChat(domain.Chat{ID: 1, Title: "A"})

	chg, ok := s.ApplyIncoming(domain.Message{ID: 5, ChatID: 1, Text: "hi"})

	require.True(t, ok)
	assert.Equal(t, state.ChangeNewMessage, chg.Kind)
	assert.Equal(t, int64(1), chg.ChatID)
	assert.Equal(t, 5, chg.Message.ID)
	assert.True(t, chg.UnreadChanged)
	require.Len(t, st.Messages(1), 1)
	c, _ := st.GetChat(1)
	assert.Equal(t, 1, c.UnreadCount)
}

// A message already covered by the read pointer is still appended to history,
// but no counter moved, so the folder bar must not be recomputed.
func TestApplyIncoming_ReadElsewhereReportsNoUnreadChange(t *testing.T) {
	s, st := newState(t)
	st.SetChat(domain.Chat{ID: 1, ReadInboxMaxID: 10})

	chg, ok := s.ApplyIncoming(domain.Message{ID: 5, ChatID: 1})

	require.True(t, ok, "the message is still news to the client")
	assert.False(t, chg.UnreadChanged)
	require.Len(t, st.Messages(1), 1)
}

func TestApplyIncoming_OutgoingDoesNotCount(t *testing.T) {
	s, st := newState(t)
	st.SetChat(domain.Chat{ID: 1})

	chg, ok := s.ApplyIncoming(domain.Message{ID: 5, ChatID: 1, IsOut: true})

	require.True(t, ok)
	assert.False(t, chg.UnreadChanged)
	c, _ := st.GetChat(1)
	assert.Equal(t, 0, c.UnreadCount)
}

// A real content edit carries an EditDate and rewrites the text.
func TestApplyEdit_RealEditUpdatesText(t *testing.T) {
	s, st := newState(t)
	st.SetChat(domain.Chat{ID: 1})
	st.AppendMessage(domain.Message{ID: 5, ChatID: 1, Text: "before", HasWebPreview: true})
	when := time.Now()

	chg, ok := s.ApplyEdit(domain.Message{ID: 5, ChatID: 1, Text: "after", EditDate: &when})

	require.True(t, ok)
	assert.Equal(t, state.ChangeMessageEdited, chg.Kind)
	assert.Equal(t, "after", st.Messages(1)[0].Text)
	assert.False(t, st.Messages(1)[0].HasWebPreview)
}

func TestApplyEdit_ServiceMessageReplacesWithoutDoubleCountingUnread(t *testing.T) {
	s, st := newState(t)
	st.SetChat(domain.Chat{ID: 1, UnreadCount: 1, ReadInboxMaxID: 4})
	st.AppendMessage(domain.Message{
		ID: 5, ChatID: 1, Text: "before", SenderName: "Ada", IsService: true,
	})

	chg, ok := s.ApplyEdit(domain.Message{
		ID: 5, ChatID: 1, Text: "after", IsService: true,
	})

	require.True(t, ok)
	assert.Equal(t, state.ChangeMessageEdited, chg.Kind)
	assert.Equal(t, "after", st.Messages(1)[0].Text)
	assert.Equal(t, "Ada", st.Messages(1)[0].SenderName)
	chat, _ := st.GetChat(1)
	assert.Equal(t, 1, chat.UnreadCount)
}

// A reaction on a message that was genuinely edited earlier arrives with a
// non-nil EditDate: edit_date still carries the original edit time and
// edit_hide is false, because the "edited" label genuinely should show. The
// reactions ride along in the same payload and must not be dropped (#199).
func TestApplyEdit_AlreadyEditedMessageStillAppliesReactions(t *testing.T) {
	s, st := newState(t)
	st.SetChat(domain.Chat{ID: 1})
	edited := time.Now().Add(-time.Hour)
	st.AppendMessage(domain.Message{ID: 5, ChatID: 1, Text: "fixed typo", EditDate: &edited})

	chg, ok := s.ApplyEdit(domain.Message{
		ID: 5, ChatID: 1, Text: "fixed typo", EditDate: &edited,
		Reactions:          []domain.Reaction{{Emoji: "👍", Count: 1}},
		HasUnreadReactions: true,
	})

	require.True(t, ok)
	got := st.Messages(1)[0]
	require.Len(t, got.Reactions, 1, "the reaction carried by the edit must be applied")
	assert.Equal(t, "👍", got.Reactions[0].Emoji)
	assert.Equal(t, "fixed typo", got.Text, "the text edit still applies")
	assert.NotNil(t, got.EditDate, "the message stays marked as edited")
	assert.True(t, chg.UnreadReactionChanged, "the chat's unread-reaction count moved")
}

// Telegram delivers a 1:1 peer reaction as a hidden edit with no EditDate. It
// must apply the reactions and must NOT flip the message to "edited" (#118,
// #160), so it surfaces as a reactions change, not an edit.
func TestApplyEdit_HiddenEditSurfacesAsReactions(t *testing.T) {
	s, st := newState(t)
	st.SetChat(domain.Chat{ID: 1})
	st.AppendMessage(domain.Message{ID: 5, ChatID: 1, Text: "hi"})

	chg, ok := s.ApplyEdit(domain.Message{
		ID: 5, ChatID: 1, Text: "hi",
		Reactions:          []domain.Reaction{{Emoji: "👍", Count: 1}},
		HasUnreadReactions: true,
	})

	require.True(t, ok)
	assert.Equal(t, state.ChangeMessageReactions, chg.Kind)
	assert.Equal(t, 5, chg.MsgID)
	assert.True(t, chg.ReactionsUnread)
	assert.True(t, chg.UnreadReactionChanged)
	assert.Nil(t, st.Messages(1)[0].EditDate, "a hidden edit must not mark the message edited")
}

func TestApplyReactions_TracksUnreadOnce(t *testing.T) {
	s, st := newState(t)
	st.SetChat(domain.Chat{ID: 1})
	st.AppendMessage(domain.Message{ID: 5, ChatID: 1})

	chg, ok := s.ApplyReactions(1, 5, []domain.Reaction{{Emoji: "👍", Count: 1}}, true)
	require.True(t, ok)
	assert.True(t, chg.UnreadReactionChanged)

	// Same message again: the count is already tracked.
	chg, ok = s.ApplyReactions(1, 5, []domain.Reaction{{Emoji: "👍", Count: 2}}, true)
	require.True(t, ok)
	assert.False(t, chg.UnreadReactionChanged)
	c, _ := st.GetChat(1)
	assert.Equal(t, 1, c.UnreadReactionsCount)
}

func TestApplyDelete_WithChatID(t *testing.T) {
	s, st := newState(t)
	st.SetChat(domain.Chat{ID: 1})
	st.AppendMessage(domain.Message{ID: 5, ChatID: 1})
	st.AppendMessage(domain.Message{ID: 6, ChatID: 1})

	chg, ok := s.ApplyDelete(1, []int{5})

	require.True(t, ok)
	assert.Equal(t, state.ChangeMessagesDeleted, chg.Kind)
	assert.Equal(t, int64(1), chg.ChatID)
	assert.Equal(t, []int{5}, chg.MsgIDs)
	require.Len(t, st.Messages(1), 1)
	assert.Equal(t, 6, st.Messages(1)[0].ID)
}

// A non-channel delete arrives with no peer context; the store resolves each ID
// to its owning chat through its index (#72).
func TestApplyDelete_WithoutChatIDResolvesThroughStore(t *testing.T) {
	s, st := newState(t)
	st.SetChat(domain.Chat{ID: 1, Peer: domain.Peer{ID: 1, Type: domain.PeerUser}})
	st.AppendMessage(domain.Message{ID: 5, ChatID: 1})

	chg, ok := s.ApplyDelete(0, []int{5})

	require.True(t, ok)
	assert.Equal(t, int64(0), chg.ChatID)
	assert.Empty(t, st.Messages(1))
}

// ApplyHistory is how a fetched page of history enters state: the owner merges
// it with what is already stored and commits the result, so projections rebuild
// from one place rather than from a client's reply handler.
func TestApplyHistory_StoresTheWindowAndPublishesOneChange(t *testing.T) {
	s, st := newState(t)
	var seen []state.Change
	s.OnChange(func(c state.Change) { seen = append(seen, c) })
	st.SetChat(domain.Chat{ID: 1})

	chg, ok := s.ApplyHistory(1, []domain.Message{
		{ID: 1, ChatID: 1, Date: time.Unix(1, 0)},
		{ID: 2, ChatID: 1, Date: time.Unix(2, 0)},
	})

	require.True(t, ok)
	assert.Equal(t, state.ChangeHistory, chg.Kind)
	assert.Equal(t, int64(1), chg.ChatID)
	assert.Len(t, st.Messages(1), 2)
	assert.Len(t, seen, 1)
}

func TestApplyHistory_ReplacesTheStoredMessages(t *testing.T) {
	s, st := newState(t)
	st.SetChat(domain.Chat{ID: 1})
	s.ApplyHistory(1, []domain.Message{{ID: 5, ChatID: 1, Date: time.Unix(5, 0)}})

	s.ApplyHistory(1, []domain.Message{
		{ID: 4, ChatID: 1, Date: time.Unix(4, 0)},
		{ID: 5, ChatID: 1, Date: time.Unix(5, 0)},
	})

	assert.Len(t, st.Messages(1), 2, "the caller merges; state stores what it is given")
}

// A refreshed file reference is not an edit: it changes how the media is
// addressed, not what the message says, so it must not set an edited marker.
func TestApplyMediaRef_ReplacesTheReferenceAndPublishes(t *testing.T) {
	s, st := newState(t)
	st.SetMessages(1, []domain.Message{{
		ID: 5, ChatID: 1, Date: time.Unix(1, 0),
		Photo: &domain.PhotoRef{ID: 9, FileReference: []byte("stale")},
	}})
	var seen []state.Change
	s.OnChange(func(c state.Change) { seen = append(seen, c) })

	chg, ok := s.ApplyMediaRef(1, 5, &domain.PhotoRef{ID: 9, FileReference: []byte("fresh")}, nil)

	require.True(t, ok)
	assert.Equal(t, state.ChangeMediaRef, chg.Kind)
	assert.Equal(t, int64(1), chg.ChatID)
	assert.Equal(t, 5, chg.MsgID)
	require.Len(t, seen, 1)
	got := st.Messages(1)
	require.Len(t, got, 1)
	assert.Equal(t, []byte("fresh"), got[0].Photo.FileReference)
	assert.Nil(t, got[0].EditDate, "refreshing a reference must not mark the message edited")
}
