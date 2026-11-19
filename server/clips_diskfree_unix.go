//go:build !windows

package main

import "golang.org/x/sys/unix"

// freeBytes is how much room the volume holding path has left for this
// process, and how big it is. Bavail rather than Bfree: the blocks a
// filesystem keeps back for root are no use to a plugin that is not root, and
// counting them would let clips fill a disk Mattermost itself then cannot
// write to. ok is false when the volume could not be asked, which callers
// treat as no answer rather than as an empty disk.
func freeBytes(path string) (free, total uint64, ok bool) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}

	blockSize := uint64(stat.Bsize) //nolint:gosec // a block size is never negative

	return stat.Bavail * blockSize, stat.Blocks * blockSize, true
}
