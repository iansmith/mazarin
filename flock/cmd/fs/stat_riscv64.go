//go:build riscv64

package main

import (
	"encoding/binary"

	"mazzy/shared/fs/ext2"
)

// writeStatBuf writes a 128-byte Linux struct stat (RISC-V layout) from an ext2 inode.
// RISC-V uses the same layout as ARM64.
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
