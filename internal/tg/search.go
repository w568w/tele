package tg

import (
	"context"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// SearchContacts queries Telegram (contacts.search) for users matching q,
// returning matches as domain.Chat with valid peers (access hashes). Phase 1:
// users only — groups/channels are filtered out (issue #82).
func (c *GotdClient) SearchContacts(ctx context.Context, q string, limit int) ([]domain.Chat, error) {
	q = strings.TrimPrefix(strings.TrimSpace(q), "@")
	api, err := c.acquireAPI()
	if err != nil {
		return nil, err
	}
	found, err := api.ContactsSearch(ctx, &tg.ContactsSearchRequest{Q: q, Limit: limit})
	if err != nil {
		return nil, err
	}
	chats := usersFromContactsFound(found, limit)
	for _, ch := range chats {
		c.cachePeer(ch.Peer)
	}
	return chats, nil
}

// ResolveUsername resolves one exact public username to a user, group or
// channel with a usable peer/access hash.
func (c *GotdClient) ResolveUsername(ctx context.Context, username string) (domain.Chat, error) {
	api, err := c.acquireAPI()
	if err != nil {
		return domain.Chat{}, err
	}
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: strings.TrimPrefix(strings.TrimSpace(username), "@"),
	})
	if err != nil {
		return domain.Chat{}, err
	}
	if resolved == nil {
		return domain.Chat{}, &telerr.Error{Kind: telerr.NotFound, Op: "resolve username", Detail: "empty response"}
	}
	chat, ok := resolvedChat(resolved.Peer, resolved.Users, resolved.Chats)
	if !ok {
		return domain.Chat{}, &telerr.Error{Kind: telerr.PeerNotFound, Op: "resolve username"}
	}
	c.cachePeer(chat.Peer)
	return chat, nil
}

// ResolveChannel resolves the numeric channel id used by private t.me/c links.
// Telegram accepts an access hash of zero for channels already known to the
// current account, matching the official clients' private-link lookup.
func (c *GotdClient) ResolveChannel(ctx context.Context, channelID int64) (domain.Chat, error) {
	api, err := c.acquireAPI()
	if err != nil {
		return domain.Chat{}, err
	}
	result, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID},
	})
	if err != nil {
		return domain.Chat{}, err
	}
	var chats []tg.ChatClass
	switch result := result.(type) {
	case *tg.MessagesChats:
		chats = result.Chats
	case *tg.MessagesChatsSlice:
		chats = result.Chats
	}
	chat, ok := resolvedChat(&tg.PeerChannel{ChannelID: channelID}, nil, chats)
	if !ok {
		return domain.Chat{}, &telerr.Error{Kind: telerr.PeerNotFound, Op: "resolve channel"}
	}
	c.cachePeer(chat.Peer)
	return chat, nil
}

func resolvedChat(peer tg.PeerClass, users []tg.UserClass, chats []tg.ChatClass) (domain.Chat, bool) {
	id := peerIDFromPeer(peer)
	for _, raw := range users {
		if user, ok := raw.(*tg.User); ok && user.ID == id {
			return convertUser(user)
		}
	}
	for _, raw := range chats {
		switch chat := raw.(type) {
		case *tg.Chat:
			if chat.ID == id {
				return convertGroupChat(chat)
			}
		case *tg.Channel:
			if chat.ID == id {
				return convertChannel(chat)
			}
		}
	}
	return domain.Chat{}, false
}

// usersFromContactsFound maps the user peers of a contacts.search response to
// domain.Chat. MyResults (exact/contact matches) are listed before global
// Results; non-user peers, self, and duplicates are dropped, and the count is
// capped at limit.
func usersFromContactsFound(found *tg.ContactsFound, limit int) []domain.Chat {
	if found == nil {
		return nil
	}
	userMap := make(map[int64]*tg.User, len(found.Users))
	for _, u := range found.Users {
		if user, ok := u.(*tg.User); ok {
			userMap[user.ID] = user
		}
	}
	seen := make(map[int64]struct{})
	out := make([]domain.Chat, 0, limit)
	add := func(peers []tg.PeerClass) {
		for _, p := range peers {
			pu, ok := p.(*tg.PeerUser)
			if !ok {
				continue
			}
			if _, dup := seen[pu.UserID]; dup {
				continue
			}
			user := userMap[pu.UserID]
			chat, ok := convertUser(user)
			if !ok || user.Self {
				continue
			}
			seen[pu.UserID] = struct{}{}
			out = append(out, chat)
			if len(out) >= limit {
				return
			}
		}
	}
	add(found.MyResults)
	if len(out) < limit {
		add(found.Results)
	}
	return out
}
