package tg

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReactionsHaveUnread(t *testing.T) {
	none := tg.MessageReactions{
		RecentReactions: []tg.MessagePeerReaction{
			{Unread: false, Reaction: &tg.ReactionEmoji{Emoticon: "👍"}},
		},
	}
	assert.False(t, reactionsHaveUnread(none))

	some := tg.MessageReactions{
		RecentReactions: []tg.MessagePeerReaction{
			{Unread: false, Reaction: &tg.ReactionEmoji{Emoticon: "👍"}},
			{Unread: true, Reaction: &tg.ReactionEmoji{Emoticon: "❤"}},
		},
	}
	assert.True(t, reactionsHaveUnread(some))

	assert.False(t, reactionsHaveUnread(tg.MessageReactions{}))
}

func TestConvertMessage_HasUnreadReactions(t *testing.T) {
	raw := &tg.Message{
		ID: 5, Date: 1700000000, Out: true, Message: "hi",
		Reactions: tg.MessageReactions{
			Results: []tg.ReactionCount{
				{Reaction: &tg.ReactionEmoji{Emoticon: "❤"}, Count: 1},
			},
			RecentReactions: []tg.MessagePeerReaction{
				{Unread: true, Reaction: &tg.ReactionEmoji{Emoticon: "❤"}},
			},
		},
	}
	msg, ok := convertMessage(raw, 10)
	require.True(t, ok)
	assert.True(t, msg.HasUnreadReactions)

	plain := &tg.Message{ID: 6, Date: 1700000000, Message: "no reactions"}
	msg2, ok := convertMessage(plain, 10)
	require.True(t, ok)
	assert.False(t, msg2.HasUnreadReactions)
}

func TestConvertReactions_PreservesCustomEmoji(t *testing.T) {
	got := convertReactions(tg.MessageReactions{Results: []tg.ReactionCount{{
		Reaction: &tg.ReactionCustomEmoji{DocumentID: 77},
		Count:    3,
	}}})
	require.Len(t, got, 1)
	assert.Equal(t, int64(77), got[0].CustomEmojiID)
	assert.NotEmpty(t, got[0].Emoji, "custom reactions need a visible fallback before alt hydration")
	assert.Equal(t, 3, got[0].Count)
}

func TestConvertMessage_CommentsAndThreadAddress(t *testing.T) {
	replies := tg.MessageReplies{Comments: true, Replies: 12}
	replies.SetChannelID(99)
	raw := &tg.Message{
		ID: 5, Date: 1700000000,
		ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 3, ReplyToTopID: 1},
	}
	raw.SetReplies(replies)

	msg, ok := convertMessage(raw, 10)
	require.True(t, ok)
	assert.Equal(t, 3, msg.ReplyToMsgID)
	assert.Equal(t, 1, msg.ThreadRootID)
	assert.True(t, msg.HasComments)
	assert.Equal(t, 12, msg.RepliesCount)
	assert.Equal(t, int64(99), msg.DiscussionChatID)
}

func TestConvertMessage_Mentioned(t *testing.T) {
	raw := &tg.Message{ID: 7, Date: 1700000000, Mentioned: true, Message: "@you hi"}
	out, ok := convertMessage(raw, 1)
	require.True(t, ok)
	assert.True(t, out.Mentioned)

	plain := &tg.Message{ID: 8, Date: 1700000000, Message: "hi"}
	out2, ok := convertMessage(plain, 1)
	require.True(t, ok)
	assert.False(t, out2.Mentioned)
}

func TestConvertMessageGroupedID(t *testing.T) {
	raw := &tg.Message{ID: 10, Message: "part", Date: 1700000000}
	raw.SetGroupedID(9988776655)
	got, ok := convertMessage(raw, 42)
	if !ok {
		t.Fatalf("convertMessage returned ok=false")
	}
	if got.GroupedID != 9988776655 {
		t.Fatalf("GroupedID = %d, want 9988776655", got.GroupedID)
	}
}

func TestConvertMessageNoGroupedID(t *testing.T) {
	raw := &tg.Message{ID: 11, Message: "solo", Date: 1700000000}
	got, _ := convertMessage(raw, 42)
	if got.GroupedID != 0 {
		t.Fatalf("GroupedID = %d, want 0", got.GroupedID)
	}
}
