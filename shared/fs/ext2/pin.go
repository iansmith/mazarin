package ext2

// PinInode bumps the open-handle refcount for inum. While pinned, the
// inum's bitmap bit and on-disk inode are kept live even if a Remove /
// WriteFile-overwrite / Rename-overwrite would otherwise free them.
func (fs *FileSystem) PinInode(inum uint32) {
	if !fs.writable || inum == 0 {
		return
	}
	fs.pinMu.Lock()
	fs.inodeRefs[inum]++
	fs.pinMu.Unlock()
}

// UnpinInode drops one open-handle ref. If the count hits zero AND the
// inum was marked for deferred free while pinned, the bitmap + on-disk
// inode + data blocks are reclaimed now.
func (fs *FileSystem) UnpinInode(inum uint32) error {
	if !fs.writable || inum == 0 {
		return nil
	}
	fs.pinMu.Lock()
	n := fs.inodeRefs[inum] - 1
	if n > 0 {
		fs.inodeRefs[inum] = n
		fs.pinMu.Unlock()
		return nil
	}
	delete(fs.inodeRefs, inum)
	deferred := fs.pendingFreeSet[inum]
	if deferred {
		delete(fs.pendingFreeSet, inum)
	}
	fs.pinMu.Unlock()

	if !deferred {
		return nil
	}
	return fs.reclaimInode(inum)
}

// isInodePinned reports whether any open handle currently holds inum.
// Caller must hold pinMu.
func (fs *FileSystem) isInodePinnedLocked(inum uint32) bool {
	return fs.inodeRefs[inum] > 0
}

// markPendingFreeLocked records that inum should be reclaimed when its
// last pin drops. Caller must hold pinMu and have just verified the
// inode is pinned (otherwise the caller should free immediately).
func (fs *FileSystem) markPendingFreeLocked(inum uint32) {
	fs.pendingFreeSet[inum] = true
}

// reclaimInode performs the actual on-disk free: data blocks, then zero
// the inode, then clear the bitmap bit. Mirrors what Remove /
// WriteFile-overwrite / Rename-overwrite did inline before deferred-free.
func (fs *FileSystem) reclaimInode(inum uint32) error {
	inode, err := fs.ReadInode(inum)
	if err != nil {
		return err
	}
	if err := fs.freeInodeBlocks(inode); err != nil {
		return err
	}
	var zeroInode Inode
	if err := fs.WriteInode(inum, &zeroInode); err != nil {
		return err
	}
	return fs.freeInode(inum)
}
