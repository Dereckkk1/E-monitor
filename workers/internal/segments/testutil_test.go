package segments

import "os"

// touch creates an empty file at path. Used by tests that only need the file
// to exist (filename is what's parsed for time information).
func touch(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return f.Close()
}
