package core

import (
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// peer resolves the Telegram addressing of a chat the client named by ID.
// Commands take a chat ID because addressing is the owner's business: a client
// names what it sees on screen, and in v2 it may not share a process with the
// connection at all.
func (o *Owner) peer(chatID int64) (domain.Peer, error) {
	chat, ok := o.reader().GetChat(chatID)
	if !ok {
		return domain.Peer{}, &telerr.Error{Kind: telerr.PeerNotFound}
	}
	return chat.Peer, nil
}
