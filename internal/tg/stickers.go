package tg

import (
	"context"

	"github.com/gotd/td/tg"
	"github.com/sorokin-vladimir/tele/internal/domain"
)

// GetStickerCatalog returns immediately usable special tabs plus references to
// installed packs. Pack documents are fetched lazily by GetStickerPack.
func (c *GotdClient) GetStickerCatalog(ctx context.Context) (domain.StickerCatalog, error) {
	api, err := c.acquireAPI()
	if err != nil {
		return domain.StickerCatalog{}, err
	}

	var favorites, recent []tg.DocumentClass
	var sets []tg.StickerSet
	err = WithRetry(ctx, func() error {
		faved, err := api.MessagesGetFavedStickers(ctx, 0)
		if err != nil {
			return err
		}
		if f, ok := faved.(*tg.MessagesFavedStickers); ok {
			favorites = f.Stickers
		}
		r, err := api.MessagesGetRecentStickers(ctx, &tg.MessagesGetRecentStickersRequest{})
		if err != nil {
			return err
		}
		if recents, ok := r.(*tg.MessagesRecentStickers); ok {
			recent = recents.Stickers
		}
		all, err := api.MessagesGetAllStickers(ctx, 0)
		if err != nil {
			return err
		}
		if installed, ok := all.(*tg.MessagesAllStickers); ok {
			sets = installed.Sets
		}
		return nil
	})
	if err != nil {
		return domain.StickerCatalog{}, err
	}
	catalog := domain.StickerCatalog{
		Favorites: stickerRefs(favorites),
		Recent:    stickerRefs(recent),
		Packs:     make([]domain.StickerPackRef, 0, len(sets)),
	}
	for _, set := range sets {
		if set.Archived || set.Emojis {
			continue
		}
		catalog.Packs = append(catalog.Packs, domain.StickerPackRef{
			ID: set.ID, AccessHash: set.AccessHash, Title: set.Title, ShortName: set.ShortName,
		})
	}
	return catalog, nil
}

func (c *GotdClient) GetStickerPack(ctx context.Context, pack domain.StickerPackRef) ([]domain.StickerRef, error) {
	api, err := c.acquireAPI()
	if err != nil {
		return nil, err
	}
	var docs []tg.DocumentClass
	err = WithRetry(ctx, func() error {
		result, err := api.MessagesGetStickerSet(ctx, &tg.MessagesGetStickerSetRequest{
			Stickerset: &tg.InputStickerSetID{ID: pack.ID, AccessHash: pack.AccessHash},
		})
		if err != nil {
			return err
		}
		if set, ok := result.(*tg.MessagesStickerSet); ok {
			docs = set.Documents
		}
		return nil
	})
	return stickerRefs(docs), err
}

func stickerRefs(docs []tg.DocumentClass) []domain.StickerRef {
	out := make([]domain.StickerRef, 0, len(docs))
	for _, class := range docs {
		doc, ok := class.(*tg.Document)
		if !ok {
			continue
		}
		emoji := ""
		for _, attr := range doc.Attributes {
			if sticker, ok := attr.(*tg.DocumentAttributeSticker); ok {
				emoji = sticker.Alt
				break
			}
		}
		out = append(out, domain.StickerRef{Document: *buildDocumentRef(doc), Emoji: emoji})
	}
	return out
}

func (c *GotdClient) SendSticker(ctx context.Context, peer domain.Peer, sticker domain.StickerRef, replyToMsgID, threadRootID int, randomID int64) (int, error) {
	doc := sticker.Document
	media := &tg.InputMediaDocument{
		ID: &tg.InputDocument{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference},
	}
	return c.SendMedia(ctx, SendMediaParams{
		Peer: peer, Media: media, ReplyToMsgID: replyToMsgID,
		ThreadRootID: threadRootID, RandomID: randomID,
	})
}
