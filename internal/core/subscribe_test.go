package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/config"
	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/core/state"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
	internaltg "github.com/sorokin-vladimir/tele/internal/tg"
)

// stubConn answers GetHistory and nothing else. The embedded interface is nil,
// so any other call panics — which is the point: a test that reaches further
// than it declared should fail loudly.
type stubConn struct {
	internaltg.Client
	history   []domain.Message
	pages     map[int][]domain.Message
	messages  map[int]domain.Message
	windows   map[int][]domain.Message
	calls     atomic.Int32
	release   chan struct{}
	replies   []domain.Message
	replyPeer domain.Peer

	refreshPeer domain.Peer
}

func (s *stubConn) Connect(context.Context, *config.Config, *internaltg.AuthFlow, chan<- struct{}, func(int64, string)) error {
	return nil
}

func (s *stubConn) Updates() <-chan store.Event { return nil }

func (s *stubConn) GetHistory(_ context.Context, _ domain.Peer, offsetID int, _ int) ([]domain.Message, error) {
	s.calls.Add(1)
	if s.release != nil {
		<-s.release
	}
	if page, ok := s.pages[offsetID]; ok {
		return page, nil
	}
	return s.history, nil
}

func (s *stubConn) GetHistoryWindow(_ context.Context, _ domain.Peer, anchorID, _, _ int) ([]domain.Message, error) {
	s.calls.Add(1)
	if s.release != nil {
		<-s.release
	}
	if window, ok := s.windows[anchorID]; ok {
		return window, nil
	}
	if msg, ok := s.messages[anchorID]; ok {
		return []domain.Message{msg}, nil
	}
	return s.history, nil
}

func (s *stubConn) GetReplies(_ context.Context, peer domain.Peer, _, _, _ int) ([]domain.Message, error) {
	s.calls.Add(1)
	s.replyPeer = peer
	return s.replies, nil
}

func (s *stubConn) RefreshMessage(_ context.Context, _ domain.Peer, id int) (domain.Message, error) {
	return s.messages[id], nil
}

func (s *stubConn) RefreshMessages(_ context.Context, peer domain.Peer, ids []int) ([]domain.Message, error) {
	s.refreshPeer = peer
	out := make([]domain.Message, 0, len(ids))
	for _, id := range ids {
		if msg, ok := s.messages[id]; ok {
			out = append(out, msg)
		}
	}
	return out, nil
}

func TestHydrateReplyPreviews_UsesExternalReplyPeer(t *testing.T) {
	targetPeer := domain.Peer{ID: 99, Type: domain.PeerSuperGroup, AccessHash: 7}
	c := &stubConn{messages: map[int]domain.Message{
		50: {ID: 50, ChatID: 99, SenderName: "Alice", Text: "original"},
	}}
	o, _ := newOwnerWithClient(t, c)
	reply := domain.Message{
		ID: 7, ChatID: 1, ReplyToMsgID: 50,
		ReplyTarget: &domain.MessageTarget{ChatID: 99, Peer: targetPeer, Title: "Group", MsgID: 50},
	}

	got, changed, err := o.hydrateReplyPreviews(context.Background(), domain.Peer{ID: 1, Type: domain.PeerUser}, []domain.Message{reply}, []domain.Message{reply})

	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, targetPeer, c.refreshPeer)
	require.NotNil(t, got[0].ReplyPreview)
	assert.Equal(t, "original", got[0].ReplyPreview.Text)
}

func newOwnerWithClient(t *testing.T, c Connection) (*Owner, *state.State) {
	t.Helper()
	s := state.New(store.NewMemory())
	cfg := &config.Config{}
	cfg.UI.HistoryLimit = 20
	o := New(cfg, zap.NewNop(), s, c, nopNotifier{})
	return o, s
}

func TestOwner_SubscribeDeliversInitialContents(t *testing.T) {
	o, events, st := newTestOwner(t)
	_ = events
	st.SetChat(domain.Chat{ID: 1, Title: "Ada"})

	id := o.Subscribe(project.ChatListWindow{Limit: 10})

	d, ok := recvDelta(t, o.Deltas())
	require.True(t, ok)
	assert.Equal(t, id, d.Sub)
	require.NotNil(t, d.ChatList)
	assert.Equal(t, project.ChatListReset, d.ChatList.Kind)
	require.Len(t, d.ChatList.Rows, 1)
	assert.Equal(t, "Ada", d.ChatList.Rows[0].Title)
}

func TestBackfill_RefreshesCommentMetadataOnCachedChannelPosts(t *testing.T) {
	peer := domain.Peer{ID: 7, Type: domain.PeerChannel, AccessHash: 70}
	fresh := domain.Message{
		ID: 10, ChatID: 7, Text: "post", HasComments: true,
		RepliesCount: 3, DiscussionChatID: 8,
	}
	c := &stubConn{messages: map[int]domain.Message{10: fresh}}
	o, s := newOwnerWithClient(t, c)
	s.Store().SetChat(domain.Chat{ID: 7, Peer: peer})
	s.Store().SetMessages(7, []domain.Message{{ID: 10, ChatID: 7, Text: "post"}})
	w := project.ChatWindow{ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorNewest}}
	id, _ := o.registry.Subscribe(w)

	o.backfill(context.Background(), id, w)

	msgs := s.Store().Messages(7)
	require.Len(t, msgs, 1)
	assert.True(t, msgs[0].HasComments)
	assert.Equal(t, 3, msgs[0].RepliesCount)
	assert.Equal(t, int64(8), msgs[0].DiscussionChatID)
}

func TestBackfill_ThreadUsesItsWindowPeerWithoutADialog(t *testing.T) {
	peer := domain.Peer{ID: 8, Type: domain.PeerSuperGroup, AccessHash: 80}
	c := &stubConn{replies: []domain.Message{
		{ID: 40, ChatID: 8, ThreadRootID: 40},
		{ID: 41, ChatID: 8, ReplyToMsgID: 40, ThreadRootID: 40},
	}}
	o, s := newOwnerWithClient(t, c)
	s.Store().SetMessages(8, c.replies[:1])
	w := project.ChatWindow{
		ChatID: 8, ThreadRootID: 40, ThreadPeer: peer,
		Anchor: project.Anchor{Kind: project.AnchorNewest}, Before: 2,
	}

	o.backfill(context.Background(), 1, w)

	assert.Equal(t, peer, c.replyPeer)
	assert.Len(t, s.Store().Messages(8), 2)
	_, exists := s.Store().GetChat(8)
	assert.False(t, exists)
}

func TestOwner_UnsubscribedClientGetsNothing(t *testing.T) {
	o, events, st := newTestOwner(t)
	st.SetChat(domain.Chat{ID: 1})
	id := o.Subscribe(project.ChatListWindow{Limit: 10})
	_, _ = recvDelta(t, o.Deltas())
	o.Unsubscribe(id)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.RunUpdates(ctx)

	events <- store.Event{Kind: store.EventMuteUpdate, ChatID: 1, Muted: true}

	_, more := recvDelta(t, o.Deltas())
	assert.False(t, more, "an unsubscribed window must cost nothing")
}

func TestOwner_SubscribingToAnUnfilledChatWindowBackfills(t *testing.T) {
	c := &stubConn{history: []domain.Message{
		{ID: 1, ChatID: 7, Text: "old", Date: time.Unix(1, 0)},
		{ID: 2, ChatID: 7, Text: "new", Date: time.Unix(2, 0)},
	}}
	o, s := newOwnerWithClient(t, c)
	s.Store().SetChat(domain.Chat{ID: 7, Peer: domain.Peer{ID: 7}})

	o.Subscribe(project.ChatWindow{
		ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorNewest}, Before: 20,
	})

	// The initial delta is the empty window; the fetched history follows.
	_, _ = recvDelta(t, o.Deltas())
	d, ok := recvDelta(t, o.Deltas())
	require.True(t, ok, "the client must not have to know the store was empty")
	require.NotNil(t, d.Chat)
	assert.Len(t, d.Chat.Contents.Messages, 2)
}

func TestOwner_FirstUnreadBackfillLoadsEveryMissingPage(t *testing.T) {
	c := &stubConn{pages: map[int][]domain.Message{
		0:  testMessages(15, 16),
		15: testMessages(13, 14),
		13: testMessages(11, 12),
	}}
	o, s := newOwnerWithClient(t, c)
	o.Config().UI.HistoryLimit = 2
	s.Store().SetChat(domain.Chat{
		ID: 7, Peer: domain.Peer{ID: 7}, UnreadCount: 6, ReadInboxMaxID: 10,
	})
	s.Store().SetMessages(7, testMessages(1, 2, 3, 4, 5, 6, 7, 8, 9, 10))

	o.Subscribe(project.ChatWindow{
		ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorFirstUnread}, Before: 2,
	})

	_, _ = recvDelta(t, o.Deltas())
	d, ok := recvDelta(t, o.Deltas())
	require.True(t, ok)
	require.NotNil(t, d.Chat)
	assert.Equal(t, project.ChatReset, d.Chat.Kind)
	assert.Equal(t, []int{9, 10, 11, 12, 13, 14, 15, 16}, msgIDs(d.Chat.Contents.Messages))
	assert.Equal(t, int32(3), c.calls.Load(), "the unread range spans three server pages")
}

func TestOwner_OpenRepairsLargeCachedSupergroupGap(t *testing.T) {
	c := &stubConn{pages: map[int][]domain.Message{
		8: testMessages(3, 4, 5, 6, 7),
		3: testMessages(1, 2),
	}}
	o, s := newOwnerWithClient(t, c)
	o.Config().UI.HistoryLimit = 5
	s.Store().SetChat(domain.Chat{
		ID: 7, Peer: domain.Peer{ID: 7, Type: domain.PeerSuperGroup}, ReadInboxMaxID: 9,
	})
	s.Store().SetMessages(7, testMessages(1, 2, 8, 9))

	o.Subscribe(project.ChatWindow{
		ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorFirstUnread}, Before: 2,
	})

	_, _ = recvDelta(t, o.Deltas())
	require.Eventually(t, func() bool {
		return len(s.Store().Messages(7)) == 9
	}, time.Second, time.Millisecond)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}, msgIDs(s.Store().Messages(7)))
	assert.Equal(t, int32(2), c.calls.Load(), "repair pages backward until they overlap the lower cached range")
}

func TestOwner_ReopeningSameWindowBackfillsNewUnreadTail(t *testing.T) {
	c := &stubConn{history: []domain.Message{{ID: 4, ChatID: 7, Date: time.Unix(4, 0)}}}
	o, s := newOwnerWithClient(t, c)
	s.Store().SetChat(domain.Chat{ID: 7, Peer: domain.Peer{ID: 7}})
	s.Store().SetMessages(7, []domain.Message{
		{ID: 1, ChatID: 7, Date: time.Unix(1, 0)},
		{ID: 2, ChatID: 7, Date: time.Unix(2, 0)},
		{ID: 3, ChatID: 7, Date: time.Unix(3, 0)},
	})

	w := project.ChatWindow{ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorFirstUnread}, Before: 1}
	id := o.Subscribe(w)

	_, _ = recvDelta(t, o.Deltas())
	assert.Zero(t, c.calls.Load(), "the store already held everything the window asked for")

	s.Store().SetChat(domain.Chat{
		ID: 7, Peer: domain.Peer{ID: 7}, UnreadCount: 1, ReadInboxMaxID: 3,
		LastMessage: &c.history[0],
	})
	o.MoveWindow(id, w)

	require.Eventually(t, func() bool {
		return len(s.Store().Messages(7)) == 4
	}, time.Second, time.Millisecond)
	assert.Equal(t, int32(1), c.calls.Load())
}

func TestOwner_FullWindowHydratesAnOutOfWindowReplyPreview(t *testing.T) {
	c := &stubConn{messages: map[int]domain.Message{
		1: {ID: 1, ChatID: 7, SenderID: 9, SenderName: "Ada", Text: "old message"},
	}}
	o, s := newOwnerWithClient(t, c)
	s.Store().SetChat(domain.Chat{ID: 7, Peer: domain.Peer{ID: 7}})
	s.Store().SetMessages(7, []domain.Message{{ID: 100, ChatID: 7, Text: "reply", ReplyToMsgID: 1}})

	o.Subscribe(project.ChatWindow{ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorNewest}})
	_, _ = recvDelta(t, o.Deltas())
	d, ok := recvDelta(t, o.Deltas())

	require.True(t, ok)
	require.NotNil(t, d.Chat)
	assert.Equal(t, project.ChatUpdate, d.Chat.Kind)
	require.NotNil(t, d.Chat.Message.ReplyPreview)
	assert.Equal(t, "Ada", d.Chat.Message.ReplyPreview.SenderName)
	assert.Equal(t, "old message", d.Chat.Message.ReplyPreview.Text)
	assert.Zero(t, c.calls.Load(), "a full window must not page through history to resolve a reply")
}

func TestOwner_MessageAnchorFetchesTheTargetDirectly(t *testing.T) {
	c := &stubConn{messages: map[int]domain.Message{
		1: {ID: 1, ChatID: 7, Text: "target", Date: time.Unix(1, 0)},
	}}
	o, s := newOwnerWithClient(t, c)
	s.Store().SetChat(domain.Chat{ID: 7, Peer: domain.Peer{ID: 7}})
	s.Store().SetMessages(7, []domain.Message{{ID: 100, ChatID: 7, Text: "reply", Date: time.Unix(100, 0)}})

	o.Subscribe(project.ChatWindow{
		ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorMessage, MsgID: 1}, Before: 20,
	})
	_, _ = recvDelta(t, o.Deltas())
	d, ok := recvDelta(t, o.Deltas())

	require.True(t, ok)
	require.NotNil(t, d.Chat)
	assert.Equal(t, 1, d.Chat.Contents.AnchorMsgID)
	require.NotEmpty(t, d.Chat.Contents.Messages)
	assert.Equal(t, 1, d.Chat.Contents.Messages[len(d.Chat.Contents.Messages)-1].ID)
}

func TestOwner_MessageAnchorLoadsNewerWithoutCrossingAStoredGap(t *testing.T) {
	release := make(chan struct{})
	c := &stubConn{windows: map[int][]domain.Message{
		5: {
			{ID: 4, ChatID: 7, Date: time.Unix(4, 0)},
			{ID: 5, ChatID: 7, Date: time.Unix(5, 0)},
			{ID: 6, ChatID: 7, Date: time.Unix(6, 0)},
		},
		6: {
			{ID: 6, ChatID: 7, Date: time.Unix(6, 0)},
			{ID: 7, ChatID: 7, Date: time.Unix(7, 0)},
		},
	}, release: release}
	o, s := newOwnerWithClient(t, c)
	s.Store().SetChat(domain.Chat{ID: 7, Peer: domain.Peer{ID: 7}})
	s.Store().SetMessages(7, []domain.Message{{ID: 100, ChatID: 7, Date: time.Unix(100, 0)}})
	id := o.Subscribe(project.ChatWindow{ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorNewest}})
	for len(o.Deltas()) > 0 {
		<-o.Deltas()
	}

	o.MoveWindow(id, project.ChatWindow{
		ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorMessage, MsgID: 5}, Before: 1, After: 1,
	})
	require.Eventually(t, func() bool { return c.calls.Load() == 1 }, time.Second, time.Millisecond)
	// A second scroll while the first page is in flight must be retained.
	o.MoveWindow(id, project.ChatWindow{
		ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorMessage, MsgID: 5}, Before: 1, After: 2,
	})
	close(release)
	d, ok := recvDelta(t, o.Deltas())
	require.True(t, ok)
	require.NotNil(t, d.Chat)
	require.Equal(t, project.ChatOlder, d.Chat.Kind)
	require.Empty(t, d.Chat.Messages)

	d, ok = recvDelta(t, o.Deltas())
	require.True(t, ok)
	require.NotNil(t, d.Chat)
	require.Equal(t, project.ChatReset, d.Chat.Kind)
	assert.Equal(t, []int{4, 5, 6}, msgIDs(d.Chat.Contents.Messages))

	d, ok = recvDelta(t, o.Deltas())
	require.True(t, ok)
	require.NotNil(t, d.Chat)
	assert.Equal(t, project.ChatNewer, d.Chat.Kind)
	assert.Equal(t, []int{7}, msgIDs(d.Chat.Messages))
}

func TestOwner_ReturnToNewestSupersedesAnInFlightMessageAnchor(t *testing.T) {
	release := make(chan struct{})
	c := &stubConn{
		windows: map[int][]domain.Message{5: {{ID: 5, ChatID: 7, Date: time.Unix(5, 0)}}},
		release: release,
	}
	o, s := newOwnerWithClient(t, c)
	s.Store().SetChat(domain.Chat{ID: 7, Peer: domain.Peer{ID: 7}})
	s.Store().SetMessages(7, []domain.Message{{ID: 100, ChatID: 7, Date: time.Unix(100, 0)}})
	id := o.Subscribe(project.ChatWindow{ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorNewest}})
	for len(o.Deltas()) > 0 {
		<-o.Deltas()
	}

	o.MoveWindow(id, project.ChatWindow{
		ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorMessage, MsgID: 5}, Before: 1, After: 1,
	})
	require.Eventually(t, func() bool { return c.calls.Load() == 1 }, time.Second, time.Millisecond)
	o.MoveWindow(id, project.ChatWindow{ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorNewest}})
	close(release)

	d, ok := recvDelta(t, o.Deltas())
	require.True(t, ok)
	require.NotNil(t, d.Chat)
	assert.Equal(t, project.ChatOlder, d.Chat.Kind)
	_, more := recvDelta(t, o.Deltas())
	assert.False(t, more, "the completed old jump must not reset the live window")
}

func msgIDs(msgs []domain.Message) []int {
	ids := make([]int, len(msgs))
	for i, msg := range msgs {
		ids[i] = msg.ID
	}
	return ids
}

func TestOwner_ProjectionBurstIsDeliveredWithoutLoss(t *testing.T) {
	o, _, _ := newTestOwner(t)
	const count = 300
	deltas := make([]project.Delta, count)
	for i := range deltas {
		deltas[i].Sub = project.SubID(i + 1)
	}

	o.publish(deltas)

	for i := range deltas {
		d, ok := recvDelta(t, o.Deltas())
		require.True(t, ok)
		assert.Equal(t, project.SubID(i+1), d.Sub)
	}
}

func testMessages(ids ...int) []domain.Message {
	msgs := make([]domain.Message, len(ids))
	for i, id := range ids {
		msgs[i] = domain.Message{ID: id, ChatID: 7, Date: time.Unix(int64(id), 0)}
	}
	return msgs
}

// Rapid scroll-up fires many window moves. Without the guard each one starts its
// own fetch and the overlapping pages stack into a repeating date range (#120).
func TestOwner_ConcurrentBackfillsCollapseToOneFetch(t *testing.T) {
	c := &stubConn{release: make(chan struct{})}
	o, s := newOwnerWithClient(t, c)
	s.Store().SetChat(domain.Chat{ID: 7, Peer: domain.Peer{ID: 7}})
	w := project.ChatWindow{ChatID: 7, Anchor: project.Anchor{Kind: project.AnchorNewest}, Before: 20}
	id := o.Subscribe(w)

	for i := 0; i < 5; i++ {
		o.MoveWindow(id, project.ChatWindow{
			ChatID: 7, Anchor: w.Anchor, Before: 20 + i,
		})
	}
	// Let every goroutine reach the guard before releasing the first fetch.
	assert.Eventually(t, func() bool { return c.calls.Load() >= 1 }, time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	inFlight := c.calls.Load()
	close(c.release)

	assert.Equal(t, int32(1), inFlight, "one fetch per subscription may be in flight")
}
