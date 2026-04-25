package fontcache

import (
	"bytes"
	"errors"
	"fmt"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazarin/textshape"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"

	goFont "github.com/go-text/typesetting/font"
)

// Instrumentation counters for glyph lookup.
var (
	glyphTier1Hits  atomic.Int64
	glyphTier2Hits  atomic.Int64
	glyphTier2IPC   atomic.Int64
	glyphTier2Miss  atomic.Int64
	glyphNoCacheHit atomic.Int64
)

// slotKind tags whether a slot was allocated for a permanent or
// temporary font. Used internally by FontSvcGlyphProvider to dispatch
// between IPC and in-process paths in GlyphByGID, and to drive
// CloseTemporaryFont's behavior. Never derived from arithmetic on
// fontIDs — set explicitly when the slot is populated.
type slotKind uint8

const (
	kindUnused slotKind = iota
	kindPermanent
	kindTemporary
)

// FontSvcGlyphProvider implements textshape.GlyphProvider using IPC to
// fontsvc.maz for font loading and glyph cache access. Cache and font
// file pages are shared read-only via SharePages.
//
// The slot table sizes to textshape.MaxFonts (= the upstream array
// index ceiling for HarfBuzzShaper / HarfBuzzTextLayout / DrawContextImpl)
// so any client-side fontID we hand upstream is guaranteed to fit
// those arrays. Each slot stores its server-side fontID (whatever
// fontsvc returned — could be 0..31 for permanent, 0x1001..0x101F
// for temp, or any other tagging the server happens to use); the
// client looks it up on every IPC send rather than computing it
// from the slot index. This keeps the wire's TempFontIDBase tagging
// scheme purely server-side; client and server pool sizes can change
// independently without breaking the translation.
type FontSvcGlyphProvider struct {
	fc         *FontCache
	slots      [textshape.MaxFonts]*fontsvcFont
	regMu      sync.Mutex
	registered map[regKey]*registeredFace // local face cache for HarfBuzz shaping (RegisterBuffer'd fonts)
}

type fontsvcFont struct {
	face         *goFont.Face // parsed from shared font file pages, or RegisterBuffer'd bytes
	fontData     []byte       // shared font file pages (unsafe.Slice), or RegisterBuffer'd bytes
	cache        []byte       // shared V2 cache pages (unsafe.Slice); nil for in-process registered fonts
	tier2        map[uint32]*tier2Glyph
	metrics      textshape.FontMetrics
	family       string // logical font family name
	variant      int32
	size         int32
	registered   bool    // true if opened via RegisterBuffer in-process fallback path (no IPC)
	scale        float32 // pointSize / upem, for in-process RenderGlyph
	serverFontID int32   // fontsvc-side fontID (sent on glyph IPC / close); -1 for in-process registered
	kind         slotKind
}

type tier2Glyph struct {
	info  textshape.GlyphInfo
	alpha []byte
}

// regKey identifies a registered face by family + variant.
type regKey struct {
	family  string
	variant int32
}

// registeredFace holds a font face registered via RegisterBuffer
// (e.g. CSS @font-face) rather than fetched via fontsvc IPC.
type registeredFace struct {
	face     *goFont.Face
	fontData []byte // caller-owned buffer; provider holds reference
}

// Compile-time check that FontSvcGlyphProvider implements textshape.GlyphProvider.
var _ textshape.GlyphProvider = (*FontSvcGlyphProvider)(nil)

// NewFontSvcGlyphProvider creates a GlyphProvider backed by fontsvc IPC.
func NewFontSvcGlyphProvider(fc *FontCache) *FontSvcGlyphProvider {
	return &FontSvcGlyphProvider{fc: fc}
}

// OpenFont sends an OpenFont request to fontsvc. fontsvc builds a V2 cache,
// shares cache and font file pages, and returns metrics. The provider parses
// a local Face from the shared font file pages for HarfBuzz.
//
// If the (family, variant) was previously registered via RegisterBuffer
// (e.g. CSS @font-face) and the fontsvc-side push failed, OpenFont serves
// the face in-process via openRegistered. The IPC-success path (RegisterBuffer
// pushed bytes to fontsvc) goes through OpenTemporaryFont; OpenFont is for
// permanent-pool fonts (system fonts, mancini chrome).
func (p *FontSvcGlyphProvider) OpenFont(req textshape.OpenFontRequest) (textshape.FontMetrics, error) {
	// Check if already opened with matching family/variant/size.
	if existing := p.findCachedFont(req.Family, req.Variant, req.Size); existing != nil {
		return existing.metrics, nil
	}

	// Check registered buffer (CSS @font-face) — only relevant when the
	// fontsvc-side push failed; otherwise these fonts go through
	// OpenTemporaryFont. Kept as a fallback so single-process testing
	// (and any path where fontsvc hasn't received the bytes) still works.
	if reg := p.findRegistered(req.Family, req.Variant); reg != nil {
		return p.openRegistered(req, reg)
	}

	// Send family name to fontsvc — server resolves to filesystem path.
	reply, err := p.fc.SendOpenFont(req.Family, req.Variant, int64(req.Size))
	if err != nil {
		return textshape.FontMetrics{}, fmt.Errorf("SendOpenFont: %w", err)
	}
	if reply == nil || reply.FontID < 0 {
		return textshape.FontMetrics{}, fmt.Errorf("fontsvc: OpenFont failed for %s", req.Family)
	}

	clientID, err := p.populateSlot(reply.FontID, kindPermanent, req,
		reply.CacheAddr, reply.CacheSize,
		reply.FontAddr, reply.FontSize,
		reply.Height, reply.Ascent, reply.Descent)
	if err != nil {
		return textshape.FontMetrics{}, err
	}
	return p.slots[clientID].metrics, nil
}

// populateSlot allocates a free slot in slots[], parses Face from shared
// font file pages, and stores all the per-slot state. Returns the client
// fontID (the slot index) which is what we hand upstream — small int <
// textshape.MaxFonts so it fits in the shaper / layout / drawcontext
// fixed-size arrays. The serverFontID is preserved inside the slot for
// later IPC sends (CloseTemporaryFont, RequestGlyph).
func (p *FontSvcGlyphProvider) populateSlot(serverFontID int32, kind slotKind,
	req textshape.OpenFontRequest, cacheAddr, cacheSize, fontAddr, fontSize uint64,
	height, ascent, descent int32) (int32, error) {

	var cache []byte
	if cacheAddr != 0 && cacheSize > 0 {
		cache = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(cacheAddr))), int(cacheSize))
	}
	var fontData []byte
	if fontAddr != 0 && fontSize > 0 {
		fontData = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(fontAddr))), int(fontSize))
	}
	var face *goFont.Face
	if len(fontData) > 0 {
		var parseErr error
		face, parseErr = goFont.ParseTTF(bytes.NewReader(fontData))
		if parseErr != nil {
			return -1, fmt.Errorf("ParseTTF from shared pages: %w", parseErr)
		}
	}
	clientID := p.allocSlot()
	if clientID < 0 {
		return -1, errors.New("no free font slots")
	}
	metrics := textshape.FontMetrics{
		FontID:  clientID,
		Height:  height,
		Ascent:  ascent,
		Descent: descent,
	}
	p.slots[clientID] = &fontsvcFont{
		face:         face,
		fontData:     fontData,
		cache:        cache,
		tier2:        make(map[uint32]*tier2Glyph),
		metrics:      metrics,
		family:       req.Family,
		variant:      req.Variant,
		size:         req.Size,
		serverFontID: serverFontID,
		kind:         kind,
	}
	sys.UartWriteString("[provider] populateSlot client=" + strconv.Itoa(int(clientID)) +
		" server=" + strconv.Itoa(int(serverFontID)) +
		" kind=" + strconv.Itoa(int(kind)) +
		" cacheLen=" + strconv.Itoa(len(cache)) +
		" fontDataLen=" + strconv.Itoa(len(fontData)) + "\n")
	return clientID, nil
}

// findCachedFont scans slots[] for an open font matching (family, variant, size).
func (p *FontSvcGlyphProvider) findCachedFont(family string, variant, size int32) *fontsvcFont {
	for i := int32(0); i < int32(len(p.slots)); i++ {
		s := p.slots[i]
		if s != nil && s.family == family && s.variant == variant && s.size == size {
			return s
		}
	}
	return nil
}

// RegisterBuffer hands @font-face byte data to fontsvc and keeps a
// local Face for HarfBuzz shaping. Bytes are AllocPages'd into a
// kernel-allocated buffer, copied, then SharePagesWithTarget'd into
// fontsvc's address space; a wm.RegisterFontBuffer IPC tells fontsvc
// where the bytes live. Subsequent OpenTemporaryFont calls for this
// (family, variant) parse Face from those bytes server-side and build
// a tier-1 cache per requested size.
//
// The local Face is retained so HarfBuzz shaping (which runs in the
// shepherd) can query the font's tables without IPC. The local Face
// and the fontsvc-side mapping share the same physical bytes (one
// memcpy in, both views see the same data).
//
// On any failure (Parse, AllocPages, SharePagesWithTarget, IPC reply),
// falls back to the in-process registered map: face is parsed, stored
// locally; subsequent OpenFont/OpenTemporaryFont serve the face in-
// process via openRegistered. This keeps single-process testing and
// fontsvc-down scenarios working.
func (p *FontSvcGlyphProvider) RegisterBuffer(family string, variant int32, data []byte) error {
	if family == "" {
		return errors.New("RegisterBuffer: empty family")
	}
	if len(data) == 0 {
		return errors.New("RegisterBuffer: empty data")
	}
	face, err := goFont.ParseTTF(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("RegisterBuffer: parse font: %w", err)
	}

	// Always keep a local face for HarfBuzz shaping (and as a fallback
	// for openRegistered if the fontsvc push fails).
	p.regMu.Lock()
	if p.registered == nil {
		p.registered = make(map[regKey]*registeredFace)
	}
	p.registered[regKey{family, variant}] = &registeredFace{face: face, fontData: data}
	p.regMu.Unlock()

	// Push bytes to fontsvc. Failure is non-fatal — local face stays
	// valid and openRegistered will serve glyphs in-process.
	if err := p.pushBytesToFontsvc(family, variant, data); err != nil {
		sys.UartWriteString("[provider] RegisterBuffer: pushBytesToFontsvc failed for " +
			family + ": " + err.Error() + " — falling back to in-process\n")
		// Not returning the error: caller's RegisterBuffer succeeds at
		// the local level; OpenTemporaryFont will see the local entry
		// and use openRegistered.
	}
	return nil
}

// pushBytesToFontsvc allocates kernel pages, copies the buffer in,
// shares with fontsvc, and sends wm.RegisterFontBuffer. On success
// fontsvc holds the (family, variant) → mapping; subsequent
// OpenTemporaryFont calls for that key parse Face from the registered
// bytes on the fontsvc side and build tier-1 cache there.
func (p *FontSvcGlyphProvider) pushBytesToFontsvc(family string, variant int32, data []byte) error {
	numPages := (len(data) + 4095) / 4096
	if numPages == 0 {
		return errors.New("pushBytesToFontsvc: zero pages")
	}
	pages, err := mem.AllocPagesSlice(numPages, mem.PageFontCache)
	if err != nil {
		return fmt.Errorf("AllocPagesSlice(%d): %w", numPages, err)
	}
	copy(pages, data)
	pageVA := uintptr(unsafe.Pointer(&pages[0]))
	fontsvcVA, err := sys.SharePagesWithTarget(p.fc.rachelSID, pageVA, numPages)
	if err != nil {
		return fmt.Errorf("SharePagesWithTarget: %w", err)
	}
	reply, err := p.fc.SendRegisterFontBuffer(family, variant,
		uint64(fontsvcVA), uint64(len(data)), uint32(numPages))
	if err != nil {
		return fmt.Errorf("SendRegisterFontBuffer: %w", err)
	}
	if reply == nil || reply.ErrCode != 0 {
		ec := int32(0)
		if reply != nil {
			ec = reply.ErrCode
		}
		return fmt.Errorf("RegisterFontBuffer reply errcode=%d", ec)
	}
	return nil
}

// findRegistered returns the registered face for (family, variant), or nil.
func (p *FontSvcGlyphProvider) findRegistered(family string, variant int32) *registeredFace {
	p.regMu.Lock()
	defer p.regMu.Unlock()
	if p.registered == nil {
		return nil
	}
	return p.registered[regKey{family, variant}]
}

// openRegistered opens a font from a previously-registered buffer entirely
// in-process. No IPC, no shared cache pages. RenderGlyph runs locally on
// tier-2 misses (mirrors DirectGlyphProvider's tier-2 path). This is the
// fallback path used when the fontsvc push at RegisterBuffer time failed
// (or when running without fontsvc); the IPC-success path goes through
// OpenTemporaryFont and is served entirely by fontsvc.
func (p *FontSvcGlyphProvider) openRegistered(req textshape.OpenFontRequest, reg *registeredFace) (textshape.FontMetrics, error) {
	clientID := p.allocSlot()
	if clientID < 0 {
		return textshape.FontMetrics{}, errors.New("no free font slots")
	}
	upem := float32(reg.face.Upem())
	scale := float32(req.Size) / upem
	metrics := textshape.ComputeFontMetricsWithData(reg.face, scale, clientID, reg.fontData)
	p.slots[clientID] = &fontsvcFont{
		face:         reg.face,
		fontData:     reg.fontData,
		cache:        nil, // no shared cache for in-process registered fonts
		tier2:        make(map[uint32]*tier2Glyph),
		metrics:      metrics,
		family:       req.Family,
		variant:      req.Variant,
		size:         req.Size,
		registered:   true,
		scale:        scale,
		serverFontID: -1, // never sent over the wire
		kind:         kindPermanent,
	}
	return metrics, nil
}

// allocSlot finds a free index in slots[] (sized to textshape.MaxFonts).
// The returned index is the client-side fontID handed upstream — small
// int < textshape.MaxFonts, fits in all the upstream fixed-size arrays.
func (p *FontSvcGlyphProvider) allocSlot() int32 {
	for i := int32(0); i < int32(len(p.slots)); i++ {
		if p.slots[i] == nil {
			return i
		}
	}
	return -1
}

// OpenTemporaryFont opens a font scoped to a single render pass. The
// returned fontID MUST be paired with CloseTemporaryFont. Three resolution
// paths, in order:
//
//  1. Permanent-first dedupe: an already-open slot matches (family,
//     variant, size) → return that slot's fontID. CloseTemporaryFont
//     for a permanent-kind slot is a no-op, so callers can defer-close
//     uniformly.
//
//  2. RegisterBuffer-pushed: RegisterBuffer told fontsvc about this
//     family's bytes. Send wm.OpenTemporaryFont with FontDataVA=0;
//     fontsvc resolves via its registeredBytes map, parses Face from
//     those bytes, builds tier-1 cache for `size`, returns a server-
//     side fontID with TempFontIDBase tagged. Allocate a client slot,
//     store serverFontID, return the slot index upstream.
//
//  3. Filesystem fallback: same wm.OpenTemporaryFont IPC; fontsvc
//     resolves via its font index. Same client-side handling.
//
// The `data` parameter is reserved for an inline shared-bytes path
// (caller passes bytes per-open). louis14's flow registers via
// RegisterBuffer and passes data=nil here.
func (p *FontSvcGlyphProvider) OpenTemporaryFont(req textshape.OpenFontRequest, data []byte) (textshape.FontMetrics, error) {
	_ = data // reserved for future inline shared-bytes path

	// Permanent-first dedupe.
	if existing := p.findCachedFont(req.Family, req.Variant, req.Size); existing != nil {
		return existing.metrics, nil
	}

	// In-process registered fallback: if the fontsvc push at
	// RegisterBuffer time failed, we still have the bytes locally.
	// openRegistered handles the in-process path (no IPC, no shared
	// cache).
	if reg := p.findRegistered(req.Family, req.Variant); reg != nil {
		// Try the IPC path first — fontsvc may have the bytes if the
		// push succeeded. If IPC fails (registry miss server-side), fall
		// back to in-process. We can't tell from here whether the push
		// landed, so we just try IPC; on -ENOENT we fall through.
		if metrics, err := p.openTempViaIPC(req); err == nil {
			return metrics, nil
		}
		return p.openRegistered(req, reg)
	}

	// Filesystem path or registered-bytes path (fontsvc decides
	// internally which based on its own registeredBytes map).
	return p.openTempViaIPC(req)
}

// openTempViaIPC sends a wm.OpenTemporaryFont and populates a slot.
// Returns the metrics with FontID set to the client-side slot index.
func (p *FontSvcGlyphProvider) openTempViaIPC(req textshape.OpenFontRequest) (textshape.FontMetrics, error) {
	reply, err := p.fc.SendOpenTemporaryFont(req.Family, req.Variant, req.Size, 0, 0, 0)
	if err != nil {
		return textshape.FontMetrics{}, fmt.Errorf("SendOpenTemporaryFont: %w", err)
	}
	if reply == nil || reply.FontID < 0 {
		return textshape.FontMetrics{}, fmt.Errorf("fontsvc: OpenTemporaryFont failed for %s", req.Family)
	}
	clientID, err := p.populateSlot(reply.FontID, kindTemporary, req,
		reply.CacheAddr, reply.CacheSize,
		reply.FontAddr, reply.FontSize,
		reply.Height, reply.Ascent, reply.Descent)
	if err != nil {
		return textshape.FontMetrics{}, err
	}
	return p.slots[clientID].metrics, nil
}

// CloseTemporaryFont releases a fontID returned by OpenTemporaryFont.
// For kindTemporary slots: looks up the server-side fontID, sends
// wm.CloseTemporaryFont, drops the local slot. For kindPermanent
// slots (returned by the permanent-first dedupe path): no-op so
// callers can defer-close every fontID indiscriminately. Unknown /
// out-of-range / already-closed fontIDs return nil per the interface
// contract.
func (p *FontSvcGlyphProvider) CloseTemporaryFont(fontID int32) error {
	if fontID < 0 || int(fontID) >= len(p.slots) {
		return nil
	}
	slot := p.slots[fontID]
	if slot == nil || slot.kind != kindTemporary {
		return nil
	}
	// Send close to fontsvc using the server-side fontID. Reply
	// errcode is best-effort — even if fontsvc didn't have the slot,
	// we still drop our local entry.
	_, _ = p.fc.SendCloseTemporaryFont(slot.serverFontID)
	p.slots[fontID] = nil
	return nil
}

// DumpGlyphStats prints glyph lookup statistics.
func DumpGlyphStats() {
	sys.UartWriteString("[provider] glyph stats: tier1=" + strconv.FormatInt(glyphTier1Hits.Load(), 10) +
		" tier2=" + strconv.FormatInt(glyphTier2Hits.Load(), 10) +
		" ipc=" + strconv.FormatInt(glyphTier2IPC.Load(), 10) +
		" noCache=" + strconv.FormatInt(glyphNoCacheHit.Load(), 10) +
		" miss=" + strconv.FormatInt(glyphTier2Miss.Load(), 10) + "\n")
}

// Face returns the go-text Face for the given fontID, parsed from shared
// font file pages or RegisterBuffer'd bytes.
func (p *FontSvcGlyphProvider) Face(fontID int32) *goFont.Face {
	if fontID < 0 || int(fontID) >= len(p.slots) || p.slots[fontID] == nil {
		return nil
	}
	return p.slots[fontID].face
}

// GlyphByGID looks up a glyph by GID using a tiered strategy:
// tier-1 (binary search in shared V2 cache), tier-2 (local overflow map),
// then fontsvc IPC for on-demand rasterization. The IPC uses the slot's
// serverFontID (looked up, not derived) so the fontsvc-side temp/perm
// distinction is handled correctly without arithmetic on fontID values.
//
// In-process registered fonts (the fontsvc-push fallback path)
// skip the IPC path entirely: tier-2 overflow + in-process
// [textshape.RenderGlyph], mirroring DirectGlyphProvider's tier-2 path.
func (p *FontSvcGlyphProvider) GlyphByGID(fontID int32, gid uint32) (*textshape.GlyphInfo, []byte, error) {
	if fontID < 0 || int(fontID) >= len(p.slots) || p.slots[fontID] == nil {
		return nil, nil, fmt.Errorf("invalid fontID %d", fontID)
	}

	ff := p.slots[fontID]

	if ff.registered {
		// In-process path: tier-2 overflow only, then RenderGlyph.
		if t2, ok := ff.tier2[gid]; ok {
			return &t2.info, t2.alpha, nil
		}
		info, alpha := textshape.RenderGlyph(ff.face, goFont.GID(gid), ff.scale)
		if info == nil {
			return nil, nil, nil
		}
		ff.tier2[gid] = &tier2Glyph{info: *info, alpha: alpha}
		return info, alpha, nil
	}

	// Tier 1: binary search in shared V2 cache.
	if len(ff.cache) > 0 {
		info, alpha := textshape.LookupByGID(ff.cache, gid)
		if info != nil {
			glyphTier1Hits.Add(1)
			return info, alpha, nil
		}
	} else {
		n := glyphNoCacheHit.Add(1)
		if n <= 3 {
			sys.UartWriteString("[provider] GlyphByGID: NO CACHE for fontID=" + strconv.Itoa(int(fontID)) + " gid=" + strconv.Itoa(int(gid)) + "\n")
		}
	}

	// Tier 2: check local overflow map.
	if t2, ok := ff.tier2[gid]; ok {
		glyphTier2Hits.Add(1)
		return &t2.info, t2.alpha, nil
	}

	// Tier 2 miss: request from fontsvc using the server-side fontID.
	n := glyphTier2IPC.Add(1)
	if n <= 5 {
		sys.UartWriteString("[provider] GlyphByGID: tier2 IPC client=" + strconv.Itoa(int(fontID)) +
			" server=" + strconv.Itoa(int(ff.serverFontID)) + " gid=" + strconv.Itoa(int(gid)) + "\n")
	}
	reply := p.fc.RequestGlyphByGID(ff.serverFontID, gid)
	if reply == nil || reply.GlyphSize == 0 {
		return nil, nil, nil
	}

	// Copy glyph data from scratch page into local cache.
	scratchPtr := uintptr(reply.ScratchAddr)
	ge := (*GlyphEntry)(unsafe.Pointer(scratchPtr))
	info := textshape.GlyphInfo{
		Advance: ge.Advance,
		DrMinX:  ge.DrMinX,
		DrMinY:  ge.DrMinY,
		DrMaxX:  ge.DrMaxX,
		DrMaxY:  ge.DrMaxY,
		Width:   ge.Width,
		Height:  ge.Height,
	}
	var alpha []byte
	w := int(ge.Width)
	h := int(ge.Height)
	if w > 0 && h > 0 {
		alpha = make([]byte, w*h)
		srcAlpha := unsafe.Slice((*byte)(unsafe.Pointer(scratchPtr+GlyphEntrySize)), w*h)
		copy(alpha, srcAlpha)
	}

	ff.tier2[gid] = &tier2Glyph{info: info, alpha: alpha}
	return &ff.tier2[gid].info, alpha, nil
}
