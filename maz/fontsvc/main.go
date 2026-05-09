// fontsvc is a .maz module loaded by rachel that provides centralized font
// loading and glyph rendering. It owns rachel's uring IPC loop: font
// messages are handled directly; all other notifications are forwarded to
// rachel via a Go channel injected through MazarinShepherd.
//
// From the kernel's perspective, fontsvc IS rachel (same PID/SID).
package main

import (
	"bytes"
	"errors"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazarin/textshape"
	"mazzy/shared/wm"
	"unsafe"

	goFont "github.com/go-text/typesetting/font"
)

// MazEntryPoint holds a reference to MazarinMain to prevent DCE.
var MazEntryPoint func() = MazarinMain

// MazarinShepherdAddr holds a reference to MazarinShepherd to prevent DCE.
var MazarinShepherdAddr func(interface{}) error = MazarinShepherd

// fc is the fs IPC client, set by MazarinShepherd from the injector.
var fc *fsclient.Client

func init() {
	if MazEntryPoint == nil {
		panic("unreachable")
	}
	if MazarinShepherdAddr == nil {
		panic("unreachable")
	}
	for i := range tempFontOwner {
		tempFontOwner[i] = -1
	}
}

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
	// Register handlers with rachel. Rachel calls these directly from the
	// uring Dispatcher goroutine (rachel's runtime), passing scalar/array
	// values instead of interface{} to avoid cross-.maz type assertions.
	inj.RegisterOpenFontHandler(handleOpenFontCallback)
	inj.RegisterRequestGlyphHandler(handleRequestGlyphCallback)
	inj.RegisterOpenTemporaryFontHandler(handleOpenTemporaryFontCallback)
	inj.RegisterCloseTemporaryFontHandler(handleCloseTemporaryFontCallback)
	inj.RegisterRegisterFontBufferHandler(handleRegisterFontBufferCallback)
	inj.RegisterUnregisterFontBufferHandler(handleUnregisterFontBufferCallback)
	inj.RegisterCleanupHandler(CleanupShepherdFonts)

	// Register in-process font callbacks so rachel can use fonts directly
	// without uring IPC or SharePages (can't share pages with yourself).
	// Uses plain function callbacks (not interfaces) to avoid cross-.maz
	// type assertion failures.
	inj.RegisterInternalOpenFont(internalOpenFont)
	inj.RegisterInternalGlyphByGID(internalGlyphByGID)

	// Extract fs IPC client from the concrete injector struct.
	if init, ok := injected.(*fontcache.FontSvcInit); ok {
		fc = init.FSClient
	}
	if fc == nil {
		rawPuts("[fontsvc] MazarinShepherd: no FSClient in injector\n")
	}

	rawPuts("[fontsvc] MazarinShepherd: handlers registered\n")
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
	fontData []byte        // raw font file bytes (from fs IPC)
	scale    float32       // pointSize / upem
}

var fonts [fontcache.MaxFonts]fontSlot

// MaxTempFonts is the size of the temporary-font pool. Distinct from
// the permanent pool (`fonts`) — temp fonts have explicit lifecycle
// via OpenTemporaryFont/CloseTemporaryFont and are recycled per render
// scope. fontIDs returned for temp slots are OR'd with wm.TempFontIDBase
// (0x1000) at the IPC boundary so dumps unambiguously identify which
// pool a fontID belongs to.
const MaxTempFonts = 32

// tempFonts is the temp pool. Each in-use slot also stores the owning
// shepherd SID so per-shepherd death cleanup can release any opens
// that the shepherd never closed (panic mid-render, killed, etc.).
var tempFonts [MaxTempFonts]fontSlot

// tempFontOwner[i] is the SID that opened tempFonts[i]. -1 = free.
var tempFontOwner [MaxTempFonts]int16

// fontIdx resolves family names to filesystem paths. Loaded lazily.
var fontIdx *textshape.FontIndex

// MaxRegisteredFonts caps the number of distinct (family, variant)
// caller-supplied byte buffers fontsvc can hold registered at once.
// Each entry records a SharePagesWithTarget'd region the client copied
// the @font-face bytes into; OpenTemporaryFont for a registered family
// parses Face from these pages and builds tier-1 cache per size. Bytes
// stay mapped until UnregisterFontBuffer (or shepherd death cleanup).
const MaxRegisteredFonts = 64

// registeredFontBytes holds one (family, variant) → byte mapping
// pushed by a shepherd via wm.RegisterFontBuffer. The bytes live in
// fontsvc's address space at fontDataVA; they are unmapped on
// successful UnregisterFontBuffer or when ownerSID dies.
//
// The caller (louis14) is expected to register-at-page-load and
// unregister-at-page-unload (or never, relying on death cleanup).
// Unregister-with-live-temp-slots is undefined behavior — fontsvc
// drops its bookkeeping on Unregister; any Face parsed from these
// bytes by an open temp slot will then have a dangling pointer.
// In practice louis14 never calls Unregister, so this isn't a
// problem yet; if a refcounted-pinning model is needed later it
// goes here.
type registeredFontBytes struct {
	inUse       bool
	family      string
	variant     int32
	fontDataVA  uintptr
	fontDataLen int64
	numPages    uint32
	ownerSID    int16
}

var registeredBytes [MaxRegisteredFonts]registeredFontBytes

// findRegisteredBytes returns the registeredBytes index for (family, variant), or -1.
func findRegisteredBytes(family string, variant int32) int32 {
	for i := int32(0); i < MaxRegisteredFonts; i++ {
		if registeredBytes[i].inUse &&
			registeredBytes[i].family == family &&
			registeredBytes[i].variant == variant {
			return i
		}
	}
	return -1
}

// allocRegisteredBytesSlot finds a free registeredBytes slot, or -1.
func allocRegisteredBytesSlot() int32 {
	for i := int32(0); i < MaxRegisteredFonts; i++ {
		if !registeredBytes[i].inUse {
			return i
		}
	}
	return -1
}

// Per-shepherd state for font IPC.
type shepherdConn struct {
	scratchBuf []byte  // scratch page for tier-2 glyphs (4KB)
	scratchVA  uintptr // VA of scratch page in shepherd's space
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

// allocTempFontID finds the next free temp slot. Returns the local
// index (0..MaxTempFonts-1); the IPC layer adds wm.TempFontIDBase
// before returning to clients.
func allocTempFontID() int32 {
	for i := int32(0); i < MaxTempFonts; i++ {
		if !tempFonts[i].inUse {
			return i
		}
	}
	return -1
}

// findCachedTempFont checks if (path, variant, size) is already
// loaded in the temp pool. Used to dedupe within a single render
// when the same face/size is requested multiple times.
func findCachedTempFont(path string, variant int32, size int32) int32 {
	for i := int32(0); i < MaxTempFonts; i++ {
		if tempFonts[i].inUse && tempFonts[i].path == path &&
			tempFonts[i].variant == variant && tempFonts[i].size == size {
			return i
		}
	}
	return -1
}

// resolveFontID returns a pointer to the slot for fontID, masking off
// the wm.TempFontIDBase bit if present. Returns nil for invalid IDs.
// Used by Face/GlyphByGID lookups that need to handle both pools.
func resolveFontID(fontID int32) *fontSlot {
	if fontID < 0 {
		return nil
	}
	if (fontID & wm.TempFontIDBase) != 0 {
		idx := fontID & wm.TempFontIDMask
		if idx < 0 || idx >= MaxTempFonts {
			return nil
		}
		if !tempFonts[idx].inUse {
			return nil
		}
		return &tempFonts[idx]
	}
	if fontID >= fontcache.MaxFonts {
		return nil
	}
	if !fonts[fontID].inUse {
		return nil
	}
	return &fonts[fontID]
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
// Returns the parsed Face and font data so we can skip ReadFile+Parse.
func findExistingFont(path string, variant int32) (*goFont.Face, []byte) {
	for i := int32(0); i < fontcache.MaxFonts; i++ {
		if fonts[i].inUse && fonts[i].path == path &&
			fonts[i].variant == variant && fonts[i].face != nil {
			return fonts[i].face, fonts[i].fontData
		}
	}
	return nil, nil
}

// MazarinMain is the fontsvc entry point. With the callback-based injection,
// all work happens in handleFontRequest (called by rachel's Dispatcher).
// MazarinMain is kept as a no-op for the LoadMaz bootstrap flow.
//
//go:noinline
func MazarinMain() {
	rawPuts("[fontsvc] MazarinMain (callback mode, no loop needed)\n")
}

// handleOpenFontCallback is called by rachel's Dispatcher with scalar values.
func handleOpenFontCallback(senderSID int, variant, size int32, path [100]byte) {
	of := &wm.OpenFont{Variant: variant, Size: size, Path: path}
	handleOpenFont(senderSID, of)
}

// handleRequestGlyphCallback is called by rachel's Dispatcher with scalar values.
func handleRequestGlyphCallback(senderSID int, fontID, gid, codepoint int32) {
	rg := &wm.RequestGlyph{FontID: fontID, GID: gid, Codepoint: codepoint}
	handleRequestGlyph(senderSID, rg)
}

// handleOpenTemporaryFontCallback adapts rachel's typed-struct dispatch
// to fontsvc's pointer-taking handler. msg is passed by value across the
// shepherd→fontsvc callback boundary; we take its address to keep the
// existing handler shape (consistent with handleOpenFont).
func handleOpenTemporaryFontCallback(senderSID int, msg wm.OpenTemporaryFont) {
	handleOpenTemporaryFont(senderSID, &msg)
}

// handleCloseTemporaryFontCallback similarly adapts the close-side
// dispatch. fontID is passed scalar; we wrap it for the existing handler.
func handleCloseTemporaryFontCallback(senderSID int, fontID int32) {
	ctf := wm.CloseTemporaryFont{FontID: fontID}
	handleCloseTemporaryFont(senderSID, &ctf)
}

// handleRegisterFontBufferCallback adapts rachel's typed-struct
// dispatch to fontsvc's pointer-taking handler.
func handleRegisterFontBufferCallback(senderSID int, msg wm.RegisterFontBuffer) {
	handleRegisterFontBuffer(senderSID, &msg)
}

// handleUnregisterFontBufferCallback adapts the unregister side.
func handleUnregisterFontBufferCallback(senderSID int, msg wm.UnregisterFontBuffer) {
	handleUnregisterFontBuffer(senderSID, &msg)
}

// ensureFontIndex loads the font index lazily.
func ensureFontIndex() error {
	if fontIdx != nil {
		return nil
	}
	if fc == nil {
		rawPuts("[fontsvc] ensureFontIndex: no fs client\n")
		return errors.New("no fs client")
	}
	data, err := fc.ReadFile("/fonts/fonts.csv")
	if err != nil {
		rawPuts("[fontsvc] failed to load font index: " + err.Error() + "\n")
		return err
	}
	fontIdx, err = fontcache.LoadFontIndex(data)
	if err != nil {
		rawPuts("[fontsvc] failed to parse font index: " + err.Error() + "\n")
		return err
	}
	rawPuts("[fontsvc] font index loaded\n")
	return nil
}

// loadOrCacheFont resolves a family+variant+size to a font slot. Returns
// the fontID (>= 0) on success, or -1 on failure. If the font is already
// cached, returns the existing slot. Otherwise loads from disk, builds the
// V2 cache, and stores a new slot.
func loadOrCacheFont(family string, variant, size int32) int32 {
	if err := ensureFontIndex(); err != nil {
		return -1
	}

	style := textshape.VariantToStyle(variant)
	filename := fontIdx.ResolveOptical(family, style, int(size))
	if filename == "" {
		rawPuts("[fontsvc] unknown font family: " + family + "/" + style + "\n")
		return -1
	}
	path := "/fonts/" + filename

	// Check cache.
	fontID := findCachedFont(path, variant, size)
	if fontID >= 0 {
		return fontID
	}

	// Cache miss — load and render.
	fontID = allocFontID()
	if fontID < 0 {
		rawPuts("[fontsvc] no free font slots\n")
		return -1
	}

	// Try reusing an already-parsed font (same file, different size).
	face, fontData := findExistingFont(path, variant)
	if face == nil {
		// Load font file via fs IPC.
		var loadErr error
		fontData, loadErr = fc.ReadFile(path)
		if loadErr != nil {
			rawPuts("[fontsvc] ReadFile failed: " + path + "\n")
			return -1
		}

		// Parse font using go-text.
		var err error
		face, err = goFont.ParseTTF(bytes.NewReader(fontData))
		if err != nil {
			rawPuts("[fontsvc] ParseTTF failed for " + path + ": " + err.Error() + "\n")
			return -1
		}
	}

	upem := float32(face.Upem())
	scale := float32(size) / upem

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
		rawPutsInt(int(size))
		rawPuts("\n")
		panic("[fontsvc] AllocPages for font cache failed: OOM")
	}

	// Build V2 cache into kernel-allocated pages.
	cache = textshape.BuildGlyphCacheInto(cache, face, scale, uint32(fontID), size, metrics)

	// Store font slot.
	fonts[fontID] = fontSlot{
		inUse:    true,
		path:     path,
		variant:  variant,
		size:     size,
		cache:    cache,
		face:     face,
		fontData: fontData,
		scale:    scale,
	}

	return fontID
}

func handleOpenFont(senderSID int, msg *wm.OpenFont) {
	family := cstring(msg.Path[:])

	fontID := loadOrCacheFont(family, msg.Variant, msg.Size)
	if fontID < 0 {
		sendOpenFontError(senderSID)
		return
	}

	// Ensure we have a return channel to this shepherd.
	conn, connIdx := getOrCreateConn(senderSID)
	if conn == nil {
		rawPuts("[fontsvc] failed to create conn for shepherd\n")
		return
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
			targetVA, err := sys.SharePages(senderSID, pageVA)
			if err != nil {
				rawPuts("[fontsvc] SharePages cache failed for page ")
				rawPutsInt(int(i))
				rawPuts("\n")
				sendOpenFontError(senderSID)
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
			targetVA, err := sys.SharePages(senderSID, pageVA)
			if err != nil {
				rawPuts("[fontsvc] SharePages font failed for page ")
				rawPutsInt(int(i))
				rawPuts("\n")
				sendOpenFontError(senderSID)
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

	// Send reply via uring.
	metrics := textshape.ComputeFontMetrics(slot.face, slot.scale, fontID)
	encoded := wm.EncodeOpenFontReply(&wm.OpenFontReply{
		FontID:        fontID,
		NumCachePages: numCachePages,
		CacheAddr:     uint64(cacheFirstVA),
		CacheSize:     uint64(cacheUsed),
		Height:        metrics.Height,
		Ascent:        metrics.Ascent,
		Descent:       metrics.Descent,
		NumFontPages:  numFontPages,
		FontAddr:      uint64(fontFirstVA),
		FontSize:      uint64(len(slot.fontData)),
	})
	if err := uring.Send(senderSID, &encoded); err != nil {
		rawPuts("[fontsvc] uring.Send OpenFontReply FAILED: senderSID=")
		rawPutsInt(senderSID)
		rawPuts(" err=")
		rawPuts(err.Error())
		rawPuts("\n")
	}
}

// resolveFamilyPath looks up (family, variant) in the font index and
// returns the absolute /fonts/ path, picking the optical size best
// matching `size`. Returns ok=false if the index can't be loaded or
// the family is unknown.
func resolveFamilyPath(family string, variant, size int32) (string, bool) {
	if err := ensureFontIndex(); err != nil {
		return "", false
	}
	style := textshape.VariantToStyle(variant)
	filename := fontIdx.ResolveOptical(family, style, int(size))
	if filename == "" {
		rawPuts("[fontsvc] unknown font family: " + family + "/" + style + "\n")
		return "", false
	}
	return "/fonts/" + filename, true
}

// loadAndParseFont loads a font file from FAT32 and parses the Face.
// Returns parseErr=true on either failure (already logged); returns
// the live byte slice (backed by the kernel-allocated load pages) and
// the parsed Face on success.
func loadAndParseFont(path string) (*goFont.Face, []byte, bool) {
	fontData, err := fc.ReadFile(path)
	if err != nil {
		rawPuts("[fontsvc] ReadFile failed: " + path + "\n")
		return nil, nil, true
	}
	face, err := goFont.ParseTTF(bytes.NewReader(fontData))
	if err != nil {
		rawPuts("[fontsvc] ParseTTF failed for " + path + ": " + err.Error() + "\n")
		return nil, nil, true
	}
	return face, fontData, false
}

// buildTempCache computes scale and builds the V2 glyph cache for a
// temp slot. The cache header records fontID with wm.TempFontIDBase
// OR'd in so dumps tag it as temp. Returns ok=false on AllocPages OOM
// (already logged).
func buildTempCache(face *goFont.Face, fontData []byte, size int32, idx int32) (float32, []byte, bool) {
	_ = fontData // reserved for future use (e.g. on-disk subsetting)
	upem := float32(face.Upem())
	scale := float32(size) / upem

	fontIDForCache := idx | wm.TempFontIDBase
	metrics := textshape.ComputeFontMetrics(face, scale, fontIDForCache)

	maxCachePages := (textshape.MaxCacheSize + 4095) / 4096
	cache, err := mem.AllocPagesSlice(maxCachePages, mem.PageFontCache)
	if err != nil {
		rawPuts("[fontsvc] AllocPagesSlice for temp cache failed (OOM)\n")
		return 0, nil, false
	}
	cache = textshape.BuildGlyphCacheInto(cache, face, scale, uint32(fontIDForCache), size, metrics)
	return scale, cache, true
}

// shareCacheAndReplyTemp shares the slot's cache + font-data pages
// with the requesting shepherd and sends an OpenTemporaryFontReply.
// `idx` selects either tempFonts[idx] (when isTemp=true) or fonts[idx]
// (when isTemp=false, the permanent-first fallback). The fontID
// returned to the caller carries TempFontIDBase only when isTemp=true.
//
// Unlike shareCacheAndReply (the permanent path), this routine does
// NOT cache shared VAs in sharedVAs[] — temp slots have a short
// lifecycle (Close → release → potentially reused with different
// content) so caching mappings would tag stale data with old VAs.
func shareCacheAndReplyTemp(conn *shepherdConn, connIdx int, senderSID int, idx int32, isTemp bool) {
	_ = conn
	_ = connIdx
	var slot *fontSlot
	var fontIDToSend int32
	if isTemp {
		slot = &tempFonts[idx]
		fontIDToSend = idx | wm.TempFontIDBase
	} else {
		slot = &fonts[idx]
		fontIDToSend = idx
	}
	cache := slot.cache

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

	cacheBase := uintptr(unsafe.Pointer(&cache[0]))
	var cacheFirstVA uintptr
	for i := int32(0); i < numCachePages; i++ {
		pageVA := cacheBase + uintptr(i)*4096
		targetVA, err := sys.SharePages(senderSID, pageVA)
		if err != nil {
			rawPuts("[fontsvc] SharePages temp cache failed for page ")
			rawPutsInt(int(i))
			rawPuts("\n")
			sendOpenTempFontError(senderSID)
			return
		}
		if i == 0 {
			cacheFirstVA = targetVA
		}
	}

	numFontPages := int32((len(slot.fontData) + 4095) / 4096)
	var fontFirstVA uintptr
	if numFontPages > 0 {
		fontBase := uintptr(unsafe.Pointer(&slot.fontData[0]))
		for i := int32(0); i < numFontPages; i++ {
			pageVA := fontBase + uintptr(i)*4096
			targetVA, err := sys.SharePages(senderSID, pageVA)
			if err != nil {
				rawPuts("[fontsvc] SharePages temp font failed for page ")
				rawPutsInt(int(i))
				rawPuts("\n")
				sendOpenTempFontError(senderSID)
				return
			}
			if i == 0 {
				fontFirstVA = targetVA
			}
		}
	}

	metrics := textshape.ComputeFontMetrics(slot.face, slot.scale, fontIDToSend)
	encoded := wm.EncodeOpenTemporaryFontReply(&wm.OpenTemporaryFontReply{
		FontID:        fontIDToSend,
		NumCachePages: numCachePages,
		CacheAddr:     uint64(cacheFirstVA),
		CacheSize:     uint64(cacheUsed),
		Height:        metrics.Height,
		Ascent:        metrics.Ascent,
		Descent:       metrics.Descent,
		NumFontPages:  numFontPages,
		FontAddr:      uint64(fontFirstVA),
		FontSize:      uint64(len(slot.fontData)),
	})
	if err := uring.Send(senderSID, &encoded); err != nil {
		rawPuts("[fontsvc] uring.Send OpenTemporaryFontReply FAILED: ")
		rawPuts(err.Error())
		rawPuts("\n")
	}
}

// handleOpenTemporaryFont allocates a slot in the temp pool, parses the
// face from caller-shared bytes (or falls back to filesystem if the
// caller passed FontDataVA=0 and the family resolves), builds the
// type-I glyph cache, and shares it back to the caller. Returns
// fontID with wm.TempFontIDBase OR'd in. Permanent-first: if the
// requested (family, variant, size) is already loaded in the
// permanent pool, returns that fontID with NO temp bit and no new
// allocation. Caller's CloseTemporaryFont is a no-op for those.
func handleOpenTemporaryFont(senderSID int, msg *wm.OpenTemporaryFont) {
	family := cstring(msg.Path[:])

	// Permanent-first: dedupe against existing permanent slots.
	if fontID := findCachedFont(family, msg.Variant, msg.Size); fontID >= 0 {
		conn, connIdx := getOrCreateConn(senderSID)
		if conn == nil {
			sendOpenTempFontError(senderSID)
			return
		}
		shareCacheAndReplyTemp(conn, connIdx, senderSID, fontID, false)
		return
	}

	// Temp-pool dedupe: same (family, variant, size) opened twice in one
	// render scope reuses the slot. The caller is expected to close it
	// the same number of times it opened — fontsvc-side refcount on
	// temp slots is left as a future addition; for now we trust the
	// renderer's per-render scope discipline (defer-close-all).
	if idx := findCachedTempFont(family, msg.Variant, msg.Size); idx >= 0 {
		conn, connIdx := getOrCreateConn(senderSID)
		if conn == nil {
			sendOpenTempFontError(senderSID)
			return
		}
		// Share cache pages back (already populated). Mark as temp on
		// the wire by OR'ing TempFontIDBase.
		shareCacheAndReplyTemp(conn, connIdx, senderSID, idx, true)
		return
	}

	// Fresh temp open. Two sources of bytes:
	//   1. registeredBytes — the caller previously sent
	//      wm.RegisterFontBuffer with @font-face data; bytes are
	//      already mapped at a known fontsvc-side VA. This is the
	//      common case for HTML rendering.
	//   2. filesystem — the family resolves through the font index
	//      to a /fonts/ path; load via fc.ReadFile.
	//
	// msg.FontDataVA is reserved for an inline shared-bytes path
	// (caller passing bytes per-open) — currently unused. Most
	// callers set FontDataVA=0 and rely on registration or
	// filesystem resolution.
	idx := allocTempFontID()
	if idx < 0 {
		rawPuts("[fontsvc] OpenTemporaryFont: temp pool full\n")
		sendOpenTempFontError(senderSID)
		return
	}

	var face *goFont.Face
	var fontData []byte
	if regIdx := findRegisteredBytes(family, msg.Variant); regIdx >= 0 {
		// Registered-bytes path — parse Face from the pre-mapped pages.
		reg := &registeredBytes[regIdx]
		fontData = unsafe.Slice((*byte)(unsafe.Pointer(reg.fontDataVA)), int(reg.fontDataLen))
		var parseErr bool
		face, parseErr = parseFaceFromBytes(fontData)
		if parseErr {
			sendOpenTempFontError(senderSID)
			return
		}
	} else if msg.FontDataVA != 0 {
		rawPuts("[fontsvc] OpenTemporaryFont with inline shared bytes not yet wired — returning ENOSYS\n")
		sendOpenTempFontError(senderSID)
		return
	} else {
		// Filesystem path: resolve family → /fonts/ path, load + parse.
		path, ok := resolveFamilyPath(family, msg.Variant, msg.Size)
		if !ok {
			sendOpenTempFontError(senderSID)
			return
		}
		var parseErr bool
		face, fontData, parseErr = loadAndParseFont(path)
		if parseErr {
			sendOpenTempFontError(senderSID)
			return
		}
	}

	scale, cache, ok := buildTempCache(face, fontData, msg.Size, idx)
	if !ok {
		sendOpenTempFontError(senderSID)
		return
	}

	tempFonts[idx] = fontSlot{
		inUse:    true,
		path:     family,
		variant:  msg.Variant,
		size:     msg.Size,
		cache:    cache,
		face:     face,
		fontData: fontData,
		scale:    scale,
	}
	tempFontOwner[idx] = int16(senderSID)

	conn, connIdx := getOrCreateConn(senderSID)
	if conn == nil {
		// Roll back the allocation.
		tempFonts[idx] = fontSlot{}
		tempFontOwner[idx] = -1
		sendOpenTempFontError(senderSID)
		return
	}
	shareCacheAndReplyTemp(conn, connIdx, senderSID, idx, true)
}

// parseFaceFromBytes parses a TTF/OTF byte buffer into a goFont.Face.
// Returns parseErr=true on failure (already logged). Used by the
// registered-bytes path in handleOpenTemporaryFont — extracted so it
// can be shared with future shared-bytes paths without duplicating
// the bytes.NewReader / ParseTTF dance.
func parseFaceFromBytes(data []byte) (*goFont.Face, bool) {
	face, err := goFont.ParseTTF(bytes.NewReader(data))
	if err != nil {
		rawPuts("[fontsvc] ParseTTF from registered bytes failed: " + err.Error() + "\n")
		return nil, true
	}
	return face, false
}

// handleCloseTemporaryFont releases a temp slot, frees its cache pages,
// and unmaps any shared font-data pages that the caller had handed in
// (when the bytes-path is wired). Permanent-range fontIDs are accepted
// as a no-op for caller convenience.
func handleCloseTemporaryFont(senderSID int, msg *wm.CloseTemporaryFont) {
	fontID := msg.FontID

	// Permanent-range fontID — no-op success.
	if fontID >= 0 && fontID < fontcache.MaxFonts {
		sendCloseTempFontReply(senderSID, fontID, 0)
		return
	}

	if (fontID & wm.TempFontIDBase) == 0 {
		sendCloseTempFontReply(senderSID, fontID, -22) // EINVAL
		return
	}
	idx := fontID & wm.TempFontIDMask
	if idx < 0 || idx >= MaxTempFonts || !tempFonts[idx].inUse {
		sendCloseTempFontReply(senderSID, fontID, -3) // ESRCH
		return
	}
	if tempFontOwner[idx] != int16(senderSID) {
		// Cross-shepherd close attempt — refuse. Death cleanup is the
		// only legitimate cross-shepherd path and it goes through
		// CleanupShepherdFonts directly.
		sendCloseTempFontReply(senderSID, fontID, -1) // EPERM
		return
	}

	rawPuts("[fontsvc:close] preRelease idx=")
	rawPutsInt(int(idx))
	rawPuts("\n")
	releaseTempSlot(idx)
	rawPuts("[fontsvc:close] postRelease idx=")
	rawPutsInt(int(idx))
	rawPuts("\n")
	sendCloseTempFontReply(senderSID, fontID, 0)
	rawPuts("[fontsvc:close] postReply idx=")
	rawPutsInt(int(idx))
	rawPuts("\n")
}

// handleRegisterFontBuffer accepts a (family, variant) → caller-shared
// byte buffer mapping. The caller has SharePagesWithTarget'd the
// pages into fontsvc's address space at FontDataVA; we just record
// the mapping in registeredBytes for later OpenTemporaryFont
// resolution. Bytes parse on first OpenTemporaryFont and stay mapped
// across many open/close cycles.
func handleRegisterFontBuffer(senderSID int, msg *wm.RegisterFontBuffer) {
	family := cstring(msg.Path[:])
	if family == "" {
		sendRegFontBufferReply(senderSID, -22) // EINVAL
		return
	}
	if msg.FontDataVA == 0 || msg.FontDataLen == 0 {
		sendRegFontBufferReply(senderSID, -22) // EINVAL
		return
	}
	if findRegisteredBytes(family, msg.Variant) >= 0 {
		sendRegFontBufferReply(senderSID, -17) // EEXIST
		return
	}
	slot := allocRegisteredBytesSlot()
	if slot < 0 {
		rawPuts("[fontsvc] RegisterFontBuffer: registry full\n")
		sendRegFontBufferReply(senderSID, -12) // ENOMEM
		return
	}
	registeredBytes[slot] = registeredFontBytes{
		inUse:       true,
		family:      family,
		variant:     msg.Variant,
		fontDataVA:  uintptr(msg.FontDataVA),
		fontDataLen: int64(msg.FontDataLen),
		numPages:    msg.NumPages,
		ownerSID:    int16(senderSID),
	}
	sendRegFontBufferReply(senderSID, 0)
}

// handleUnregisterFontBuffer drops a previously-registered (family,
// variant) entry. The caller is responsible for FreePages on its
// side after receiving the reply; fontsvc just clears its
// bookkeeping. Live temp slots holding a Face parsed from these
// bytes will have a dangling pointer afterward — louis14's pattern
// is register-at-page-load / unregister-never (or only after all
// renders complete), so this constraint isn't currently exercised.
// If a refcounted-pinning model is needed later it goes here.
func handleUnregisterFontBuffer(senderSID int, msg *wm.UnregisterFontBuffer) {
	family := cstring(msg.Path[:])
	slot := findRegisteredBytes(family, msg.Variant)
	if slot < 0 {
		sendUnregFontBufferReply(senderSID, -2) // ENOENT
		return
	}
	registeredBytes[slot] = registeredFontBytes{}
	sendUnregFontBufferReply(senderSID, 0)
}

// sendRegFontBufferReply replies to a RegisterFontBuffer request.
func sendRegFontBufferReply(senderSID int, errCode int32) {
	encoded := wm.EncodeRegisterFontBufferReply(&wm.RegisterFontBufferReply{
		ErrCode: errCode,
	})
	if err := uring.Send(senderSID, &encoded); err != nil {
		rawPuts("[fontsvc] uring.Send RegisterFontBufferReply failed: ")
		rawPuts(err.Error())
		rawPuts("\n")
	}
}

// sendUnregFontBufferReply replies to an UnregisterFontBuffer request.
func sendUnregFontBufferReply(senderSID int, errCode int32) {
	encoded := wm.EncodeUnregisterFontBufferReply(&wm.UnregisterFontBufferReply{
		ErrCode: errCode,
	})
	if err := uring.Send(senderSID, &encoded); err != nil {
		rawPuts("[fontsvc] uring.Send UnregisterFontBufferReply failed: ")
		rawPuts(err.Error())
		rawPuts("\n")
	}
}

// CleanupShepherdFonts releases every temp slot owned by deadSID. Called
// by the host (rachel) when a shepherd dies. Mirrors linux's
// orphanHandles cleanup pattern from diversion #6: handles that survive
// a close-without-clean-shutdown get released here.
//
// Exported so rachel can call it from handleShepherdDeath. Wiring is
// deferred until the user confirms the rachel-side hook; see
// design/louis14_temp_fonts_plan.md.
func CleanupShepherdFonts(deadSID int) {
	for i := int32(0); i < MaxTempFonts; i++ {
		if tempFonts[i].inUse && tempFontOwner[i] == int16(deadSID) {
			releaseTempSlot(i)
		}
	}
	for i := int32(0); i < MaxRegisteredFonts; i++ {
		if registeredBytes[i].inUse && registeredBytes[i].ownerSID == int16(deadSID) {
			registeredBytes[i] = registeredFontBytes{}
		}
	}
}

// releaseTempSlot frees the cache pages and clears the slot. Caller has
// already validated the index.
//
// Cache pages: allocated by buildTempCache via mem.AllocPagesSlice as a
// fixed maxCachePages (= MaxCacheSize/4096) block; BuildGlyphCacheInto
// returns a sub-slice via [:n], which preserves the original cap. We
// free cap()/4096 pages from the slice base. mem.FreePages decrements
// each page's RefCount; if the caller still has the cache mapped (i.e.
// it hasn't yet called CloseTemporaryFont on its side), the page stays
// alive at RefCount=1 until the caller's munmap drops it to 0.
//
// fontData: ownership is path-dependent and not currently tracked
// per-slot:
//   - registeredBytes path (handleOpenTemporaryFont line ~782): the
//     pages were SharePages'd to fontsvc by the caller via
//     RegisterFontBuffer; fontsvc holds a reshared mapping, not
//     ownership. Freeing here would underflow the kernel RefCount.
//   - filesystem path (handleOpenTemporaryFont line ~798): fontsvc
//     loaded the bytes via fc.ReadFile and retains them.
//
// We don't free fontData here because we don't track which path was
// used. Registered-bytes is the dominant case for HTML rendering, so
// the residual leak is small (~100-400KB per filesystem-only temp
// font, of which there are few). TODO: track per-slot ownership and
// free filesystem-loaded fontData here.
func releaseTempSlot(idx int32) {
	slot := &tempFonts[idx]
	if len(slot.cache) > 0 {
		base := unsafe.Pointer(&slot.cache[0])
		numPages := (cap(slot.cache) + 4095) / 4096
		// Bug B family instrumentation: log each release so we can
		// correlate with kernel `[munmap:FREED]` and provider
		// `[provider:close]` lines.
		rawPuts("[fontsvc:release] idx=")
		rawPutsInt(int(idx))
		rawPuts(" srvID=")
		rawPutsInt(int(idx | wm.TempFontIDBase))
		rawPuts(" cacheVA=")
		rawPutsHex(uint64(uintptr(base)))
		rawPuts(" cachePages=")
		rawPutsInt(numPages)
		rawPuts("\n")
		if err := mem.FreePages(base, numPages); err != nil {
			rawPuts("[fontsvc] releaseTempSlot: FreePages cache failed: " + err.Error() + "\n")
		}
	}
	tempFonts[idx] = fontSlot{}
	tempFontOwner[idx] = -1
}

// sendOpenTempFontError replies with a sentinel fontID indicating failure.
func sendOpenTempFontError(senderSID int) {
	encoded := wm.EncodeOpenTemporaryFontReply(&wm.OpenTemporaryFontReply{FontID: -1})
	if err := uring.Send(senderSID, &encoded); err != nil {
		rawPuts("[fontsvc] uring.Send OpenTemporaryFontReply error path failed: ")
		rawPuts(err.Error())
		rawPuts("\n")
	}
}

// sendCloseTempFontReply replies to a CloseTemporaryFont request with
// the given errno (0 on success).
func sendCloseTempFontReply(senderSID int, fontID, errCode int32) {
	encoded := wm.EncodeCloseTemporaryFontReply(&wm.CloseTemporaryFontReply{
		FontID:  fontID,
		ErrCode: errCode,
	})
	rawPuts("[fontsvc:close] preSend senderSID=")
	rawPutsInt(senderSID)
	rawPuts(" fontID=")
	rawPutsInt(int(fontID))
	rawPuts("\n")
	if err := uring.Send(senderSID, &encoded); err != nil {
		rawPuts("[fontsvc] uring.Send CloseTemporaryFontReply failed: ")
		rawPuts(err.Error())
		rawPuts("\n")
	}
	rawPuts("[fontsvc:close] postSend senderSID=")
	rawPutsInt(senderSID)
	rawPuts(" fontID=")
	rawPutsInt(int(fontID))
	rawPuts("\n")
}

func handleRequestGlyph(senderSID int, msg *wm.RequestGlyph) {
	conn, _ := shepherds.get(senderSID)
	if conn == nil {
		rawPuts("[fontsvc] RequestGlyph from unknown shepherd\n")
		return
	}

	fontID := msg.FontID
	slot := resolveFontID(fontID)
	if slot == nil {
		rawPuts("[fontsvc] RequestGlyph: invalid fontID\n")
		return
	}

	// Resolve GID: prefer msg.GID, fall back to codepoint→GID via NominalGlyph.
	gid := goFont.GID(msg.GID)
	if msg.GID == 0 && msg.Codepoint != 0 {
		g, ok := slot.face.NominalGlyph(rune(msg.Codepoint))
		if !ok {
			encoded := wm.EncodeGlyphReply(&wm.GlyphReply{
				FontID:    fontID,
				GID:       msg.GID,
				GlyphSize: 0,
			})
			uring.Send(senderSID, &encoded)
			return
		}
		gid = g
	}

	// Render the glyph using textshape.RenderGlyph.
	info, alpha := textshape.RenderGlyph(slot.face, gid, slot.scale)
	if info == nil {
		encoded := wm.EncodeGlyphReply(&wm.GlyphReply{
			FontID:    fontID,
			GID:       int32(gid),
			GlyphSize: 0,
		})
		uring.Send(senderSID, &encoded)
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
		targetVA, err := sys.SharePages(senderSID, scratchBase)
		if err != nil {
			rawPuts("[fontsvc] scratch SharePages failed\n")
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
	encoded := wm.EncodeGlyphReply(&wm.GlyphReply{
		FontID:      fontID,
		GID:         int32(gid),
		ScratchAddr: uint64(conn.scratchVA),
		GlyphSize:   totalSize,
	})
	uring.Send(senderSID, &encoded)
}

func sendOpenFontError(senderSID int) {
	encoded := wm.EncodeOpenFontReply(&wm.OpenFontReply{FontID: -1})
	uring.Send(senderSID, &encoded)
}

func getOrCreateConn(sid int) (*shepherdConn, int) {
	if conn, idx := shepherds.get(sid); conn != nil {
		return conn, idx
	}
	conn := &shepherdConn{}
	shepherds.put(sid, conn)
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

// rawPutsHex writes n as lowercase hex (no "0x" prefix). Used for
// correlating fontsvc-side logs with kernel `[munmap:FREED]` lines.
func rawPutsHex(n uint64) {
	if n == 0 {
		rawPuts("0")
		return
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		d := byte(n & 0xF)
		if d < 10 {
			buf[i] = '0' + d
		} else {
			buf[i] = 'a' + d - 10
		}
		n >>= 4
	}
	rawPuts(string(buf[i:]))
}

// --- Internal font callbacks for in-process use by rachel ---

// internalOpenFont is a plain function callback for rachel's in-process
// font opening. Same code path as handleOpenFont but returns results
// directly — no SharePages, no uring send.
func internalOpenFont(family string, variant, size int32) (fontcache.InternalOpenFontResult, bool) {
	fontID := loadOrCacheFont(family, variant, size)
	if fontID < 0 {
		return fontcache.InternalOpenFontResult{}, false
	}

	slot := &fonts[fontID]
	metrics := textshape.ComputeFontMetrics(slot.face, slot.scale, fontID)

	return fontcache.InternalOpenFontResult{
		FontID:   fontID,
		Height:   metrics.Height,
		Ascent:   metrics.Ascent,
		Descent:  metrics.Descent,
		Cache:    slot.cache,
		FontData: slot.fontData,
	}, true
}

// internalGlyphByGID is a plain function callback for rachel's in-process
// glyph rendering. Same code path as handleRequestGlyph but returns
// the glyph data directly.
func internalGlyphByGID(fontID int32, gid uint32) (fontcache.InternalGlyphResult, bool) {
	slot := resolveFontID(fontID)
	if slot == nil {
		return fontcache.InternalGlyphResult{}, false
	}
	info, alpha := textshape.RenderGlyph(slot.face, goFont.GID(gid), slot.scale)
	if info == nil {
		return fontcache.InternalGlyphResult{}, true // ok but no renderable outline
	}

	// Copy alpha so the caller owns the data.
	alphaCopy := make([]byte, len(alpha))
	copy(alphaCopy, alpha)

	return fontcache.InternalGlyphResult{
		Info: fontcache.GlyphEntry{
			Advance: info.Advance,
			DrMinX:  info.DrMinX,
			DrMinY:  info.DrMinY,
			DrMaxX:  info.DrMaxX,
			DrMaxY:  info.DrMaxY,
			Width:   info.Width,
			Height:  info.Height,
		},
		Alpha: alphaCopy,
	}, true
}

func main() {
	MazarinMain()
}
