//go:build unix

package admin

import "syscall"

// diskUsage reads how big the disk holding dir is and how much of it is free
// to use.
func diskUsage(dir string) (Disk, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return Disk{}, err
	}
	bs := uint64(st.Bsize)
	return Disk{TotalBytes: st.Blocks * bs, FreeBytes: st.Bavail * bs}, nil
}
