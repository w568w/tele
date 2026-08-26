package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
)

func TestApplyUnreadMessage_CountsFreshInboundID(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1, Title: "A"})

	assert.True(t, s.ApplyUnreadMessage(1, 100))
	c, _ := s.GetChat(1)
	assert.Equal(t, 1, c.UnreadCount)

	assert.True(t, s.ApplyUnreadMessage(1, 200))
	c, _ = s.GetChat(1)
	assert.Equal(t, 2, c.UnreadCount)
}

// A daemon may replay the same update on attach or reconnect (#183); counting
// twice would inflate what a CLI reports.
func TestApplyUnreadMessage_SameIDCountsOnce(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1})
	require.True(t, s.ApplyUnreadMessage(1, 100))

	assert.False(t, s.ApplyUnreadMessage(1, 100))
	c, _ := s.GetChat(1)
	assert.Equal(t, 1, c.UnreadCount)
}

// getDifference catch-up delivers messages already read on another client.
func TestApplyUnreadMessage_AtOrBelowReadPointerIsNoop(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1, ReadInboxMaxID: 100})

	assert.False(t, s.ApplyUnreadMessage(1, 100))
	assert.False(t, s.ApplyUnreadMessage(1, 50))
	c, _ := s.GetChat(1)
	assert.Equal(t, 0, c.UnreadCount)

	assert.True(t, s.ApplyUnreadMessage(1, 101))
	c, _ = s.GetChat(1)
	assert.Equal(t, 1, c.UnreadCount)
}

func TestApplyUnreadMessage_UnknownChatNoop(t *testing.T) {
	s := store.NewMemory()
	assert.False(t, s.ApplyUnreadMessage(42, 1))
}

// SetChat carries the authoritative dialog-list count; session tracking must not
// be added on top of a number that already includes those messages.
func TestApplyUnreadMessage_SetChatRebasesAndDropsTracking(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1})
	require.True(t, s.ApplyUnreadMessage(1, 100))

	s.SetChat(domain.Chat{ID: 1, UnreadCount: 7})
	c, _ := s.GetChat(1)
	assert.Equal(t, 7, c.UnreadCount)

	// The tracked set was dropped, so the same ID counts again on top of 7.
	assert.True(t, s.ApplyUnreadMessage(1, 100))
	c, _ = s.GetChat(1)
	assert.Equal(t, 8, c.UnreadCount)
}

func TestApplyUnreadMessage_AddsOnTopOfServerBaseline(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1, UnreadCount: 3, ReadInboxMaxID: 10})

	require.True(t, s.ApplyUnreadMessage(1, 11))
	require.True(t, s.ApplyUnreadMessage(1, 12))
	c, _ := s.GetChat(1)
	assert.Equal(t, 5, c.UnreadCount)
}

func TestRemoveMessages_DropsTrackedUnreadMessages(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1, ReadInboxMaxID: 10})
	for _, id := range []int{11, 12} {
		require.True(t, store.ApplyIncomingMessage(s, domain.Message{ID: id, ChatID: 1, IsService: true}))
	}

	s.RemoveMessages(1, []int{11, 12})

	c, _ := s.GetChat(1)
	assert.Equal(t, 0, c.UnreadCount)
}

// On restart the persisted count becomes the baseline, so counting resumes from
// it instead of restarting at zero.
func TestApplyUnreadMessage_ResumesFromPersistedCountAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	log := zap.NewNop()

	s, err := store.NewSQLite(path, log)
	require.NoError(t, err)
	s.SetChat(domain.Chat{ID: 1, Title: "A", UnreadCount: 4, ReadInboxMaxID: 10})
	require.NoError(t, s.Close())

	s2, err := store.NewSQLite(path, log)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s2.Close() })

	c, ok := s2.GetChat(1)
	require.True(t, ok)
	require.Equal(t, 4, c.UnreadCount)

	require.True(t, s2.ApplyUnreadMessage(1, 11))
	c, _ = s2.GetChat(1)
	assert.Equal(t, 5, c.UnreadCount)
}

// Regression: messages load lazily, so a chat not opened this session has an
// empty in-memory slice. The old message-scan recompute zeroed its count on any
// read event; the tracked set must survive instead.
func TestUpdateChatReadMaxID_KeepsUnreadAbovePointerWithoutLoadedMessages(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1, ReadInboxMaxID: 10})
	require.True(t, s.ApplyUnreadMessage(1, 11))
	require.True(t, s.ApplyUnreadMessage(1, 12))
	require.True(t, s.ApplyUnreadMessage(1, 13))

	// Read up to 12 on another client; 13 stays unread. No messages were ever
	// appended to this chat.
	require.True(t, s.UpdateChatReadMaxID(1, 12))

	c, _ := s.GetChat(1)
	assert.Equal(t, 12, c.ReadInboxMaxID)
	assert.Equal(t, 1, c.UnreadCount)
}

func TestUpdateChatReadMaxID_ClearsCountWhenPointerPassesEverything(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1, ReadInboxMaxID: 10})
	require.True(t, s.ApplyUnreadMessage(1, 11))
	require.True(t, s.ApplyUnreadMessage(1, 12))

	require.True(t, s.UpdateChatReadMaxID(1, 12))
	c, _ := s.GetChat(1)
	assert.Equal(t, 0, c.UnreadCount)
}

// Pruning must remove the IDs, not just the count: a message re-delivered after
// being read stays out of the count.
func TestUpdateChatReadMaxID_PrunedIDsDoNotCountAgain(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1, ReadInboxMaxID: 10})
	require.True(t, s.ApplyUnreadMessage(1, 11))
	require.True(t, s.UpdateChatReadMaxID(1, 11))

	assert.False(t, s.ApplyUnreadMessage(1, 11))
	c, _ := s.GetChat(1)
	assert.Equal(t, 0, c.UnreadCount)
}

// An advancing pointer makes the dialog-list baseline stale: its messages are
// identified by a number only, and some of them have now been read.
func TestUpdateChatReadMaxID_DropsStaleServerBaseline(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1, UnreadCount: 5, ReadInboxMaxID: 10})
	require.True(t, s.ApplyUnreadMessage(1, 20))
	c, _ := s.GetChat(1)
	require.Equal(t, 6, c.UnreadCount)

	require.True(t, s.UpdateChatReadMaxID(1, 15))

	c, _ = s.GetChat(1)
	assert.Equal(t, 1, c.UnreadCount, "only the locally observed message above 15 survives")
}

// unreadChat seeds a chat whose dialog-list baseline says unread messages sit
// above readMaxID, with history loaded from firstID to lastID as inbound.
func unreadChat(t *testing.T, s store.Store, unread, readMaxID, firstID, lastID int) {
	t.Helper()
	msgs := make([]domain.Message, 0, lastID-firstID+1)
	for id := firstID; id <= lastID; id++ {
		msgs = append(msgs, domain.Message{ID: id, ChatID: 1, Date: time.Unix(int64(id), 0)})
	}
	s.SetChat(domain.Chat{
		ID:             1,
		Peer:           domain.Peer{ID: 1, Type: domain.PeerUser},
		UnreadCount:    unread,
		ReadInboxMaxID: readMaxID,
		LastMessage:    &msgs[len(msgs)-1],
	})
	s.SetMessages(1, msgs)
}

// Opening a chat marks only the first screen read. The badge must count down by
// what was read rather than clearing: the baseline is a bare number, but once
// the chat is open the messages the pointer moved over are known by ID.
func TestUpdateChatReadMaxID_PartialReadKeepsTheRestOfTheBaseline(t *testing.T) {
	s := store.NewMemory()
	unreadChat(t, s, 10, 100, 95, 110) // 101..110 unread, 95..100 already read

	require.True(t, s.UpdateChatReadMaxID(1, 103)) // the first screen ends at 103

	c, _ := s.GetChat(1)
	assert.Equal(t, 7, c.UnreadCount, "the messages below the viewport are still unread")
}

func TestUpdateChatReadMaxID_ReadingToTheNewestClearsTheBaseline(t *testing.T) {
	s := store.NewMemory()
	unreadChat(t, s, 10, 100, 95, 110)

	require.True(t, s.UpdateChatReadMaxID(1, 110))

	c, _ := s.GetChat(1)
	assert.Equal(t, 0, c.UnreadCount)
}

// Own outgoing messages are not part of the unread count, so passing over them
// must not consume the baseline.
func TestUpdateChatReadMaxID_PartialReadIgnoresOutgoing(t *testing.T) {
	s := store.NewMemory()
	msgs := make([]domain.Message, 0, 16)
	for id := 95; id <= 110; id++ {
		msgs = append(msgs, domain.Message{ID: id, ChatID: 1, IsOut: id == 102, Date: time.Unix(int64(id), 0)})
	}
	s.SetChat(domain.Chat{
		ID: 1, Peer: domain.Peer{ID: 1, Type: domain.PeerUser},
		UnreadCount: 9, ReadInboxMaxID: 100, // 101..110 minus our own 102
		LastMessage: &msgs[len(msgs)-1],
	})
	s.SetMessages(1, msgs)

	require.True(t, s.UpdateChatReadMaxID(1, 103)) // read 101, our own 102, and 103

	c, _ := s.GetChat(1)
	assert.Equal(t, 7, c.UnreadCount, "only the two incoming messages count as read")
}

// Messages that arrived this session are counted by ID on top of the baseline,
// so reading them must not also consume baseline entries.
func TestUpdateChatReadMaxID_PartialReadDoesNotDoubleCountTrackedMessages(t *testing.T) {
	s := store.NewMemory()
	unreadChat(t, s, 10, 100, 95, 110)
	s.AppendMessage(domain.Message{ID: 111, ChatID: 1, Date: time.Unix(111, 0)})
	require.True(t, s.ApplyUnreadMessage(1, 111))
	c, _ := s.GetChat(1)
	require.Equal(t, 11, c.UnreadCount)

	// Read past the tracked message but not to the end of the baseline run.
	require.True(t, s.UpdateChatReadMaxID(1, 105))

	c, _ = s.GetChat(1)
	assert.Equal(t, 6, c.UnreadCount, "five baseline messages read; 111 was counted separately")
}

func TestUpdateChatReadMaxID_NoAdvanceLeavesCountUntouched(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1, ReadInboxMaxID: 10})
	require.True(t, s.ApplyUnreadMessage(1, 11))

	assert.False(t, s.UpdateChatReadMaxID(1, 10))
	c, _ := s.GetChat(1)
	assert.Equal(t, 1, c.UnreadCount)
	assert.Equal(t, 10, c.ReadInboxMaxID)
}
