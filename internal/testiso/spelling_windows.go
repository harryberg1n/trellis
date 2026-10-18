package testiso

import "golang.org/x/sys/windows"

// shortSpelling is the 8.3 short path of dir, or "" when it has none.
func shortSpelling(dir string) string {
	from, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return ""
	}
	n, err := windows.GetShortPathName(from, nil, 0)
	if err != nil || n == 0 {
		return ""
	}
	buf := make([]uint16, n)
	n, err = windows.GetShortPathName(from, &buf[0], n)
	if err != nil || n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
