package tg

import (
	"context"

	"github.com/gotd/td/tg"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
)

// hydrateCustomReactions resolves animated custom reactions to the Unicode alt
// carried by documentAttributeCustomEmoji. A terminal cannot draw the TGS
// animation in a border cell, but the server-provided alt preserves its meaning.
func (c *GotdClient) hydrateCustomReactions(ctx context.Context, api *tg.Client, msgs []domain.Message) {
	ids := make(map[int64]struct{})
	for _, msg := range msgs {
		for _, reaction := range msg.Reactions {
			if reaction.CustomEmojiID != 0 {
				ids[reaction.CustomEmojiID] = struct{}{}
			}
		}
	}
	if len(ids) == 0 {
		return
	}

	missing := make([]int64, 0, len(ids))
	c.customEmojiMu.RLock()
	for id := range ids {
		if _, ok := c.customEmoji[id]; !ok {
			missing = append(missing, id)
		}
	}
	c.customEmojiMu.RUnlock()

	if len(missing) > 0 {
		docs, err := api.MessagesGetCustomEmojiDocuments(ctx, missing)
		if err == nil {
			c.customEmojiMu.Lock()
			if c.customEmoji == nil {
				c.customEmoji = make(map[int64]string)
			}
			for _, id := range missing {
				c.customEmoji[id] = "◈"
			}
			for _, raw := range docs {
				doc, ok := raw.(*tg.Document)
				if !ok {
					continue
				}
				for _, attr := range doc.Attributes {
					if custom, ok := attr.(*tg.DocumentAttributeCustomEmoji); ok && custom.Alt != "" {
						c.customEmoji[doc.ID] = custom.Alt
						break
					}
				}
			}
			c.customEmojiMu.Unlock()
		}
	}

	c.customEmojiMu.RLock()
	defer c.customEmojiMu.RUnlock()
	for i := range msgs {
		for j := range msgs[i].Reactions {
			if alt := c.customEmoji[msgs[i].Reactions[j].CustomEmojiID]; alt != "" {
				msgs[i].Reactions[j].Emoji = alt
			}
		}
	}
}

func (c *GotdClient) hydrateEventCustomEmoji(ctx context.Context, evt store.Event) store.Event {
	api, err := c.acquireAPI()
	if err != nil {
		return evt
	}
	switch evt.Kind {
	case store.EventReactionsUpdate:
		msgs := []domain.Message{{Reactions: evt.Reactions}}
		c.hydrateCustomReactions(ctx, api, msgs)
		evt.Reactions = msgs[0].Reactions
	case store.EventNewMessage, store.EventEditMessage:
		msgs := []domain.Message{evt.Message}
		c.hydrateCustomReactions(ctx, api, msgs)
		evt.Message = msgs[0]
	}
	return evt
}
