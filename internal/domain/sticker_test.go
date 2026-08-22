package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStickerPreviewSlot(t *testing.T) {
	media := &MediaRef{Kind: MediaSticker}

	slot, ok := StickerPreviewSlot(media, &DocumentRef{MimeType: "image/webp"})
	assert.True(t, ok)
	assert.Equal(t, DocFull, slot)

	slot, ok = StickerPreviewSlot(media, &DocumentRef{MimeType: "video/webm", ThumbSize: "m"})
	assert.True(t, ok)
	assert.Equal(t, DocThumb, slot)

	_, ok = StickerPreviewSlot(media, &DocumentRef{MimeType: "application/x-tgsticker"})
	assert.False(t, ok)
}
