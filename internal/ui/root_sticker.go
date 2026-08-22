package ui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/sorokin-vladimir/tele/internal/ui/keys"
	"github.com/sorokin-vladimir/tele/internal/ui/media"
	"github.com/sorokin-vladimir/tele/internal/ui/theme"
)

const (
	stickerPickerWidth = 48
	stickerPreviewCols = 16
	stickerPreviewRows = 8
	stickerListRows    = 6
)

type stickerTab struct {
	title   string
	pack    domain.StickerPackRef
	items   []domain.StickerRef
	loaded  bool
	loading bool
	err     string
}

type stickerPicker struct {
	gen     int
	tabs    []stickerTab
	tab     int
	cursor  int
	loading bool
	err     string
}

type stickerOwner interface {
	GetStickerCatalog(ctx context.Context) (domain.StickerCatalog, error)
	GetStickerPack(ctx context.Context, pack domain.StickerPackRef) ([]domain.StickerRef, error)
	FetchStickerPreview(ctx context.Context, sticker domain.StickerRef) (string, error)
	SendSticker(ctx context.Context, chatID int64, peer domain.Peer, sticker domain.StickerRef, replyToMsgID, threadRootID int) error
}

type stickerCatalogLoadedMsg struct {
	gen     int
	catalog domain.StickerCatalog
	err     error
}

type stickerPackLoadedMsg struct {
	gen    int
	packID int64
	items  []domain.StickerRef
	err    error
}

type stickerSendDoneMsg struct{ err error }

func (p *stickerPicker) activeTab() *stickerTab {
	if p == nil || p.tab < 0 || p.tab >= len(p.tabs) {
		return nil
	}
	return &p.tabs[p.tab]
}

func (p *stickerPicker) selected() (domain.StickerRef, bool) {
	tab := p.activeTab()
	if tab == nil || p.cursor < 0 || p.cursor >= len(tab.items) {
		return domain.StickerRef{}, false
	}
	return tab.items[p.cursor], true
}

func (p *stickerPicker) selectedID() int64 {
	if item, ok := p.selected(); ok {
		return item.Document.ID
	}
	return 0
}

func (m RootModel) openStickerPicker() (tea.Model, tea.Cmd) {
	owner, ok := m.owner.(stickerOwner)
	if !ok || m.chat == nil || m.currentChatID == 0 {
		return m, nil
	}
	if m.chat.Editing() {
		m.statusBar.SetStatus("Finish editing before sending a sticker")
		return m, nil
	}
	m.mentionPopup = nil
	m.stickerPickerGen++
	gen := m.stickerPickerGen
	m.stickerPicker = &stickerPicker{gen: gen, loading: true}
	return m, loadStickerCatalogCmd(m.ctx, owner, gen)
}

func loadStickerCatalogCmd(ctx context.Context, owner stickerOwner, gen int) tea.Cmd {
	return func() tea.Msg {
		catalog, err := owner.GetStickerCatalog(ctx)
		return stickerCatalogLoadedMsg{gen: gen, catalog: catalog, err: err}
	}
}

func loadStickerPackCmd(ctx context.Context, owner stickerOwner, gen int, pack domain.StickerPackRef) tea.Cmd {
	return func() tea.Msg {
		items, err := owner.GetStickerPack(ctx, pack)
		return stickerPackLoadedMsg{gen: gen, packID: pack.ID, items: items, err: err}
	}
}

func (m RootModel) handleStickerCatalogLoaded(msg stickerCatalogLoadedMsg) (RootModel, tea.Cmd) {
	p := m.stickerPicker
	if p == nil || msg.gen != p.gen {
		return m, nil
	}
	p.loading = false
	p.err = ""
	if msg.err != nil {
		p.err = "Could not load stickers"
		return m, nil
	}
	p.tabs = []stickerTab{
		{title: "Recent", items: msg.catalog.Recent, loaded: true},
		{title: "Favorites", items: msg.catalog.Favorites, loaded: true},
	}
	for _, pack := range msg.catalog.Packs {
		title := pack.Title
		if title == "" {
			title = pack.ShortName
		}
		if title == "" {
			title = "Pack"
		}
		p.tabs = append(p.tabs, stickerTab{title: title, pack: pack})
	}
	p.tab, p.cursor = 0, 0
	return m, m.fetchSelectedStickerPreviewCmd()
}

func (m RootModel) handleStickerPackLoaded(msg stickerPackLoadedMsg) (RootModel, tea.Cmd) {
	p := m.stickerPicker
	if p == nil || msg.gen != p.gen {
		return m, nil
	}
	for i := range p.tabs {
		tab := &p.tabs[i]
		if tab.pack.ID != msg.packID {
			continue
		}
		tab.loading = false
		tab.loaded = msg.err == nil
		tab.err = ""
		if msg.err != nil {
			tab.err = "Could not load sticker pack"
			tab.items = nil
		} else {
			tab.items = msg.items
		}
		if i == p.tab {
			p.cursor = 0
			return m, m.fetchSelectedStickerPreviewCmd()
		}
		break
	}
	return m, nil
}

func (m RootModel) switchStickerTab(delta int) (RootModel, tea.Cmd) {
	p := m.stickerPicker
	if p == nil || len(p.tabs) == 0 {
		return m, nil
	}
	p.tab = (p.tab + delta + len(p.tabs)) % len(p.tabs)
	p.cursor = 0
	tab := p.activeTab()
	if tab.loaded {
		return m, m.fetchSelectedStickerPreviewCmd()
	}
	if tab.loading {
		return m, nil
	}
	owner, ok := m.owner.(stickerOwner)
	if !ok {
		return m, nil
	}
	tab.loading = true
	tab.err = ""
	return m, loadStickerPackCmd(m.ctx, owner, p.gen, tab.pack)
}

func (m RootModel) handleStickerPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.stickerPicker
	switch keys.NormalizeKey(msg.String()) {
	case "esc":
		m.stickerPicker = nil
		return m, nil
	case "enter":
		return m.sendSelectedSticker()
	case "up", "ctrl+k", "k":
		if p.cursor > 0 {
			p.cursor--
			return m, m.fetchSelectedStickerPreviewCmd()
		}
	case "down", "ctrl+j", "j":
		if tab := p.activeTab(); tab != nil && p.cursor+1 < len(tab.items) {
			p.cursor++
			return m, m.fetchSelectedStickerPreviewCmd()
		}
	case "left", "h":
		return m.switchStickerTab(-1)
	case "right", "l":
		return m.switchStickerTab(1)
	}
	return m, nil
}

func (m RootModel) fetchSelectedStickerPreviewCmd() tea.Cmd {
	item, ok := m.stickerPicker.selected()
	if !ok {
		return nil
	}
	if _, ok := domain.StickerPreviewSlot(&domain.MediaRef{Kind: domain.MediaSticker}, &item.Document); !ok {
		return nil
	}
	if _, ok := m.imageCache.Get(item.Document.ID); ok {
		return nil
	}
	owner, ok := m.owner.(stickerOwner)
	if !ok {
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg {
		path, err := owner.FetchStickerPreview(ctx, item)
		if err != nil {
			return nil
		}
		img, err := decodeImageFile(path)
		if err != nil {
			return nil
		}
		return PhotoReadyMsg{PhotoID: item.Document.ID, Image: img}
	}
}

func (m RootModel) sendSelectedSticker() (RootModel, tea.Cmd) {
	item, ok := m.stickerPicker.selected()
	owner, ownerOK := m.owner.(stickerOwner)
	if !ok || !ownerOK {
		return m, nil
	}
	chatID := m.currentChatID
	peer, replyID, threadRootID := m.sendTarget(m.chat.PendingReplyID())
	m.stickerPicker = nil
	m.chat.ClearPendingAction()
	ctx := m.ctx
	return m, func() tea.Msg {
		return stickerSendDoneMsg{err: owner.SendSticker(ctx, chatID, peer, item, replyID, threadRootID)}
	}
}

func (m RootModel) handleStickerSendDone(msg stickerSendDoneMsg) (RootModel, tea.Cmd) {
	if msg.err != nil {
		return m, func() tea.Msg { return errStatus("send sticker", msg.err) }
	}
	return m, nil
}

func stickerPreviewBox(imgW, imgH int) (int, int) {
	rows := media.PhotoRows(imgW, imgH, stickerPreviewCols, media.CellAspect())
	if rows > stickerPreviewRows {
		rows = stickerPreviewRows
	}
	if rows < 1 {
		rows = 1
	}
	return stickerPreviewCols, rows
}

func (m RootModel) stickerPreviewLines(item domain.StickerRef) []string {
	img, ok := m.imageCache.Get(item.Document.ID)
	if !ok {
		return nil
	}
	cols, rows := stickerPreviewBox(img.Bounds().Dx(), img.Bounds().Dy())
	if m.imageMode == media.ModeKitty {
		if !m.kittyStore.Ready(item.Document.ID, cols) {
			return nil
		}
		return media.PlaceholderLines(m.kittyStore.IDFor(item.Document.ID), cols, rows)
	}
	lines := media.RenderBlockArt(img, cols)
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return lines
}

func stickerTabsLine(p *stickerPicker, width int) string {
	if p == nil || len(p.tabs) == 0 {
		return ""
	}
	labels := make([]string, len(p.tabs))
	for i, tab := range p.tabs {
		title := runewidth.Truncate(tab.title, 12, "…")
		if i == p.tab {
			labels[i] = "[" + title + "]"
		} else {
			labels[i] = title
		}
	}
	budget := max(1, width-4)
	start, end := p.tab, p.tab+1
	used := runewidth.StringWidth(labels[p.tab])
	for end < len(labels) && used+1+runewidth.StringWidth(labels[end]) <= budget {
		used += 1 + runewidth.StringWidth(labels[end])
		end++
	}
	for start > 0 && used+1+runewidth.StringWidth(labels[start-1]) <= budget {
		start--
		used += 1 + runewidth.StringWidth(labels[start])
	}
	line := strings.Join(labels[start:end], " ")
	if start > 0 {
		line = "< " + line
	}
	if end < len(labels) {
		line += " >"
	}
	return runewidth.Truncate(line, width, "…")
}

func (m RootModel) stickerPickerView(base string) string {
	p := m.stickerPicker
	innerW := stickerPickerWidth
	if m.width-4 < innerW {
		innerW = max(20, m.width-4)
	}
	rows := []string{stickerTabsLine(p, innerW)}
	item, selected := p.selected()
	var preview []string
	if selected {
		preview = m.stickerPreviewLines(item)
	}
	tab := p.activeTab()
	loading, loadErr := p.loading, p.err
	if tab != nil {
		loading = loading || tab.loading
		if tab.err != "" {
			loadErr = tab.err
		}
	}
	for i := 0; i < stickerPreviewRows; i++ {
		line := ""
		if i < len(preview) {
			line = preview[i]
		} else if i == stickerPreviewRows/2 && loading {
			line = "loading..."
		} else if i == stickerPreviewRows/2 && loadErr != "" {
			line = loadErr
		} else if i == stickerPreviewRows/2 && !selected {
			line = "No stickers"
		}
		gap := max(0, innerW-lipgloss.Width(line))
		rows = append(rows, theme.Pad(gap/2)+line+theme.Pad(gap-gap/2))
	}
	var items []domain.StickerRef
	if tab != nil {
		items = tab.items
	}
	start := p.cursor - stickerListRows/2
	if start < 0 {
		start = 0
	}
	if start+stickerListRows > len(items) {
		start = max(0, len(items)-stickerListRows)
	}
	for i := 0; i < stickerListRows; i++ {
		idx := start + i
		line := ""
		if idx < len(items) {
			it := items[idx]
			mark := "  "
			if idx == p.cursor {
				mark = "> "
			}
			emoji := it.Emoji
			if emoji == "" {
				emoji = "[sticker]"
			}
			line = mark + emoji + "  " + stickerFormat(it.Document.MimeType)
		}
		rows = append(rows, runewidth.Truncate(line, innerW, "…"))
	}
	rows = append(rows, "left/right tab | up/down select | enter send | esc close")
	content := strings.Join(rows, "\n")
	boxH := len(rows) + 2
	boxW := innerW + 2
	box := components.RenderBox(content, "Stickers", "", "", "", lipgloss.RoundedBorder(), nil, boxW, boxH)
	top := max(0, (m.height-boxH)/2)
	left := max(0, (m.width-boxW)/2)
	return stampBoxOverlay(base, strings.Split(box, "\n"), top, left, boxW, m.height)
}

func stickerFormat(mime string) string {
	switch mime {
	case "image/webp":
		return "WEBP"
	case "application/x-tgsticker":
		return "TGS"
	case "video/webm":
		return "WEBM"
	default:
		return mime
	}
}
