package desktop

import "golang.org/x/sys/unix"

func renameWithoutReplacement(from, to string) error {
	return unix.RenamexNp(from, to, unix.RENAME_EXCL)
}
