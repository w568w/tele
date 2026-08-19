//go:build linux || freebsd || openbsd || netbsd || dragonfly

package ui

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	fileURIListMIME = "text/uri-list"
	gnomeFilesMIME  = "x-special/gnome-copied-files"
)

func wlPasteFileExtractCmd(target string) *exec.Cmd {
	return exec.Command("wl-paste", "--no-newline", "--type", target)
}

func xclipFileExtractCmd(target string) *exec.Cmd {
	return exec.Command("xclip", "-selection", "clipboard", "-t", target, "-o")
}

func clipboardFileTarget(types string) string {
	fields := strings.Fields(types)
	for _, target := range []string{fileURIListMIME, gnomeFilesMIME} {
		for _, field := range fields {
			if field == target {
				return target
			}
		}
	}
	return ""
}

func readOSClipboardFiles() ([]string, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return readClipboardFiles(wlPasteListCmd(), wlPasteFileExtractCmd)
	}
	if os.Getenv("DISPLAY") != "" {
		return readClipboardFiles(xclipTargetsCmd(), xclipFileExtractCmd)
	}
	return nil, nil
}

func readClipboardFiles(list *exec.Cmd, extract func(string) *exec.Cmd) ([]string, error) {
	types, err := list.Output()
	if err != nil {
		return nil, nil
	}
	target := clipboardFileTarget(string(types))
	if target == "" {
		return nil, nil
	}
	data, err := extract(target).Output()
	if err != nil {
		return nil, fmt.Errorf("clipboard file extract: %w", err)
	}
	return parseClipboardFileList(string(data), target)
}

func parseClipboardFileList(data, target string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	if target == gnomeFilesMIME && len(lines) > 0 {
		action := strings.TrimSpace(lines[0])
		if action == "copy" || action == "cut" {
			lines = lines[1:]
		}
	}

	paths := make([]string, 0, len(lines))
	seen := make(map[string]bool)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.ContainsRune(line, '\x00') {
			return nil, fmt.Errorf("clipboard file URI contains NUL")
		}
		u, err := url.Parse(line)
		if err != nil {
			return nil, fmt.Errorf("clipboard file URI: %w", err)
		}
		if u.Scheme != "file" || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) || u.Path == "" {
			continue
		}
		if strings.ContainsRune(u.Path, '\x00') {
			return nil, fmt.Errorf("clipboard file path contains NUL")
		}
		path := filepath.Clean(u.Path)
		if seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths, nil
}
