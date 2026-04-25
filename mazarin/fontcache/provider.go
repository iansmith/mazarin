package fontcache

import (
	"bytes"
	"errors"
	"fmt"
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

// FontSvcGlyphProvider implements textshape.GlyphProvider using IPC to
// fontsvc.maz for font loading and glyph cache access. Cache and font
// file pages are shared read-only via SharePages.
type FontSvcGlyphProvider struct {
	fc         *FontCache
	fonts      [MaxFonts]*fontsvcFont
	regMu      sync.Mutex
	registered map[regKey]*registeredFace
	nextRegID  int32 // next FontID to assign for buffer-registered fonts
}

type fontsvcFont struct {
	face       *goFont.Face // parsed from shared font file pages, or registered buffer
	fontData   []byte       // shared font file pages (unsafe.Slice), or registered buffer
	cache      []byte       // shared V2 cache pages (unsafe.Slice); nil for registered
	tier2      map[uint32]*tier2Glyph
	metrics    textshape.FontMetrics
	family     string // logical font family name
	variant    int32
	size       int32
	registered bool    // true if opened via RegisterBuffer (in-process, no IPC)
	scale      float32 // pointSize / upem, for in-process RenderGlyph
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
// (e.g. CSS @font-face), OpenFont skips fontsvc entirely and serves the
// face in-process. RenderGlyph runs locally on tier-2 misses, with no
// shared cache pages.
func (p *FontSvcGlyphProvider) OpenFont(req textshape.OpenFontRequest) (textshape.FontMetrics, error) {
	// Check if already opened with matching family/variant/size.
	for i := int32(0); i < MaxFonts; i++ {
		if p.fonts[i] != nil && p.fonts[i].family == req.Family && p.fonts[i].variant == req.Variant && p.fonts[i].size == req.Size {
			return p.fonts[i].metrics, nil
		}
	}

	// Check registered buffer (CSS @font-face). Stays in-process.
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

	fontID := reply.FontID

	// Build slices over shared pages.
	var cache []byte
	if reply.CacheAddr != 0 && reply.CacheSize > 0 {
		cache = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(reply.CacheAddr))), int(reply.CacheSize))
	}

	var fontData []byte
	if reply.FontAddr != 0 && reply.FontSize > 0 {
		fontData = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(reply.FontAddr))), int(reply.FontSize))
	}

	// Parse Face from shared font file pages.
	var face *goFont.Face
	if len(fontData) > 0 {
		var parseErr error
		face, parseErr = goFont.ParseTTF(bytes.NewReader(fontData))
		if parseErr != nil {
			return textshape.FontMetrics{}, fmt.Errorf("ParseTTF from shared pages: %w", parseErr)
		}
	}

	metrics := textshape.FontMetrics{
		FontID:  fontID,
		Height:  reply.Height,
		Ascent:  reply.Ascent,
		Descent: reply.Descent,
	}

	p.fonts[fontID] = &fontsvcFont{
		face:     face,
		fontData: fontData,
		cache:    cache,
		tier2:    make(map[uint32]*tier2Glyph),
		metrics:  metrics,
		family:   req.Family,
		variant:  req.Variant,
		size:     req.Size,
	}

	sys.UartWriteString("[provider] OpenFont fontID=" + strconv.Itoa(int(fontID)) +
		" cacheLen=" + strconv.Itoa(len(cache)) +
		" fontDataLen=" + strconv.Itoa(len(fontData)) +
		" face=" + fmt.Sprintf("%p", face) + "\n")

	return metrics, nil
}

// RegisterBuffer registers a parsed font buffer under (family, variant).
// Subsequent OpenFont calls for that key serve the face in-process — no
// fontsvc IPC, no shared cache pages, no protocol changes in fontsvc.maz.
// Used by CSS @font-face: caller fetches/decompresses the font, then
// hands the buffer here. data is retained by reference.
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
	p.regMu.Lock()
	defer p.regMu.Unlock()
	if p.registered == nil {
		p.registered = make(map[regKey]*registeredFace)
	}
	p.registered[regKey{family, variant}] = &registeredFace{face: face, fontData: data}
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
// tier-2 misses (mirrors DirectGlyphProvider's tier-2 path).
func (p *FontSvcGlyphProvider) openRegistered(req textshape.OpenFontRequest, reg *registeredFace) (textshape.FontMetrics, error) {
	fontID := p.allocFontID()
	if fontID < 0 {
		return textshape.FontMetrics{}, errors.New("no free font slots")
	}

	upem := float32(reg.face.Upem())
	scale := float32(req.Size) / upem
	metrics := textshape.ComputeFontMetricsWithData(reg.face, scale, fontID, reg.fontData)

	p.fonts[fontID] = &fontsvcFont{
		face:       reg.face,
		fontData:   reg.fontData,
		cache:      nil, // no shared cache for registered (in-process) fonts
		tier2:      make(map[uint32]*tier2Glyph),
		metrics:    metrics,
		family:     req.Family,
		variant:    req.Variant,
		size:       req.Size,
		registered: true,
		scale:      scale,
	}

	return metrics, nil
}

// allocFontID finds a free FontID slot in the local fonts array.
// Used for buffer-registered fonts that don't have a server-assigned ID.
func (p *FontSvcGlyphProvider) allocFontID() int32 {
	for i := int32(0); i < MaxFonts; i++ {
		if p.fonts[i] == nil {
			return i
		}
	}
	return -1
}

// OpenTemporaryFont — IPC-side stub. The full implementation will:
//   - check the permanent pool first (return that fontID if hit)
//   - if data is non-nil, allocate IPC pages, copy bytes, SharePages
//     to fontsvc, send RequestOpenTemporaryFont, get back a 0x1000|idx
//     fontID
//   - if data is nil, send RequestOpenTemporaryFont with FontDataVA=0,
//     fontsvc resolves from the registered map or filesystem
//   - track the returned fontID in the provider's local fonts table
//     under the masked index (with isTemp=true)
//
// The wire protocol (wm.OpenTemporaryFont / wm.OpenTemporaryFontReply)
// and the fontsvc temp pool are pending. Until then this returns an
// error so callers fall back to OpenFont — current renderers (which
// don't use OpenTemporaryFont yet) are unaffected.
func (p *FontSvcGlyphProvider) OpenTemporaryFont(req textshape.OpenFontRequest, data []byte) (textshape.FontMetrics, error) {
	// Permanent-first: if already open in the regular slot table, reuse.
	for i := int32(0); i < MaxFonts; i++ {
		if p.fonts[i] != nil && p.fonts[i].family == req.Family &&
			p.fonts[i].variant == req.Variant && p.fonts[i].size == req.Size {
			return p.fonts[i].metrics, nil
		}
	}
	// Pre-registered (CSS @font-face via RegisterBuffer): same in-process
	// path as today's OpenFont — no IPC, no temp slot needed.
	if reg := p.findRegistered(req.Family, req.Variant); reg != nil {
		return p.openRegistered(req, reg)
	}
	_ = data
	return textshape.FontMetrics{}, errors.New("OpenTemporaryFont: temp pool IPC not yet implemented")
}

// CloseTemporaryFont — IPC-side stub. The full implementation will:
//   - if fontID is in permanent range (0x0000-0x003F): no-op
//   - if fontID is in temp range (0x1000-0x101F): send
//     RequestCloseTemporaryFont to fontsvc, free local provider entry,
//     FreePages on the shared font-data buffer
//   - tolerate unknown / out-of-range / double-close fontIDs (return nil)
//
// Until the IPC is wired, this is a no-op. Unknown fontIDs return nil
// per the interface contract.
func (p *FontSvcGlyphProvider) CloseTemporaryFont(fontID int32) error {
	_ = fontID
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
// font file pages.
func (p *FontSvcGlyphProvider) Face(fontID int32) *goFont.Face {
	if fontID < 0 || fontID >= MaxFonts || p.fonts[fontID] == nil {
		return nil
	}
	return p.fonts[fontID].face
}

// GlyphByGID looks up a glyph by GID using a tiered strategy:
// tier-1 (binary search in shared V2 cache), tier-2 (local overflow map),
// then fontsvc IPC for on-demand rasterization.
//
// Registered (CSS @font-face) fonts skip the IPC path entirely: tier-2
// overflow + in-process [textshape.RenderGlyph], mirroring
// DirectGlyphProvider's tier-2 path.
func (p *FontSvcGlyphProvider) GlyphByGID(fontID int32, gid uint32) (*textshape.GlyphInfo, []byte, error) {
	if fontID < 0 || fontID >= MaxFonts || p.fonts[fontID] == nil {
		return nil, nil, fmt.Errorf("invalid fontID %d", fontID)
	}

	ff := p.fonts[fontID]

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

	// Tier 2 miss: request from fontsvc.
	n := glyphTier2IPC.Add(1)
	if n <= 5 {
		sys.UartWriteString("[provider] GlyphByGID: tier2 IPC fontID=" + strconv.Itoa(int(fontID)) + " gid=" + strconv.Itoa(int(gid)) + "\n")
	}
	reply := p.fc.RequestGlyphByGID(fontID, gid)
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
