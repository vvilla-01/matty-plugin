//go:build windows

package main

import "golang.org/x/sys/windows"

// freeBytes is how much room the volume holding path has left for this
// process, and how big it is: the free space available to the caller, which
// honours disk quotas, rather than the volume's total free space. ok is false
// when the volume could not be asked.
func freeBytes(path string) (free, total uint64, ok bool) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}

	var available, size, allFree uint64
	if err := windows.GetDiskFreeSpaceEx(name, &available, &size, &allFree); err != nil {
		return 0, 0, false
	}

	return available, size, true
}
