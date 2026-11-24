package panel

import "os"

func permissionMode(value uint64) os.FileMode {
	mode := os.FileMode(value & 0777)
	if value&04000 != 0 {
		mode |= os.ModeSetuid
	}
	if value&02000 != 0 {
		mode |= os.ModeSetgid
	}
	if value&01000 != 0 {
		mode |= os.ModeSticky
	}
	return mode
}
func unixPermission(mode os.FileMode) uint32 {
	value := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		value |= 04000
	}
	if mode&os.ModeSetgid != 0 {
		value |= 02000
	}
	if mode&os.ModeSticky != 0 {
		value |= 01000
	}
	return value
}
