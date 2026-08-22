package tg

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStickerRefsKeepTheSendAndPreviewReference(t *testing.T) {
	doc := &tg.Document{
		ID: 7, AccessHash: 8, FileReference: []byte{9}, MimeType: "video/webm",
		Thumbs:     []tg.PhotoSizeClass{&tg.PhotoSize{Type: "m", W: 320, H: 320}},
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{Alt: "cat"}},
	}

	got := stickerRefs([]tg.DocumentClass{doc})

	require.Len(t, got, 1)
	assert.Equal(t, int64(7), got[0].Document.ID)
	assert.Equal(t, "m", got[0].Document.ThumbSize)
	assert.Equal(t, "cat", got[0].Emoji)
}
