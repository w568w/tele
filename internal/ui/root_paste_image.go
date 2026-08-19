package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/ui/components"
)

// clipboardImagePastedMsg reports a file-manager paste, clipboard image, or
// clipboard extraction failure. Plain text continues to use tea.PasteMsg.
type clipboardImagePastedMsg struct {
	paths []string
	err   error
}

// readClipboardForComposerCmd runs off the update loop. It prefers an image,
// then files copied by a file manager, and finally plain text. A single tea.Cmd
// yields one message, so the image-error path defers its text fallback to the
// handler.
func readClipboardForComposerCmd(tmpDir string) tea.Cmd {
	return func() tea.Msg {
		data, ext, imageErr := clipImageReader.ReadImage()
		if len(data) > 0 {
			path, werr := writeTempMediaFile(data, tmpDir, ext)
			return clipboardImagePastedMsg{paths: []string{path}, err: werr}
		}
		paths, err := clipFileReader()
		if err != nil {
			return clipboardImagePastedMsg{err: err}
		}
		if len(paths) > 0 {
			return clipboardImagePastedMsg{paths: paths}
		}
		if imageErr != nil {
			return clipboardImagePastedMsg{err: imageErr}
		}
		str, err := clipboardRead()
		if err != nil || str == "" {
			return nil
		}
		return tea.PasteMsg{Content: str}
	}
}

func (m RootModel) handleClipboardImagePasted(msg clipboardImagePastedMsg) (RootModel, tea.Cmd) {
	if msg.err != nil {
		toast := func() tea.Msg {
			return StatusErrMsg{Text: "clipboard paste failed", Sev: components.SeverityWarning}
		}
		return m, tea.Batch(toast, readClipboardCmd())
	}
	if len(msg.paths) > 0 {
		return m.stageAttachmentsFromPaths(msg.paths)
	}
	return m, nil
}
