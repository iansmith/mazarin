//go:build arm64 || amd64 || riscv64

package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"unsafe"
)

// ============================================================================
// Mailbox System — Kernel-Mediated Wake Notifications with VA Translation
// ============================================================================
//
// Shepherds share memory pages for ring-buffer IPC. The mailbox syscall
// wakes the target shepherd and delivers a translated VA so it can read
// the shared ring buffer.
//
// VA Translation Cache:
//   Maps (ownerSID, ownerPageVA) → (targetSID, targetPageVA).
//   Populated when a shepherd maps a page into another shepherd's space
//   via SysMailboxMapPage. Looked up when SysMailboxSend translates the
//   sender's VA to the receiver's VA.

// maxVACacheEntries is the maximum number of cached VA translations.
const maxVACacheEntries = 128

type vaCacheEntry struct {
	inUse     bool
	ownerSID  int16
	targetSID int16
	ownerVA   uintptr // page-aligned VA in owner's space
	targetVA  uintptr // page-aligned VA in target's space
}

var vaCache [maxVACacheEntries]vaCacheEntry

// addVACacheEntry adds a new VA translation to the cache.
// Returns false if cache is full.
func addVACacheEntry(ownerSID, targetSID int16, ownerVA, targetVA uintptr) bool {
	for i := range vaCache {
		if !vaCache[i].inUse {
			vaCache[i] = vaCacheEntry{
				inUse:     true,
				ownerSID:  ownerSID,
				targetSID: targetSID,
				ownerVA:   ownerVA,
				targetVA:  targetVA,
			}
			return true
		}
	}
	return false
}

// lookupVACache finds the target VA for a given owner's VA.
// Returns (targetVA, true) on hit, (0, false) on miss.
//
//go:nosplit
func lookupVACache(ownerSID, targetSID int16, ownerPageVA uintptr) (uintptr, bool) {
	for i := range vaCache {
		if vaCache[i].inUse &&
			vaCache[i].ownerSID == ownerSID &&
			vaCache[i].targetSID == targetSID &&
			vaCache[i].ownerVA == ownerPageVA {
			return vaCache[i].targetVA, true
		}
	}
	return 0, false
}

// cleanupVACacheForShepherd removes all cache entries involving a shepherd.
func cleanupVACacheForShepherd(sid int16) {
	for i := range vaCache {
		if vaCache[i].inUse && (vaCache[i].ownerSID == sid || vaCache[i].targetSID == sid) {
			vaCache[i].inUse = false
		}
	}
}

// ============================================================================
// Mailbox Notification Queue — Per-Shepherd Pending Notifications
// ============================================================================
//
// When SysMailboxSend is called, the notification is queued for the target
// shepherd. If a thread is blocked in SysMailboxRecv, it is woken.

const maxMailboxQueueSize = 16

// MailboxNotification is a pending notification delivered to a shepherd.
type MailboxNotification struct {
	Code     int64   // WMNotify or ShepherdNotify
	SenderSID int16  // Who sent this
	_        int16   // padding
	_        int32   // padding
	RingAddr uintptr // Translated VA of the ring buffer in receiver's space
}

type mailboxQueue struct {
	items [maxMailboxQueueSize]MailboxNotification
	head  uint32
	tail  uint32
}

func (q *mailboxQueue) push(n MailboxNotification) bool {
	if q.tail-q.head >= maxMailboxQueueSize {
		return false
	}
	q.items[q.tail%maxMailboxQueueSize] = n
	q.tail++
	return true
}

func (q *mailboxQueue) pop() (MailboxNotification, bool) {
	if q.head == q.tail {
		return MailboxNotification{}, false
	}
	n := q.items[q.head%maxMailboxQueueSize]
	q.head++
	return n, true
}

func (q *mailboxQueue) empty() bool {
	return q.head == q.tail
}

// Per-shepherd mailbox queues and blocked thread tracking.
var mailboxQueues [proc.MaxShepherds]mailboxQueue
var mailboxBlockedTID [proc.MaxShepherds]ThreadId
var mailboxBlockedPtr [proc.MaxShepherds]uintptr

func init() {
	for i := range mailboxBlockedTID {
		mailboxBlockedTID[i] = -1
	}
}

// ============================================================================
// Kernel Functions (called from ksyscall via linkname)
// ============================================================================

// AddVACacheEntry adds a VA translation cache entry. Called from ksyscall.
func AddVACacheEntry(ownerSID, targetSID int16, ownerVA, targetVA uintptr) bool {
	return addVACacheEntry(ownerSID, targetSID, ownerVA, targetVA)
}

// LookupVACache looks up a cached VA translation. Called from ksyscall.
//
//go:nosplit
func LookupVACache(ownerSID, targetSID int16, ownerPageVA uintptr) (uintptr, bool) {
	return lookupVACache(ownerSID, targetSID, ownerPageVA)
}

// mailboxSendKernel queues a notification for the target shepherd and wakes it.
// senderVA is the ring buffer VA in the sender's address space.
// The kernel translates it to the target's VA using the cache.
func mailboxSendKernel(senderSID, targetSID int16, code int64, senderVA uintptr) int64 {
	result, _ := mailboxSendKernelWithSwitch(senderSID, targetSID, code, senderVA)
	return result
}

// mailboxSendKernelWithSwitch is like mailboxSendKernel but also returns
// the woken thread's context pointer so the SVC handler can immediately
// context-switch to it (matching Linux's wake_up_process behavior).
// Returns (result, ctxPtr) where ctxPtr is 0 if no thread was woken.
func mailboxSendKernelWithSwitch(senderSID, targetSID int16, code int64, senderVA uintptr) (int64, uintptr) {
	if targetSID < 0 || int(targetSID) >= proc.MaxShepherds {
		return -22, 0 // EINVAL
	}

	// Translate sender's VA to target's VA
	pageOffset := senderVA & (kmem.PageSize - 1)
	senderPageVA := senderVA &^ (kmem.PageSize - 1)

	targetPageVA, ok := lookupVACache(senderSID, targetSID, senderPageVA)
	if !ok {
		serial.RawUARTPuts("[Mailbox] send: no VA cache entry for ")
		serial.RawUARTHexCompact(uint64(senderPageVA))
		serial.RawUARTPuts("\r\n")
		return -14, 0 // EFAULT — page not mapped into target
	}
	targetRingVA := targetPageVA + pageOffset

	// Queue notification
	notif := MailboxNotification{
		Code:      code,
		SenderSID: senderSID,
		RingAddr:  targetRingVA,
	}

	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	if !mailboxQueues[targetSID].push(notif) {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		serial.RawUARTPuts("[Mailbox] queue full for target ")
		serial.RawUARTDecimal(uint64(targetSID))
		serial.RawUARTPuts("\r\n")
		return -12, 0 // ENOMEM — queue full
	}

	// Wake if blocked — priority enqueue so mailbox-woken threads run quickly
	var wokenCtx uintptr
	serial.RawUARTPuts("[MBS:")
	serial.RawUARTDecimal(uint64(senderSID))
	serial.RawUARTPuts("->")
	serial.RawUARTDecimal(uint64(targetSID))
	serial.RawUARTPuts(" bt=")
	serial.RawUARTDecimal(uint64(int64(mailboxBlockedTID[targetSID])))
	serial.RawUARTPuts("]\r\n")
	if mailboxBlockedTID[targetSID] >= 0 {
		t := (*Thread)(unsafe.Pointer(mailboxBlockedPtr[targetSID]))
		if t != nil && t.State == ThreadBlockedMailbox {
			t.State = ThreadReady
			t.MailboxWoken = true
			mailboxBlockedTID[targetSID] = -1
			mailboxBlockedPtr[targetSID] = 0
			t.Context.RewindToSyscall()
			t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
			t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
			enqueueReadyPrioritySchedLockHeld(t)
			wokenCtx = uintptr(unsafe.Pointer(&t.Context))
			asm.Dsb()
		}
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)

	return 0, wokenCtx
}

// BlockForMailboxRecv blocks the current thread waiting for mailbox notifications.
// bufPtr is saved for SVC rewind (arg0 of SysMailboxRecv).
// Returns the context pointer of the next thread, or 0.
//
//go:nosplit
//go:noinline
func BlockForMailboxRecv(shepherdIdx int, bufPtr uint64) uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	next := findNextThreadForBlockSchedLockHeld(t)
	if next == nil {
		serial.RawUARTPuts("[BFMR:noNext]\r\n")
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Clear previous blocked thread (M migration)
	if shepherdIdx >= 0 && shepherdIdx < proc.MaxShepherds {
		prev := (*Thread)(unsafe.Pointer(mailboxBlockedPtr[shepherdIdx]))
		if prev != nil && prev.State == ThreadBlockedMailbox {
			prev.State = ThreadReady
			enqueueReadySchedLockHeld(prev)
		}
	}

	t.State = ThreadBlockedMailbox
	t.SoftIRQSlotArg = bufPtr    // arg0 for rewind (bufPtr for SysMailboxRecv)
	t.SoftIRQSyscallNum = 0x1031 // SysMailboxRecv
	if shepherdIdx >= 0 && shepherdIdx < proc.MaxShepherds {
		mailboxBlockedTID[shepherdIdx] = t.TID
		mailboxBlockedPtr[shepherdIdx] = uintptr(unsafe.Pointer(t))
	}

	serial.RawUARTPuts("[BFMR:ok t=")
	serial.RawUARTDecimal(uint64(t.TID))
	serial.RawUARTPuts(" n=")
	serial.RawUARTDecimal(uint64(next.TID))
	serial.RawUARTPuts(" np=")
	serial.RawUARTDecimal(uint64(next.PID))
	serial.RawUARTPuts("]\r\n")

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// drainMailboxQueue pops one notification from the shepherd's mailbox queue.
// Returns (notification, true) or (zero, false) if empty.
func drainMailboxQueue(shepherdIdx int) (MailboxNotification, bool) {
	if shepherdIdx < 0 || shepherdIdx >= proc.MaxShepherds {
		return MailboxNotification{}, false
	}
	return mailboxQueues[shepherdIdx].pop()
}

// CleanupMailboxForShepherd clears mailbox state for a dying shepherd.
func CleanupMailboxForShepherd(shepherdID int16) {
	sid := int(shepherdID)
	if sid < 0 || sid >= proc.MaxShepherds {
		return
	}

	// Clean up VA cache entries
	cleanupVACacheForShepherd(shepherdID)

	// Wake any thread blocked on this shepherd's mailbox
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	if mailboxBlockedTID[sid] >= 0 {
		t := (*Thread)(unsafe.Pointer(mailboxBlockedPtr[sid]))
		if t != nil && t.State == ThreadBlockedMailbox {
			t.State = ThreadReady
			enqueueReadySchedLockHeld(t)
		}
		mailboxBlockedTID[sid] = -1
		mailboxBlockedPtr[sid] = 0
	}

	// Clear queue
	mailboxQueues[sid].head = 0
	mailboxQueues[sid].tail = 0

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)

	serial.RawUARTPuts("[Mailbox] cleaned for shepherd ")
	serial.RawUARTDecimal(uint64(shepherdID))
	serial.RawUARTPuts("\r\n")
}
