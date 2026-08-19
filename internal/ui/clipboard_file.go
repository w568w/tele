package ui

var clipFileReader = readOSClipboardFiles

// SetClipboardFileReaderForTest swaps the reader and returns a restore func.
func SetClipboardFileReaderForTest(fn func() ([]string, error)) func() {
	prev := clipFileReader
	clipFileReader = fn
	return func() { clipFileReader = prev }
}
