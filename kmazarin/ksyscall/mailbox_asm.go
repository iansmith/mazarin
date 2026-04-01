package ksyscall

import (
	_ "unsafe" // for go:linkname
)

// Forward declarations for mailbox functions in the main package.

//go:linkname BlockForMailboxRecv main.BlockForMailboxRecv
func BlockForMailboxRecv(shepherdIdx int, bufPtr uint64) uintptr

//go:linkname mailboxSendKernel main.mailboxSendKernel
func mailboxSendKernel(senderSID, targetSID int16, code int64, senderVA uintptr) int64

//go:linkname mailboxSendKernelWithSwitch main.mailboxSendKernelWithSwitch
func mailboxSendKernelWithSwitch(senderSID, targetSID int16, code int64, senderVA uintptr) (int64, uintptr)

//go:linkname drainMailboxQueue main.drainMailboxQueue
func drainMailboxQueue(shepherdIdx int) (mailboxNotification, bool)

//go:linkname addVACacheEntry main.AddVACacheEntry
func addVACacheEntry(ownerSID, targetSID int16, ownerVA, targetVA uintptr) bool

//go:linkname lookupVACache main.LookupVACache
func lookupVACache(ownerSID, targetSID int16, ownerPageVA uintptr) (uintptr, bool)

// mailboxNotification mirrors main.MailboxNotification for the linkname bridge.
type mailboxNotification struct {
	Code      int64
	SenderSID int16
	_         int16
	_         int32
	RingAddr  uintptr
}
