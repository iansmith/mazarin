// fontsvc is a .maz module loaded by rachel that provides centralized font
// loading and glyph rendering. It owns rachel's MailboxRecv loop: font
// messages are handled directly; all other notifications are forwarded to
// rachel via a Go channel injected through MazarinPriest.
//
// From the kernel's perspective, fontsvc IS rachel (same PID/SID).
package main

import (
	"bytes"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/sys"
	"mazarin/textshape"
	"mazzy/shared/wm"
	"unsafe"

	goFont "github.com/go-text/typesetting/font"
)

// MazEntryPoint holds a reference to MazarinMain to prevent DCE.
var MazEntryPoint func() = MazarinMain

// MazarinShepherdAddr holds a reference to MazarinShepherd to prevent DCE.
var MazarinShepherdAddr func(interface{}) error = MazarinShepherd

func init() {
	if MazEntryPoint == nil {
		panic("unreachable")
	}
	if MazarinShepherdAddr == nil {
		panic("unreachable")
	}
}

// rachelCh forwards non-font mailbox notifications to rachel's WM code.
var rachelCh chan<- sys.MailboxNotification

// MazarinShepherd receives the FontSvcInjector injection from rachel.
// The kernel's LoadMaz looks for this exact symbol name.
// Uses an interface type assertion (not concrete struct pointer) because
// interface assertions work across .maz module boundaries via itabsinit.
//
//go:noinline
func MazarinShepherd(injected interface{}) error {
	if injected == nil {
		rawPuts("[fontsvc] MazarinShepherd: nil injection\n")
		return nil
	}
	inj, ok := injected.(fontcache.FontSvcInjector)
	if !ok {
		rawPuts("[fontsvc] MazarinShepherd: type assertion failed\n")
		return nil
	}
	rachelCh = inj.GetRachelChannel()
	return nil
}

// --- Font state ---

type fontSlot struct {
	inUse    bool
	path     string
	variant  int32
	size     int32
	cache    []byte        // V2 glyph cache (kernel-allocated pages)
	face     *goFont.Face  // go-text Face (backed by fontData)
	fontData []byte        // raw font file bytes (from LoadFile)
	scale    float32       // pointSize / upem
}

var fonts [fontcache.MaxFonts]fontSlot

// fontIdx resolves family names to filesystem paths. Loaded lazily.
var fontIdx *fontcache.FontIndex

// Per-shepherd state for ringbuf communication.
type shepherdConn struct {
	returnRb   *ringbuf.RingBuffer // fontsvc → shepherd
	scratchBuf []byte              // scratch page for tier-2 glyphs (4KB)
	scratchVA  uintptr             // VA of scratch page in shepherd's space
}

// shepherdConns is a simple list — avoids Go maps which require
// runtime.memhash64Fallback (not available in .maz host on ARM64).
type shepherdConns struct {
	sids  [32]int
	conns [32]*shepherdConn
	count int
}

func (sc *shepherdConns) get(sid int) (*shepherdConn, int) {
	for i := 0; i < sc.count; i++ {
		if sc.sids[i] == sid {
			return sc.conns[i], i
		}
	}
	return nil, -1
}

func (sc *shepherdConns) put(sid int, conn *shepherdConn) {
	if sc.count < 32 {
		sc.sids[sc.count] = sid
		sc.conns[sc.count] = conn
		sc.count++
	}
}

var shepherds shepherdConns

// allocFontID finds the next free font slot.
func allocFontID() int32 {
	for i := int32(0); i < fontcache.MaxFonts; i++ {
		if !fonts[i].inUse {
			return i
		}
	}
	return -1
}

// findCachedFont checks if (path, variant, size) is already loaded.
func findCachedFont(path string, variant int32, size int32) int32 {
	for i := int32(0); i < fontcache.MaxFonts; i++ {
		if fonts[i].inUse && fonts[i].path == path &&
			fonts[i].variant == variant && fonts[i].size == size {
			return i
		}
	}
	return -1
}

// findExistingFont finds an existing slot with the same path+variant (any size).
// Returns the parsed Face and font data so we can skip LoadFile+Parse.
func findExistingFont(path string, variant int32) (*goFont.Face, []byte) {
	for i := int32(0); i < fontcache.MaxFonts; i++ {
		if fonts[i].inUse && fonts[i].path == path &&
			fonts[i].variant == variant && fonts[i].face != nil {
			return fonts[i].face, fonts[i].fontData
		}
	}
	return nil, nil
}

// MazarinMain is the fontsvc entry point. It runs the MailboxRecv loop,
// handling font requests and forwarding other notifications to rachel.
//
//go:noinline
func MazarinMain() {
	rawPuts("[fontsvc] starting mailbox loop\n")

	for {
		notif, err := sys.MailboxRecv()
		if err != nil {
			rawPuts("[fontsvc] MailboxRecv error\n")
			continue
		}

		switch notif.Code {
		case wm.FontNotify:
			handleFontNotify(notif)
		default:
			// Forward to rachel's WM code.
			rachelCh <- notif
		}
	}
}

func handleFontNotify(notif sys.MailboxNotification) {
	rb := ringbuf.Open(uintptr(notif.RingAddr))
	senderSID := int(notif.SenderSID)

	var raw [wm.SizeWMMessage]byte
	for rb.Pop(unsafe.Pointer(&raw[0])) {
		msgType := *(*int64)(unsafe.Pointer(&raw[0]))
		switch msgType {
		case wm.MsgOpenFont:
			msg := (*wm.OpenFontMsg)(unsafe.Pointer(&raw[0]))
			handleOpenFont(senderSID, msg)
		case wm.MsgRequestGlyph:
			msg := (*wm.RequestGlyphMsg)(unsafe.Pointer(&raw[0]))
			handleRequestGlyph(senderSID, msg)
		default:
			rawPuts("[fontsvc] unknown font msg type\n")
		}
	}
}

func handleOpenFont(senderSID int, msg *wm.OpenFontMsg) {
	family := cstring(msg.Path[:])

	// Load font index lazily.
	if fontIdx == nil {
		var err error
		fontIdx, err = fontcache.LoadFontIndex("/fonts/fonts.csv")
		if err != nil {
			rawPuts("[fontsvc] failed to load font index: " + err.Error() + "\n")
			return
		}
		rawPuts("[fontsvc] font index loaded\n")
	}

	// Resolve family name + variant to filesystem path.
	style := "Regular"
	if msg.Variant == 1 {
		style = "Bold"
	}
	path := fontIdx.Resolve(family, style)
	if path == "" {
		rawPuts("[fontsvc] unknown font family: " + family + "/" + style + "\n")
		return
	}

	// Ensure we have a return channel to this shepherd.
	conn, connIdx := getOrCreateConn(senderSID)
	if conn == nil {
		rawPuts("[fontsvc] failed to create conn for shepherd\n")
		return
	}

	// Check cache.
	fontID := findCachedFont(path, msg.Variant, msg.Size)
	if fontID >= 0 {
		shareCacheAndReply(conn, connIdx, senderSID, fontID)
		return
	}

	// Cache miss — load and render.
	fontID = allocFontID()
	if fontID < 0 {
		rawPuts("[fontsvc] no free font slots\n")
		sendOpenFontError(conn, senderSID)
		return
	}

	// Try reusing an already-parsed font (same file, different size).
	face, fontData := findExistingFont(path, msg.Variant)
	if face == nil {
		// Load font file from FAT32.
		result, loadErr := sys.LoadFile(path)
		if loadErr != nil {
			rawPuts("[fontsvc] LoadFile failed: " + path + "\n")
			sendOpenFontError(conn, senderSID)
			return
		}
		fontData = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(result.StartVA))), result.BytesRead)

		// Parse font using go-text.
		var err error
		face, err = goFont.ParseTTF(bytes.NewReader(fontData))
		if err != nil {
			rawPuts("[fontsvc] ParseTTF failed for " + path + ": " + err.Error() + "\n")
			sendOpenFontError(conn, senderSID)
			return
		}
	}

	upem := float32(face.Upem())
	scale := float32(msg.Size) / upem

	// Compute metrics.
	metrics := textshape.ComputeFontMetrics(face, scale, fontID)

	// Allocate cache pages via kernel (variable size, up to 4MB).
	maxCachePages := (textshape.MaxCacheSize + 4095) / 4096
	cache, err2 := mem.AllocPagesSlice(maxCachePages, mem.PageFontCache)
	if err2 != nil {
		rawPuts("[fontsvc] AllocPages for cache failed — requesting panic\n")
		rawPutsInt(maxCachePages)
		rawPuts(" pages for ")
		rawPuts(path)
		rawPuts(" size=")
		rawPutsInt(int(msg.Size))
		rawPuts("\n")
		panic("[fontsvc] AllocPages for font cache failed: OOM")
	}

	// Build V2 cache into kernel-allocated pages.
	cache = textshape.BuildGlyphCacheInto(cache, face, scale, uint32(fontID), msg.Size, metrics)

	// Store font slot.
	fonts[fontID] = fontSlot{
		inUse:    true,
		path:     path,
		variant:  msg.Variant,
		size:     msg.Size,
		cache:    cache,
		face:     face,
		fontData: fontData,
		scale:    scale,
	}

	shareCacheAndReply(conn, connIdx, senderSID, fontID)
}

// sharedMapping tracks the VA of previously shared pages.
type sharedMapping struct {
	firstVA  uintptr
	numPages int32
}

// perFontSharedVAs stores shared VAs for cache and font file per fontID per shepherd.
type perFontSharedVAs struct {
	cacheVAs [fontcache.MaxFonts]sharedMapping
	fontVAs  [fontcache.MaxFonts]sharedMapping
	valid    [fontcache.MaxFonts]bool
}

var sharedVAs [32]perFontSharedVAs // indexed by shepherdConns slot

func shareCacheAndReply(conn *shepherdConn, connIdx int, senderSID int, fontID int32) {
	slot := &fonts[fontID]
	cache := slot.cache

	// Read total size from V2 header.
	type v2Hdr struct {
		Magic     uint32
		Version   uint32
		PointSize int32
		FontID    uint32
		NumGlyphs uint32
		NumCPMap  uint32
		GIDMapOff uint32
		CPMapOff  uint32
		TotalSize uint32
	}
	hdr := (*v2Hdr)(unsafe.Pointer(&cache[0]))
	cacheUsed := hdr.TotalSize
	numCachePages := int32((int(cacheUsed) + 4095) / 4096)
	if numCachePages < 1 {
		numCachePages = 1
	}

	var cacheFirstVA uintptr
	var fontFirstVA uintptr
	numFontPages := int32((len(slot.fontData) + 4095) / 4096)

	if connIdx >= 0 && connIdx < 32 && sharedVAs[connIdx].valid[fontID] {
		// Already shared — reuse previous VAs.
		cacheFirstVA = sharedVAs[connIdx].cacheVAs[fontID].firstVA
		fontFirstVA = sharedVAs[connIdx].fontVAs[fontID].firstVA
	} else {
		// Share cache pages.
		cacheBase := uintptr(unsafe.Pointer(&cache[0]))
		for i := int32(0); i < numCachePages; i++ {
			pageVA := cacheBase + uintptr(i)*4096
			targetVA, err := sys.MailboxMapPage(senderSID, pageVA)
			if err != nil {
				rawPuts("[fontsvc] MailboxMapPage cache failed for page ")
				rawPutsInt(int(i))
				rawPuts("\n")
				sendOpenFontError(conn, senderSID)
				return
			}
			if i == 0 {
				cacheFirstVA = targetVA
			}
		}

		// Share font file pages.
		fontBase := uintptr(unsafe.Pointer(&slot.fontData[0]))
		for i := int32(0); i < numFontPages; i++ {
			pageVA := fontBase + uintptr(i)*4096
			targetVA, err := sys.MailboxMapPage(senderSID, pageVA)
			if err != nil {
				rawPuts("[fontsvc] MailboxMapPage font failed for page ")
				rawPutsInt(int(i))
				rawPuts("\n")
				sendOpenFontError(conn, senderSID)
				return
			}
			if i == 0 {
				fontFirstVA = targetVA
			}
		}

		// Cache the mappings.
		if connIdx >= 0 && connIdx < 32 {
			sharedVAs[connIdx].cacheVAs[fontID] = sharedMapping{firstVA: cacheFirstVA, numPages: numCachePages}
			sharedVAs[connIdx].fontVAs[fontID] = sharedMapping{firstVA: fontFirstVA, numPages: numFontPages}
			sharedVAs[connIdx].valid[fontID] = true
		}
	}

	// Send reply.
	var reply wm.OpenFontReplyMsg
	reply.Type = wm.MsgOpenFontReply
	reply.FontID = fontID
	reply.NumCachePages = numCachePages
	reply.CacheAddr = uint64(cacheFirstVA)
	reply.CacheSize = uint64(cacheUsed)
	reply.Height = textshape.ComputeFontMetrics(slot.face, slot.scale, fontID).Height
	reply.Ascent = textshape.ComputeFontMetrics(slot.face, slot.scale, fontID).Ascent
	reply.Descent = textshape.ComputeFontMetrics(slot.face, slot.scale, fontID).Descent
	reply.NumFontPages = numFontPages
	reply.FontAddr = uint64(fontFirstVA)
	reply.FontSize = uint64(len(slot.fontData))

	conn.returnRb.Push(unsafe.Pointer(&reply))
	if err := sys.MailboxSend(senderSID, wm.FontResponse, conn.returnRb.Addr()); err != nil {
		rawPuts("[fontsvc] MailboxSend reply failed\n")
	}
}

func handleRequestGlyph(senderSID int, msg *wm.RequestGlyphMsg) {
	conn, _ := shepherds.get(senderSID)
	if conn == nil {
		rawPuts("[fontsvc] RequestGlyph from unknown shepherd\n")
		return
	}

	fontID := msg.FontID
	if fontID < 0 || fontID >= fontcache.MaxFonts || !fonts[fontID].inUse {
		rawPuts("[fontsvc] RequestGlyph: invalid fontID\n")
		return
	}

	slot := &fonts[fontID]

	// Resolve GID: prefer msg.GID, fall back to codepoint→GID via NominalGlyph.
	gid := goFont.GID(msg.GID)
	if msg.GID == 0 && msg.Codepoint != 0 {
		g, ok := slot.face.NominalGlyph(rune(msg.Codepoint))
		if !ok {
			// Send empty reply.
			var reply wm.GlyphReplyMsg
			reply.Type = wm.MsgGlyphReply
			reply.FontID = fontID
			reply.GID = msg.GID
			reply.GlyphSize = 0
			conn.returnRb.Push(unsafe.Pointer(&reply))
			sys.MailboxSend(senderSID, wm.FontResponse, conn.returnRb.Addr())
			return
		}
		gid = g
	}

	// Render the glyph using textshape.RenderGlyph.
	info, alpha := textshape.RenderGlyph(slot.face, gid, slot.scale)
	if info == nil {
		// Send empty reply.
		var reply wm.GlyphReplyMsg
		reply.Type = wm.MsgGlyphReply
		reply.FontID = fontID
		reply.GID = int32(gid)
		reply.GlyphSize = 0
		conn.returnRb.Push(unsafe.Pointer(&reply))
		sys.MailboxSend(senderSID, wm.FontResponse, conn.returnRb.Addr())
		return
	}

	// Ensure scratch buffer exists and is shared.
	if conn.scratchBuf == nil {
		var allocErr error
		conn.scratchBuf, allocErr = mem.AllocPagesSlice(1, mem.PageIPC)
		if allocErr != nil {
			rawPuts("[fontsvc] scratch AllocPages failed\n")
			return
		}
		scratchBase := uintptr(unsafe.Pointer(&conn.scratchBuf[0]))
		targetVA, err := sys.MailboxMapPage(senderSID, scratchBase)
		if err != nil {
			rawPuts("[fontsvc] scratch MailboxMapPage failed\n")
			return
		}
		conn.scratchVA = targetVA
	}

	w := info.Width
	h := info.Height
	totalSize := fontcache.GlyphTotalSize(w, h)
	if totalSize > 4096 {
		rawPuts("[fontsvc] glyph too large for scratch page\n")
		return
	}

	// Write glyph to scratch buffer.
	scratch := conn.scratchBuf
	ge := (*fontcache.GlyphEntry)(unsafe.Pointer(&scratch[0]))
	ge.Advance = info.Advance
	ge.DrMinX = info.DrMinX
	ge.DrMinY = info.DrMinY
	ge.DrMaxX = info.DrMaxX
	ge.DrMaxY = info.DrMaxY
	ge.Width = w
	ge.Height = h

	if w > 0 && h > 0 && len(alpha) > 0 {
		copy(scratch[fontcache.GlyphEntrySize:], alpha)
	}

	// Send reply with pointer to scratch in shepherd's space.
	var reply wm.GlyphReplyMsg
	reply.Type = wm.MsgGlyphReply
	reply.FontID = fontID
	reply.GID = int32(gid)
	reply.ScratchAddr = uint64(conn.scratchVA)
	reply.GlyphSize = totalSize
	conn.returnRb.Push(unsafe.Pointer(&reply))
	sys.MailboxSend(senderSID, wm.FontResponse, conn.returnRb.Addr())
}

func sendOpenFontError(conn *shepherdConn, senderSID int) {
	var reply wm.OpenFontReplyMsg
	reply.Type = wm.MsgOpenFontReply
	reply.FontID = -1
	conn.returnRb.Push(unsafe.Pointer(&reply))
	sys.MailboxSend(senderSID, wm.FontResponse, conn.returnRb.Addr())
}

func getOrCreateConn(sid int) (*shepherdConn, int) {
	if conn, idx := shepherds.get(sid); conn != nil {
		return conn, idx
	}
	// Create return ringbuf to this shepherd.
	returnRb, err := ringbuf.New(sid, 0, wm.SizeWMMessage, wm.DefaultSlotCount)
	if err != nil {
		rawPuts("[fontsvc] ringbuf.New for return channel failed\n")
		return nil, -1
	}
	conn := &shepherdConn{returnRb: returnRb}
	shepherds.put(sid, conn)
	// The index is the last added slot.
	return conn, shepherds.count - 1
}

func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// rawPuts writes a string directly to UART, bypassing the delegation
// system. Using sys.RawWrite (SYS_WRITE) per byte would generate one
// Write delegation round-trip per character, stalling callers.
func rawPuts(s string) {
	sys.UartWriteString(s)
}

func rawPutsInt(n int) {
	if n < 0 {
		rawPuts("-")
		n = -n
	}
	if n == 0 {
		rawPuts("0")
		return
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	rawPuts(string(buf[i:]))
}

func main() {
	MazarinMain()
}
