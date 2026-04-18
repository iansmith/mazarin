//go:build amd64

package main

import (
	"encoding/binary"

	"mazzy/shared/fs/ext2"
)

// writeStatBuf writes a 128-byte Linux struct stat (x86_64 layout) from an ext2 inode.
//
// x86_64 struct stat layout differs from ARM64/RISC-V:
//
//	 0: st_dev     (uint64)
//	 8: st_ino     (uint64)
//	16: st_nlink   (uint64)  ← 8 bytes, before st_mode
//	24: st_mode    (uint32)
//	28: st_uid     (uint32)
//	32: st_gid     (uint32)
//	36: __pad0     (int32)
//	40: st_rdev    (uint64)
//	48: st_size    (int64)
//	56: st_blksize (int64)   ← 8 bytes, not 4
//	64: st_blocks  (int64)
//	72: st_atime   (int64)
//	80: st_atime_nsec (int64)
//	88: st_mtime   (int64)
//	96: st_mtime_nsec (int64)
//	104: st_ctime  (int64)
//	112: st_ctime_nsec (int64)
func writeStatBuf(buf []byte, inode *ext2.Inode, inum uint32) {
	for i := range buf[:128] {
		buf[i] = 0
	}
	le := binary.LittleEndian
	le.PutUint64(buf[0:], 0)                        // st_dev
	le.PutUint64(buf[8:], uint64(inum))              // st_ino
	le.PutUint64(buf[16:], uint64(inode.LinksCount)) // st_nlink (uint64 on x86_64!)
	le.PutUint32(buf[24:], uint32(inode.Mode))       // st_mode
	le.PutUint32(buf[28:], uint32(inode.UID))        // st_uid
	le.PutUint32(buf[32:], uint32(inode.GID))        // st_gid
	le.PutUint32(buf[36:], 0)                        // __pad0
	le.PutUint64(buf[40:], 0)                        // st_rdev
	le.PutUint64(buf[48:], uint64(inode.Size))       // st_size
	le.PutUint64(buf[56:], 4096)                     // st_blksize (int64 on x86_64!)
	le.PutUint64(buf[64:], uint64(inode.Blocks))     // st_blocks
	le.PutUint64(buf[72:], uint64(inode.ATime))      // st_atime
	le.PutUint64(buf[80:], 0)                        // st_atime_nsec
	le.PutUint64(buf[88:], uint64(inode.MTime))      // st_mtime
	le.PutUint64(buf[96:], 0)                        // st_mtime_nsec
	le.PutUint64(buf[104:], uint64(inode.CTime))     // st_ctime
	le.PutUint64(buf[112:], 0)                       // st_ctime_nsec
}
