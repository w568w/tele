//go:build linux || freebsd || openbsd || netbsd || dragonfly

package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClipboardFileTargetPrefersURIList(t *testing.T) {
	assert.Equal(t, fileURIListMIME, clipboardFileTarget("text/plain\nx-special/gnome-copied-files\ntext/uri-list\n"))
	assert.Equal(t, gnomeFilesMIME, clipboardFileTarget("text/plain\nx-special/gnome-copied-files\n"))
	assert.Empty(t, clipboardFileTarget("text/plain\nimage/png\n"))
}

func TestParseClipboardFileList(t *testing.T) {
	paths, err := parseClipboardFileList(
		"# copied files\r\nfile:///tmp/one%20file.txt\r\nfile://localhost/tmp/%E4%BA%8C.txt\r\nfile:///tmp/one%20file.txt\r\nhttps://example.com/not-local\r\nfile://server/share/nope\r\n",
		fileURIListMIME,
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/one file.txt", "/tmp/二.txt"}, paths)
}

func TestParseClipboardGNOMEFileList(t *testing.T) {
	paths, err := parseClipboardFileList("copy\nfile:///tmp/a.txt\nfile:///tmp/b.txt\n", gnomeFilesMIME)
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/a.txt", "/tmp/b.txt"}, paths)
}

func TestParseClipboardFileListRejectsMalformedURI(t *testing.T) {
	_, err := parseClipboardFileList("file:///tmp/bad%zz\n", fileURIListMIME)
	assert.Error(t, err)
}

func TestClipboardFileArgvBuilders(t *testing.T) {
	assert.Equal(t, []string{"wl-paste", "--no-newline", "--type", "text/uri-list"}, wlPasteFileExtractCmd(fileURIListMIME).Args)
	assert.Equal(t, []string{"xclip", "-selection", "clipboard", "-t", "text/uri-list", "-o"}, xclipFileExtractCmd(fileURIListMIME).Args)
}
