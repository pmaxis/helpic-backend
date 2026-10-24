//go:build !unix

package admin

import "errors"

// diskUsage can't read a disk here; the panel leaves the disk out.
func diskUsage(string) (Disk, error) {
	return Disk{}, errors.New("disk usage is not supported on this platform")
}
