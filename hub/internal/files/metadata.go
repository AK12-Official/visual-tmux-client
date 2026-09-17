package files

import (
	"os"
	"syscall"
)

// otherNames is how many names the entry has besides the one it was reached by.
//
// A file reached by one name has no others, which is the answer for anything
// this cannot read: the link count is a Unix fact, and this is a Unix-only hub
// (the two platforms it runs on differ in the field's width and in nothing
// else), so a FileInfo that does not carry one is a filesystem this code has no
// opinion about -- and answering "none" is what the write path did before it
// asked at all.
func otherNames(info os.FileInfo) int {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink <= 1 {
		return 0
	}
	return int(stat.Nlink) - 1
}

// takeOwner gives a staged file the owner of the entry it will replace.
//
// A replacement is a file the hub created, so it belongs to the hub's user
// unless this succeeds. It succeeds when the owner is already the hub's -- the
// ordinary case, where there is nothing to do -- or when the hub may change a
// file's owner at all, which normally means it is running as root. A hub that
// may not, editing a file in a group-writable directory that belongs to another
// account, leaves the replacement owned by itself, and no writer that replaces
// the inode can do better: only writing through the target's own inode keeps its
// owner, and that is the atomicity this path exists to provide.
//
// A refusal is therefore not an error. The write goes on with the ownership the
// hub's own permissions imply, which is the same file it would have created by
// any other means.
func takeOwner(file *os.File, origin os.FileInfo) error {
	if origin == nil {
		// A name that was free when the write began: there is no owner to keep,
		// and the file belongs to whoever wrote it.
		return nil
	}
	wanted, ok := origin.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	mine, err := file.Stat()
	if err != nil {
		return err
	}
	current, ok := mine.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if current.Uid == wanted.Uid && current.Gid == wanted.Gid {
		return nil
	}
	if err := file.Chown(int(wanted.Uid), int(wanted.Gid)); err != nil {
		// Not permitted, which is the expected answer for everything the hub does
		// not already own. See the doc above.
		return nil //nolint:nilerr // a refusal is the documented outcome, not a failure
	}
	return nil
}
