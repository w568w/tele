package ui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/media"
	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/sorokin-vladimir/tele/internal/ui/keys"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

// pendingAttachment is the staged file awaiting send. kind is the MIME-detected
// default; sendAs is the user's choice (Photo/File toggle). The File branch is #129.
type pendingAttachment struct {
	path   string
	mime   string
	kind   domain.MediaKind
	sendAs domain.MediaKind
	name   string
	size   int64
}

func (m RootModel) openFilePicker() (RootModel, tea.Cmd) {
	m.filePicker = screens.NewFilePickerModel(m.lastPickerDir, m.width, m.height, m.keyMap)
	m.statusBar.SetPickerOpen(true)
	return m, nil
}

func (m RootModel) handleFileSelected(msg screens.FileSelectedMsg) (RootModel, tea.Cmd) {
	if m.filePicker != nil {
		m.lastPickerDir = m.filePicker.Dir()
	}
	m.filePicker = nil
	m.statusBar.SetPickerOpen(false)
	return m.stageAttachmentFromPath(msg.Path)
}

// maxStagedAttachments caps the composer queue at four full albums. Telegram
// takes at most 10 parts per album and partitionAlbums splits the queue, so the
// cap only exists to keep an accidental select-everything from staging hundreds
// of files.
const maxStagedAttachments = 40

// stageAttachmentFromPath appends a local file to the pending queue: it MIME-
// detects the kind, refreshes the composer chips, enters insert mode so the
// caption field is active, and focuses the composer. Shared by the file picker
// and the clipboard-image paste (#163). Photo/File is toggleable for image/video.
func (m RootModel) stageAttachmentFromPath(path string) (RootModel, tea.Cmd) {
	if len(m.pendingAttachments) >= maxStagedAttachments {
		return m, func() tea.Msg {
			return StatusErrMsg{
				Text: fmt.Sprintf("too many attachments (max %d)", maxStagedAttachments),
				Sev:  components.SeverityWarning,
			}
		}
	}
	mime, err := media.DetectMIME(path)
	if err != nil {
		return m, func() tea.Msg {
			return StatusErrMsg{Text: "cannot read file", Sev: components.SeverityWarning}
		}
	}
	kind := media.DefaultMediaType(mime)
	name, size := fileNameSize(path)
	m.pendingAttachments = append(m.pendingAttachments, pendingAttachment{
		path:   path,
		mime:   mime,
		kind:   kind,
		sendAs: kind,
		name:   name,
		size:   size,
	})
	m.syncAttachmentChips()
	m.statusBar.SetAttachStaged(true)
	// Enter real insert mode so the caption field is active (the composer focus
	// alone does not flip the root's vim mode, which key routing depends on).
	m.vimState.Mode = keys.ModeInsert
	m.statusBar.SetMode(keys.ModeInsert)
	// Attaching drops the limit from a message's 4096 to a caption's 1024, which
	// can leave an existing draft over the limit (#126).
	focusCmd := m.chat.FocusComposer()
	if m.chat.ComposerOverLimit() {
		var toastCmd tea.Cmd
		m, toastCmd = m.handleComposerLimit(components.ComposerLimitMsg{
			Kind: components.ComposerLimitOver, Limit: maxCaptionChars, Caption: true,
		})
		return m, tea.Batch(focusCmd, toastCmd)
	}
	return m, focusCmd
}

func (m RootModel) stageAttachmentsFromPaths(paths []string) (RootModel, tea.Cmd) {
	cmds := make([]tea.Cmd, 0, len(paths))
	for _, path := range paths {
		full := len(m.pendingAttachments) >= maxStagedAttachments
		var cmd tea.Cmd
		m, cmd = m.stageAttachmentFromPath(path)
		cmds = append(cmds, cmd)
		if full {
			break
		}
	}
	return m, tea.Batch(cmds...)
}

// syncAttachmentChips pushes the queue to the composer. The album-wide "Send as"
// toggle is offered only when every staged part is a photo or a video: Telegram
// cannot mix visual media with documents in one album, so a mixed set has no
// meaningful single choice.
func (m *RootModel) syncAttachmentChips() {
	if len(m.pendingAttachments) == 0 {
		m.chat.ClearAttachment()
		return
	}
	items := make([]components.AttachmentChip, 0, len(m.pendingAttachments))
	toggleable := true
	for _, a := range m.pendingAttachments {
		if a.kind != domain.MediaPhoto && a.kind != domain.MediaVideo {
			toggleable = false
		}
		items = append(items, components.AttachmentChip{
			Name: a.name, Size: a.size, Kind: a.kind, SendAs: a.sendAs,
		})
	}
	m.chat.SetAttachments(items, toggleable)
}

// PendingAttachmentCount reports how many files are staged (test accessor).
func (m RootModel) PendingAttachmentCount() int { return len(m.pendingAttachments) }

// PendingAttachmentSendAs reports the first staged part's "send as" kind (test
// accessor).
func (m RootModel) PendingAttachmentSendAs() (domain.MediaKind, bool) {
	if len(m.pendingAttachments) == 0 {
		return 0, false
	}
	return m.pendingAttachments[0].sendAs, true
}

// PendingAttachmentSendAsAll reports every staged part's "send as" kind (test
// accessor).
func (m RootModel) PendingAttachmentSendAsAll() []domain.MediaKind {
	out := make([]domain.MediaKind, 0, len(m.pendingAttachments))
	for _, a := range m.pendingAttachments {
		out = append(out, a.sendAs)
	}
	return out
}

// toggleSendAs flips the whole staged queue between the native kinds and File.
// The toggle is album-wide because a Telegram album cannot mix the two.
func (m RootModel) toggleSendAs() (RootModel, tea.Cmd) {
	if len(m.pendingAttachments) == 0 {
		return m, nil
	}
	for _, a := range m.pendingAttachments {
		if a.kind != domain.MediaPhoto && a.kind != domain.MediaVideo {
			return m, nil
		}
	}
	toFile := m.pendingAttachments[0].sendAs != domain.MediaFile
	next := make([]pendingAttachment, len(m.pendingAttachments))
	copy(next, m.pendingAttachments)
	for i := range next {
		if toFile {
			next[i].sendAs = domain.MediaFile
		} else {
			next[i].sendAs = next[i].kind
		}
	}
	m.pendingAttachments = next
	m.syncAttachmentChips()
	return m, nil
}

// popPendingAttachment removes the most recently staged file. Removing an
// arbitrary item needs a cursor over the list, which is #187.
func (m *RootModel) popPendingAttachment() {
	if len(m.pendingAttachments) == 0 {
		return
	}
	m.pendingAttachments = m.pendingAttachments[:len(m.pendingAttachments)-1]
	m.syncAttachmentChips()
	if len(m.pendingAttachments) == 0 {
		m.statusBar.SetAttachStaged(false)
	}
}

func (m *RootModel) clearPendingAttachments() {
	m.pendingAttachments = nil
	m.chat.ClearAttachment()
	m.statusBar.SetAttachStaged(false)
}

// handleCancelUpload removes one composer extra at a time, in priority order:
// the last staged attachment first, then an active reply/edit, then the queued
// send under the cursor.
//
// The last of those is a discard like any other: an unfinished send is a row in
// the durable queue, not something this client is driving, so cancelling it is
// telling the owner to drop it — which also stops an upload in flight (#195).
func (m RootModel) handleCancelUpload() (RootModel, tea.Cmd) {
	if len(m.pendingAttachments) > 0 {
		m.popPendingAttachment()
		return m, nil
	}
	if m.chat != nil && (m.chat.ReplyToMsgID() != 0 || m.chat.EditMsgID() != 0) {
		m.chat.ClearPendingAction()
		return m, nil
	}
	if m.chat == nil || m.owner == nil {
		return m, nil
	}
	ref := m.chat.SelectedOutboxRef()
	if ref == "" {
		return m, nil // not a queued send
	}
	owner := m.owner
	return m, func() tea.Msg {
		if err := owner.DiscardOutbox(ref); err != nil {
			return errStatus("discard", err)
		}
		return nil
	}
}

func fileNameSize(path string) (string, int64) {
	name := filepath.Base(path)
	var size int64
	if fi, err := os.Stat(path); err == nil {
		size = fi.Size()
	}
	return name, size
}
