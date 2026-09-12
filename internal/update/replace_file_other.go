//go:build !windows

package update

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
