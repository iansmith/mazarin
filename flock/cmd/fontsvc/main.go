// fontsvc is a .maz module loaded by rachel that provides centralized font
// loading and glyph rendering. It owns rachel's MailboxRecv loop: font
// messages are handled directly; all other notifications are forwarded to
// rachel via a Go channel injected through MazarinPriest.
//
// From the kernel's perspective, fontsvc IS rachel (same PID/SID).
package main

import (
	"image"
	"image/color"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/sys"
	"mazzy/shared/wm"
	"sort"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
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
	rawPuts("[fontsvc] MazarinShepherd: channel received\n")
	return nil
}

// --- Font state ---

type fontSlot struct {
	inUse   bool
	path    string
	variant int32
	size    int32
	cache   []byte   // 2MB glyph cache (owned by fontsvc)
	otFont  *opentype.Font
	face    font.Face
}

var fonts [fontcache.MaxFonts]fontSlot

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

// findParsedFont finds an existing slot with the same path+variant (any size).
// Returns the parsed *opentype.Font so we can skip LoadFile+Parse.
func findParsedFont(path string, variant int32) *opentype.Font {
	for i := int32(0); i < fontcache.MaxFonts; i++ {
		if fonts[i].inUse && fonts[i].path == path &&
			fonts[i].variant == variant && fonts[i].otFont != nil {
			return fonts[i].otFont
		}
	}
	return nil
}

// MazarinMain is the fontsvc entry point. It runs the MailboxRecv loop,
// handling font requests and forwarding other notifications to rachel.
//
//go:noinline
func MazarinMain() {
	// .maz init functions don't run, so image package globals are nil.
	// opentype's face.Glyph() calls rast.Draw(..., image.Opaque, ...) internally,
	// which panics on nil *image.Uniform. Initialize the required globals here.
	image.Opaque = image.NewUniform(color.Alpha16{A: 0xffff})
	image.Transparent = image.NewUniform(color.Alpha16{A: 0})
	image.Black = image.NewUniform(color.Gray16{Y: 0})
	image.White = image.NewUniform(color.Gray16{Y: 0xffff})

	rawPuts("[fontsvc] starting mailbox loop\n")

	for {
		notif, err := sys.MailboxRecv()
		if err != nil {
			rawPuts("[fontsvc] MailboxRecv error\n")
			continue
		}

		rawPuts("[fontsvc] recv: code=")
		rawPutsInt(int(notif.Code))
		rawPuts(" from SID=")
		rawPutsInt(int(notif.SenderSID))
		rawPuts("\n")

		switch notif.Code {
		case wm.FontNotify:
			handleFontNotify(notif)
			rawPuts("[fontsvc] handleFontNotify returned\n")
		default:
			// Forward to rachel's WM code.
			rachelCh <- notif
			rawPuts("[fontsvc] forwarded to rachelCh\n")
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
	path := cstring(msg.Path[:])
	rawPuts("[fontsvc] OpenFont: " + path + " size=")
	rawPutsInt(int(msg.Size))
	rawPuts(" variant=")
	rawPutsInt(int(msg.Variant))
	rawPuts(" from SID=")
	rawPutsInt(senderSID)
	rawPuts("\n")

	// Ensure we have a return channel to this shepherd.
	rawPuts("[fontsvc] step1: getOrCreateConn\n")
	conn, connIdx := getOrCreateConn(senderSID)
	if conn == nil {
		rawPuts("[fontsvc] failed to create conn for shepherd\n")
		return
	}

	// Check cache.
	rawPuts("[fontsvc] step2: findCachedFont\n")
	fontID := findCachedFont(path, msg.Variant, msg.Size)
	if fontID >= 0 {
		rawPuts("[fontsvc] cache hit, fontID=")
		rawPutsInt(int(fontID))
		rawPuts("\n")
		shareCacheAndReply(conn, connIdx, senderSID, fontID)
		return
	}

	// Cache miss — load and render.
	rawPuts("[fontsvc] step3: cache miss, allocFontID\n")
	fontID = allocFontID()
	if fontID < 0 {
		rawPuts("[fontsvc] no free font slots\n")
		sendOpenFontError(conn, senderSID)
		return
	}
	rawPuts("[fontsvc] step3: fontID=")
	rawPutsInt(int(fontID))
	rawPuts("\n")

	// Try reusing an already-parsed font (same file, different size).
	otFont := findParsedFont(path, msg.Variant)
	if otFont != nil {
		rawPuts("[fontsvc] step4: reusing parsed font (skip LoadFile+Parse)\n")
	} else {
		// Load font file from FAT32.
		rawPuts("[fontsvc] step4: LoadFile\n")
		result, loadErr := sys.LoadFile(path)
		if loadErr != nil {
			rawPuts("[fontsvc] LoadFile failed: " + path + "\n")
			sendOpenFontError(conn, senderSID)
			return
		}
		rawPuts("[fontsvc] step4: LoadFile done, bytes=")
		rawPutsInt(int(result.BytesRead))
		rawPuts("\n")
		fontData := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(result.StartVA))), result.BytesRead)

		// Parse font.
		rawPuts("[fontsvc] step5: opentype.Parse\n")
		var err error
		otFont, err = opentype.Parse(fontData)
		if err != nil {
			rawPuts("[fontsvc] opentype.Parse failed\n")
			sendOpenFontError(conn, senderSID)
			return
		}
		rawPuts("[fontsvc] step5: Parse done\n")
	}

	// Create face at requested size.
	rawPuts("[fontsvc] step6: NewFace\n")
	face, err := opentype.NewFace(otFont, &opentype.FaceOptions{
		Size:    float64(msg.Size),
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		rawPuts("[fontsvc] NewFace failed\n")
		sendOpenFontError(conn, senderSID)
		return
	}
	rawPuts("[fontsvc] step6: NewFace done\n")

	// Allocate cache pages via kernel (properly typed as PageFontCache).
	rawPuts("[fontsvc] step7: AllocPages for cache\n")
	cachePages := (fontcache.CacheSizeBytes + 4095) / 4096
	cache, err2 := sys.AllocPagesSlice(cachePages, sys.PageFontCache)
	if err2 != nil {
		rawPuts("[fontsvc] AllocPages for cache failed\n")
		sendOpenFontError(conn, senderSID)
		return
	}
	rawPuts("[fontsvc] step7: AllocPages done, pages=")
	rawPutsInt(cachePages)
	rawPuts("\n")
	metrics := face.Metrics()

	rawPuts("[fontsvc] step8: buildGlyphCache\n")
	numGlyphs := buildGlyphCache(cache, face, metrics, uint32(fontID), msg.Size)

	rawPuts("[fontsvc] step8: rendered ")
	rawPutsInt(int(numGlyphs))
	rawPuts(" glyphs for " + path + "\n")

	// Store font slot.
	fonts[fontID] = fontSlot{
		inUse:   true,
		path:    path,
		variant: msg.Variant,
		size:    msg.Size,
		cache:   cache,
		otFont:  otFont,
		face:    face,
	}

	rawPuts("[fontsvc] step9: shareCacheAndReply\n")
	shareCacheAndReply(conn, connIdx, senderSID, fontID)
}

// buildGlyphCache populates the cache buffer with header, glyph map, and glyph data.
// Returns the number of glyphs rendered.
func buildGlyphCache(cache []byte, face font.Face, metrics font.Metrics,
	fontID uint32, pointSize int32) uint32 {

	rawPuts("[fontsvc] buildGlyphCache: enter fontID=")
	rawPutsInt(int(fontID))
	rawPuts("\n")

	// Phase 1: Enumerate all codepoints the font supports.
	type cpEntry struct {
		cp      rune
		advance fixed.Int26_6
	}
	var supported []cpEntry

	// Scan BMP (U+0020 to U+FFFF).
	rawPuts("[fontsvc] buildGlyphCache: phase1 scanning BMP\n")
	for cp := rune(0x0020); cp <= 0xFFFF; cp++ {
		adv, ok := face.GlyphAdvance(cp)
		if ok && adv > 0 {
			supported = append(supported, cpEntry{cp: cp, advance: adv})
		}
	}
	rawPuts("[fontsvc] buildGlyphCache: phase1 done, supported=")
	rawPutsInt(len(supported))
	rawPuts("\n")

	sort.Slice(supported, func(i, j int) bool {
		return supported[i].cp < supported[j].cp
	})
	rawPuts("[fontsvc] buildGlyphCache: sorted\n")

	// Phase 2: Calculate layout.
	// Header(64) + MapEntries(8 each) + GlyphData.
	headerSize := uint32(64)
	mapEntrySize := uint32(8)
	maxGlyphs := uint32(len(supported))

	// We'll determine how many glyphs fit by rendering them.
	mapOffset := headerSize
	dataOffset := mapOffset + maxGlyphs*mapEntrySize

	// Phase 3: Render glyphs, packing into cache.
	rawPuts("[fontsvc] buildGlyphCache: phase3 rendering\n")
	dataPos := dataOffset
	var rendered uint32

	// Temporary map entries (we'll write them after we know how many fit).
	mapBuf := make([]fontcache.GlyphMapEntry, 0, len(supported))

	dot := fixed.Point26_6{}
	for _, s := range supported {
		dr, mask, maskp, advance, ok := face.Glyph(dot, s.cp)
		if !ok {
			continue
		}

		w := uint16(dr.Dx())
		h := uint16(dr.Dy())
		entrySize := fontcache.GlyphTotalSize(w, h)

		// Check if this glyph fits.
		if dataPos+entrySize > fontcache.CacheSizeBytes {
			break // cache full
		}

		// Write GlyphEntry header.
		ge := (*fontcache.GlyphEntry)(unsafe.Pointer(&cache[dataPos]))
		ge.Advance = int32(advance)
		ge.DrMinX = int16(dr.Min.X)
		ge.DrMinY = int16(dr.Min.Y)
		ge.DrMaxX = int16(dr.Max.X)
		ge.DrMaxY = int16(dr.Max.Y)
		ge.Width = w
		ge.Height = h

		// Copy alpha pixels.
		if w > 0 && h > 0 && mask != nil {
			alphaOff := dataPos + fontcache.GlyphEntrySize
			copyAlphaPixels(cache[alphaOff:], mask, maskp, int(w), int(h))
		}

		mapBuf = append(mapBuf, fontcache.GlyphMapEntry{
			Codepoint: uint32(s.cp),
			Offset:    dataPos,
		})

		dataPos += entrySize
		rendered++
	}

	rawPuts("[fontsvc] buildGlyphCache: phase3 done, rendered=")
	rawPutsInt(int(rendered))
	rawPuts("\n")

	// Now we know the actual glyph count. Recompute layout if map is smaller.
	actualMapSize := uint32(len(mapBuf)) * mapEntrySize
	if actualMapSize < maxGlyphs*mapEntrySize {
		// Shift glyph data forward to close the gap.
		newDataOffset := mapOffset + actualMapSize
		gap := dataOffset - newDataOffset
		if gap > 0 {
			// Shift all glyph data backward.
			copy(cache[newDataOffset:], cache[dataOffset:dataPos])
			dataPos -= gap
			dataOffset = newDataOffset
			// Adjust all offsets in mapBuf.
			for i := range mapBuf {
				mapBuf[i].Offset -= gap
			}
		}
	}

	// Write glyph map.
	for i, entry := range mapBuf {
		me := (*fontcache.GlyphMapEntry)(unsafe.Pointer(&cache[mapOffset+uint32(i)*mapEntrySize]))
		me.Codepoint = entry.Codepoint
		me.Offset = entry.Offset
	}

	// Write header.
	hdr := (*fontcache.CacheHeader)(unsafe.Pointer(&cache[0]))
	hdr.Magic = fontcache.CacheMagic
	hdr.Version = fontcache.CacheVersion
	hdr.PointSize = pointSize
	hdr.FontID = fontID
	hdr.Height = int32(metrics.Height)
	hdr.Ascent = int32(metrics.Ascent)
	hdr.Descent = int32(metrics.Descent)
	hdr.XHeight = int32(metrics.XHeight)
	hdr.CapHeight = int32(metrics.CapHeight)
	hdr.NumGlyphs = rendered
	hdr.MapOffset = mapOffset
	hdr.DataOffset = dataOffset
	hdr.TotalUsed = dataPos

	rawPuts("[fontsvc] buildGlyphCache: exit, totalUsed=")
	rawPutsInt(int(dataPos))
	rawPuts("\n")
	return rendered
}

// copyAlphaPixels copies the alpha channel from a mask image into a flat buffer.
func copyAlphaPixels(dst []byte, mask image.Image, maskp image.Point, w, h int) {
	switch m := mask.(type) {
	case *image.Alpha:
		for y := 0; y < h; y++ {
			srcOff := m.PixOffset(maskp.X, maskp.Y+y)
			dstOff := y * w
			if srcOff+w <= len(m.Pix) && dstOff+w <= len(dst) {
				copy(dst[dstOff:dstOff+w], m.Pix[srcOff:srcOff+w])
			}
		}
	default:
		// Fallback: read pixel by pixel.
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				_, _, _, a := mask.At(maskp.X+x, maskp.Y+y).RGBA()
				dst[y*w+x] = byte(a >> 8)
			}
		}
	}
}

// sharedMapping tracks the VA of a previously shared font cache.
type sharedMapping struct {
	firstVA  uintptr
	numPages int32
}

// perFontSharedVAs stores the shared VA for each fontID per shepherd.
// Avoids re-mapping pages on cache hits.
type perFontSharedVAs struct {
	vas   [fontcache.MaxFonts]sharedMapping
	valid [fontcache.MaxFonts]bool
}

var sharedVAs [32]perFontSharedVAs // indexed by shepherdConns slot

func shareCacheAndReply(conn *shepherdConn, connIdx int, senderSID int, fontID int32) {
	rawPuts("[fontsvc] share: enter fontID=")
	rawPutsInt(int(fontID))
	rawPuts(" senderSID=")
	rawPutsInt(senderSID)
	rawPuts("\n")

	slot := &fonts[fontID]
	cache := slot.cache
	hdr := (*fontcache.CacheHeader)(unsafe.Pointer(&cache[0]))
	numPages := int32((int(hdr.TotalUsed) + 4095) / 4096)
	if numPages < 1 {
		numPages = 1
	}

	var firstVA uintptr

	// Check if this font is already shared with this shepherd.
	if connIdx >= 0 && connIdx < 32 && sharedVAs[connIdx].valid[fontID] {
		// Already shared — reuse previous VA.
		firstVA = sharedVAs[connIdx].vas[fontID].firstVA
		rawPuts("[fontsvc] share: reusing cached VA\n")
	} else {
		// First time sharing — map cache pages.
		rawPuts("[fontsvc] share: mapping ")
		rawPutsInt(int(numPages))
		rawPuts(" pages\n")
		cacheBase := uintptr(unsafe.Pointer(&cache[0]))
		for i := int32(0); i < numPages; i++ {
			pageVA := cacheBase + uintptr(i)*4096
			targetVA, err := sys.MailboxMapPage(senderSID, pageVA)
			if err != nil {
				rawPuts("[fontsvc] MailboxMapPage failed for page ")
				rawPutsInt(int(i))
				rawPuts("\n")
				sendOpenFontError(conn, senderSID)
				return
			}
			if i == 0 {
				firstVA = targetVA
			}
		}
		rawPuts("[fontsvc] share: all pages mapped\n")
		// Cache the mapping.
		if connIdx >= 0 && connIdx < 32 {
			sharedVAs[connIdx].vas[fontID] = sharedMapping{firstVA: firstVA, numPages: numPages}
			sharedVAs[connIdx].valid[fontID] = true
		}
	}

	// Send reply.
	rawPuts("[fontsvc] share: sending reply\n")
	var reply wm.OpenFontReplyMsg
	reply.Type = wm.MsgOpenFontReply
	reply.FontID = fontID
	reply.NumPages = numPages
	reply.CacheAddr = uint64(firstVA)
	reply.CacheSize = uint64(hdr.TotalUsed)
	reply.Height = int32(hdr.Height)
	reply.Ascent = int32(hdr.Ascent)
	reply.Descent = int32(hdr.Descent)

	conn.returnRb.Push(unsafe.Pointer(&reply))
	rawPuts("[fontsvc] share: pushed to returnRb, calling MailboxSend\n")
	if err := sys.MailboxSend(senderSID, wm.FontResponse, conn.returnRb.Addr()); err != nil {
		rawPuts("[fontsvc] MailboxSend reply failed\n")
	}

	rawPuts("[fontsvc] share: done fontID=")
	rawPutsInt(int(fontID))
	rawPuts(" pages=")
	rawPutsInt(int(numPages))
	rawPuts("\n")
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
	cp := rune(msg.Codepoint)

	// Render the glyph.
	dot := fixed.Point26_6{}
	dr, mask, maskp, advance, ok := slot.face.Glyph(dot, cp)
	if !ok {
		// Send empty reply.
		var reply wm.GlyphReplyMsg
		reply.Type = wm.MsgGlyphReply
		reply.FontID = fontID
		reply.Codepoint = msg.Codepoint
		reply.GlyphSize = 0
		conn.returnRb.Push(unsafe.Pointer(&reply))
		sys.MailboxSend(senderSID, wm.FontResponse, conn.returnRb.Addr())
		return
	}

	// Ensure scratch buffer exists and is shared.
	if conn.scratchBuf == nil {
		var allocErr error
		conn.scratchBuf, allocErr = sys.AllocPagesSlice(1, sys.PageIPC)
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

	w := uint16(dr.Dx())
	h := uint16(dr.Dy())
	totalSize := fontcache.GlyphTotalSize(w, h)
	if totalSize > 4096 {
		rawPuts("[fontsvc] glyph too large for scratch page\n")
		return
	}

	// Write glyph to scratch buffer.
	scratch := conn.scratchBuf
	ge := (*fontcache.GlyphEntry)(unsafe.Pointer(&scratch[0]))
	ge.Advance = int32(advance)
	ge.DrMinX = int16(dr.Min.X)
	ge.DrMinY = int16(dr.Min.Y)
	ge.DrMaxX = int16(dr.Max.X)
	ge.DrMaxY = int16(dr.Max.Y)
	ge.Width = w
	ge.Height = h

	if w > 0 && h > 0 && mask != nil {
		copyAlphaPixels(scratch[fontcache.GlyphEntrySize:], mask, maskp, int(w), int(h))
	}

	// Send reply with pointer to scratch in shepherd's space.
	var reply wm.GlyphReplyMsg
	reply.Type = wm.MsgGlyphReply
	reply.FontID = fontID
	reply.Codepoint = msg.Codepoint
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
