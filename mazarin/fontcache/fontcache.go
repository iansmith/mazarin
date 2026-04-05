package fontcache

import (
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/wm"
	_ "unsafe"

	"golang.org/x/image/font"
)

// Font variant constants for the OpenFont wire protocol.
const (
	VariantRegular    int32 = 0
	VariantBold       int32 = 1
	VariantItalic     int32 = 2
	VariantBoldItalic int32 = 3
	VariantLight      int32 = 4
	VariantCondensed  int32 = 5
)

// faceKey identifies a unique font face configuration.
type faceKey struct {
	family  string
	variant int32
	size    int64
}

// FontCache is the client-side handle for communicating with fontsvc.maz.
// A shepherd creates one FontCache and uses it to open fonts and receive
// tier-2 glyph responses.
type FontCache struct {
	rachelSID int

	// ReplyCh receives typed font responses (wm.OpenFontReply, wm.GlyphReply)
	// from the uring Dispatcher. Wire this to the Dispatcher with:
	//   dispatcher.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, fc.ReplyCh)
	ReplyCh chan any

	// Client-side face cache: same path/bold/size returns the same face.
	cachedFaces [16]struct {
		key  faceKey
		face font.Face
	}
	cachedCount int
}

// New creates a FontCache that communicates with fontsvc via uring IPC.
// rachelSID is the SID of the rachel shepherd (where fontsvc runs).
func New(rachelSID int) *FontCache {
	return &FontCache{
		rachelSID: rachelSID,
		ReplyCh:   make(chan any, 4),
	}
}

// OpenFace returns a font.Face for the given family/variant/size. Cached faces
// are returned immediately without IPC. On first call for a given combination,
// sends an OpenFont request to fontsvc and blocks until the glyph cache
// is built and shared.
func (fc *FontCache) OpenFace(family string, variant int32, size int64) font.Face {
	// Check client-side cache first.
	key := faceKey{family: family, variant: variant, size: size}
	for i := 0; i < fc.cachedCount; i++ {
		if fc.cachedFaces[i].key == key {
			return fc.cachedFaces[i].face
		}
	}

	reply, err := fc.SendOpenFont(family, variant, size)
	if err != nil || reply.FontID < 0 {
		sys.UartWriteString("[fontcache] OpenFont failed\n")
		// Cache the nil result to prevent repeated blocking requests.
		if fc.cachedCount < len(fc.cachedFaces) {
			fc.cachedFaces[fc.cachedCount] = struct {
				key  faceKey
				face font.Face
			}{key: key, face: nil}
			fc.cachedCount++
		}
		return nil
	}

	face := newSharedFace(fc, reply)

	// Cache the face for future calls.
	if fc.cachedCount < len(fc.cachedFaces) {
		fc.cachedFaces[fc.cachedCount] = struct {
			key  faceKey
			face font.Face
		}{key: key, face: face}
		fc.cachedCount++
	}

	return face
}

// StyleToVariant maps a style name (from the font index CSV or
// mancini.Feature.String()) to a wire variant code.
func StyleToVariant(style string) int32 {
	switch style {
	case "Bold":
		return VariantBold
	case "Italic":
		return VariantItalic
	case "BoldItalic":
		return VariantBoldItalic
	case "Light":
		return VariantLight
	case "Condensed":
		return VariantCondensed
	default:
		return VariantRegular
	}
}

// OpenFaceByName sends a (family, style, size) request to fontsvc which
// resolves the family name to a filesystem path server-side. Clients never
// read font files or the font index from disk.
func (fc *FontCache) OpenFaceByName(family, style string, size int64) font.Face {
	sys.UartWriteString("[fontcache] OpenFaceByName " + family + "/" + style + " size=" + itoa(size) + "...\n")
	t0 := nanotime()
	face := fc.OpenFace(family, StyleToVariant(style), size)
	dt := (nanotime() - t0) / 1e6
	if face == nil {
		sys.UartWriteString("[fontcache] OpenFace RETURNED NIL after " + itoa(dt) + "ms\n")
	} else {
		sys.UartWriteString("[fontcache] OpenFace OK in " + itoa(dt) + "ms\n")
	}
	return face
}

// SendOpenFont sends an OpenFont request to fontsvc and blocks until the reply
// arrives. family is the font family name (e.g. "AtkinsonHyperlegibleMono");
// fontsvc resolves it to a filesystem path server-side.
func (fc *FontCache) SendOpenFont(family string, variant int32, size int64) (*wm.OpenFontReply, error) {
	var of wm.OpenFont
	of.Variant = variant
	of.Size = int32(size)
	copy(of.Path[:], family)

	sys.UartWriteString("[fontcache] SendOpenFont: sending via uring...\n")
	msg := wm.EncodeOpenFont(&of)
	if err := uring.Send(fc.rachelSID, &msg); err != nil {
		sys.UartWriteString("[fontcache] SendOpenFont: uring.Send FAILED: " + err.Error() + "\n")
		return nil, err
	}

	sys.UartWriteString("[fontcache] SendOpenFont: waiting on ReplyCh...\n")
	t0 := nanotime()
	raw := <-fc.ReplyCh
	dt := (nanotime() - t0) / 1e6
	sys.UartWriteString("[fontcache] SendOpenFont: reply received after " + itoa(dt) + "ms\n")
	reply, ok := raw.(wm.OpenFontReply)
	if !ok {
		sys.UartWriteString("[fontcache] SendOpenFont: unexpected msg type\n")
		return nil, nil
	}
	return &reply, nil
}

// RequestGlyphByGID sends a tier-2 glyph request by GID and blocks until
// fontsvc renders it. Returns the reply message. The caller must copy
// the glyph data from the scratch page immediately.
func (fc *FontCache) RequestGlyphByGID(fontID int32, gid uint32) *wm.GlyphReply {
	var rg wm.RequestGlyph
	rg.FontID = fontID
	rg.GID = int32(gid)

	msg := wm.EncodeRequestGlyph(&rg)
	if err := uring.Send(fc.rachelSID, &msg); err != nil {
		return nil
	}

	raw := <-fc.ReplyCh
	reply, ok := raw.(wm.GlyphReply)
	if !ok {
		return nil
	}
	return &reply
}

// requestGlyphByCodepoint sends a tier-2 glyph request by codepoint
// (used by the legacy sharedFace path). fontsvc converts codepoint→GID internally.
func (fc *FontCache) requestGlyphByCodepoint(fontID int32, cp rune) *wm.GlyphReply {
	var rg wm.RequestGlyph
	rg.FontID = fontID
	rg.GID = 0 // 0 signals "use Codepoint"
	rg.Codepoint = int32(cp)

	msg := wm.EncodeRequestGlyph(&rg)
	if err := uring.Send(fc.rachelSID, &msg); err != nil {
		return nil
	}

	raw := <-fc.ReplyCh
	reply, ok := raw.(wm.GlyphReply)
	if !ok {
		return nil
	}
	return &reply
}

//go:linkname nanotime runtime.nanotime
func nanotime() int64

// itoa converts an int64 to a decimal string without importing strconv.
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
