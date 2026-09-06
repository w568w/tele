package domain

// ImportantUnread counts messages, not individual reactions. Revision changes
// when the underlying targets change even if both counts stay the same.
type ImportantUnread struct {
	Mentions  int
	Reactions int
	Loading   bool
	Failed    bool
	Revision  uint64
}

type ImportantKind uint8

const (
	ImportantMention ImportantKind = iota
	ImportantReaction
)

type UnreadPage struct {
	Messages []Message
	Count    int
}
