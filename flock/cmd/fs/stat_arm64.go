//go:build arm64

package main

import (
	"encoding/binary"

	"mazzy/shared/fs/ext2"
)

// writeStatBuf writes a 128-byte Linux struct stat (ARM64 layout) from an ext2 inode.
//
// ARM64 struct stat layout:
//
//	 0: st_dev     (uint64)
//	 8: st_ino     (uint64)
//	16: st_mode    (uint32)
//	20: st_nlink   (uint32)
//	24: st_uid     (uint32)
//	28: st_gid     (uint32)
//	32: st_rdev    (uint64)
//	40: __pad1     (uint64)
//	48: st_size    (int64)
//	56: st_blksize (int32)
//	60: __pad2     (int32)
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
	le.PutUint32(buf[16:], uint32(inode.Mode))       // st_mode
	le.PutUint32(buf[20:], uint32(inode.LinksCount)) // st_nlink
	le.PutUint32(buf[24:], uint32(inode.UID))        // st_uid
	le.PutUint32(buf[28:], uint32(inode.GID))        // st_gid
	le.PutUint64(buf[32:], 0)                        // st_rdev
	le.PutUint64(buf[40:], 0)                        // __pad1
	le.PutUint64(buf[48:], uint64(inode.Size))       // st_size
	le.PutUint32(buf[56:], 4096)                     // st_blksize
	le.PutUint32(buf[60:], 0)                        // __pad2
	le.PutUint64(buf[64:], uint64(inode.Blocks))     // st_blocks
	le.PutUint64(buf[72:], uint64(inode.ATime))      // st_atime
	le.PutUint64(buf[80:], 0)                        // st_atime_nsec
	le.PutUint64(buf[88:], uint64(inode.MTime))      // st_mtime
	le.PutUint64(buf[96:], 0)                        // st_mtime_nsec
	le.PutUint64(buf[104:], uint64(inode.CTime))     // st_ctime
	le.PutUint64(buf[112:], 0)                       // st_ctime_nsec
}
