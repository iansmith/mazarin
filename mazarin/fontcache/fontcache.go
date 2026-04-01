package fontcache

import (
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/sys"
	"mazzy/shared/wm"
	"unsafe"

	"golang.org/x/image/font"
)

// faceKey identifies a unique font face configuration.
type faceKey struct {
	path string
	bold bool
	size int64
}

// FontCache is the client-side handle for communicating with fontsvc.maz.
// A shepherd creates one FontCache and uses it to open fonts and receive
// tier-2 glyph responses.
type FontCache struct {
	rachelSID  int
	requestRb  *ringbuf.RingBuffer // shepherd → fontsvc
	responseRb *ringbuf.RingBuffer // fontsvc → shepherd (set on first response)

	// replyCh carries font responses from the mailbox loop goroutine
	// to the goroutine blocked in OpenFace or requestGlyph.
	replyCh chan [wm.SizeWMMessage]byte

	// Client-side face cache: same path/bold/size returns the same face.
	cachedFaces [16]struct {
		key  faceKey
		face font.Face
	}
	cachedCount int

	// fontIndex is loaded lazily on first OpenFaceByName call.
	fontIndex       *FontIndex
	fontIndexLoaded bool // true after first load attempt (even if it failed)
}

// New creates a FontCache that communicates with fontsvc via rachel's mailbox.
// Must be called after rachel is ready and before the shepherd's mailbox loop starts.
func New(rachelSID int) *FontCache {
	rb, err := ringbuf.New(rachelSID, 0, wm.SizeWMMessage, wm.DefaultSlotCount)
	if err != nil {
		panic("[fontcache] ringbuf.New failed: " + err.Error())
	}
	return &FontCache{
		rachelSID: rachelSID,
		requestRb: rb,
		replyCh:   make(chan [wm.SizeWMMessage]byte, 4),
	}
}

// OpenFace returns a font.Face for the given path/bold/size. Cached faces
// are returned immediately without IPC. On first call for a given combination,
// sends an OpenFont request to fontsvc and blocks until the glyph cache
// is built and shared.
func (fc *FontCache) OpenFace(path string, bold bool, size int64) font.Face {
	// Check client-side cache first.
	key := faceKey{path: path, bold: bold, size: size}
	for i := 0; i < fc.cachedCount; i++ {
		if fc.cachedFaces[i].key == key {
			return fc.cachedFaces[i].face
		}
	}

	reply, err := fc.SendOpenFont(path, bold, size)
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

// OpenFaceByName resolves a logical (family, style) to a font path via
// the font index (/fonts/fonts.csv) and opens the face. The index is
// loaded lazily on first call.
func (fc *FontCache) OpenFaceByName(family, style string, size int64) font.Face {
	if !fc.fontIndexLoaded {
		fc.fontIndexLoaded = true
		sys.UartWriteString("[fontcache] loading font index /fonts/fonts.csv...\n")
		t0 := nanotime()
		idx, err := LoadFontIndex("/fonts/fonts.csv")
		dt := (nanotime() - t0) / 1e6
		if err != nil {
			sys.UartWriteString("[fontcache] font index FAILED: " + err.Error() + "\n")
			panic("[fontcache] failed to load font index: " + err.Error())
		}
		sys.UartWriteString("[fontcache] font index loaded in " + itoa(dt) + "ms\n")
		fc.fontIndex = idx
	}
	path := fc.fontIndex.Resolve(family, style)
	if path == "" {
		panic("[fontcache] font not in index: " + family + "/" + style)
	}
	sys.UartWriteString("[fontcache] OpenFace " + path + " size=" + itoa(size) + "...\n")
	t0 := nanotime()
	face := fc.OpenFace(path, IsBoldStyle(style), size)
	dt := (nanotime() - t0) / 1e6
	if face == nil {
		sys.UartWriteString("[fontcache] OpenFace RETURNED NIL after " + itoa(dt) + "ms\n")
	} else {
		sys.UartWriteString("[fontcache] OpenFace OK in " + itoa(dt) + "ms\n")
	}
	return face
}

// SendOpenFont sends an OpenFont request to fontsvc and blocks until the reply
// arrives. Returns the reply message containing cache and font file page info.
func (fc *FontCache) SendOpenFont(path string, bold bool, size int64) (*wm.OpenFontReplyMsg, error) {
	var msg wm.OpenFontMsg
	msg.Type = wm.MsgOpenFont
	if bold {
		msg.Variant = 1
	}
	msg.Size = int32(size)
	copy(msg.Path[:], path)

	sys.UartWriteString("[fontcache] SendOpenFont: pushing request...\n")
	fc.requestRb.Push(unsafe.Pointer(&msg))
	sys.UartWriteString("[fontcache] SendOpenFont: MailboxSend to rachel...\n")
	if err := sys.MailboxSend(fc.rachelSID, wm.FontNotify, fc.requestRb.Addr()); err != nil {
		sys.UartWriteString("[fontcache] SendOpenFont: MailboxSend FAILED: " + err.Error() + "\n")
		return nil, err
	}

	sys.UartWriteString("[fontcache] SendOpenFont: waiting on replyCh...\n")
	t0 := nanotime()
	raw := <-fc.replyCh
	dt := (nanotime() - t0) / 1e6
	sys.UartWriteString("[fontcache] SendOpenFont: reply received after " + itoa(dt) + "ms\n")
	msgType := *(*int64)(unsafe.Pointer(&raw[0]))
	if msgType != wm.MsgOpenFontReply {
		sys.UartWriteString("[fontcache] SendOpenFont: unexpected msg type\n")
		return nil, nil
	}
	reply := (*wm.OpenFontReplyMsg)(unsafe.Pointer(&raw[0]))
	return reply, nil
}

// HandleNotification is called by the shepherd's mailbox loop when a
// FontResponse notification arrives. It reads all pending messages from
// the response ringbuf and forwards them to the blocked OpenFace/requestGlyph call.
func (fc *FontCache) HandleNotification(notif sys.MailboxNotification) {
	rb := ringbuf.Open(uintptr(notif.RingAddr))
	if fc.responseRb == nil {
		fc.responseRb = rb
	}
	var raw [wm.SizeWMMessage]byte
	count := 0
	for rb.Pop(unsafe.Pointer(&raw[0])) {
		count++
		fc.replyCh <- raw
	}
	sys.UartWriteString("[fontcache] HandleNotification: popped " + itoa(int64(count)) + " msgs\n")
}

// RequestGlyphByGID sends a tier-2 glyph request by GID and blocks until
// fontsvc renders it. Returns the raw reply message. The caller must copy
// the glyph data from the scratch page immediately.
func (fc *FontCache) RequestGlyphByGID(fontID int32, gid uint32) *wm.GlyphReplyMsg {
	var msg wm.RequestGlyphMsg
	msg.Type = wm.MsgRequestGlyph
	msg.FontID = fontID
	msg.GID = int32(gid)

	fc.requestRb.Push(unsafe.Pointer(&msg))
	if err := sys.MailboxSend(fc.rachelSID, wm.FontNotify, fc.requestRb.Addr()); err != nil {
		return nil
	}

	raw := <-fc.replyCh
	msgType := *(*int64)(unsafe.Pointer(&raw[0]))
	if msgType != wm.MsgGlyphReply {
		return nil
	}
	reply := (*wm.GlyphReplyMsg)(unsafe.Pointer(&raw[0]))
	return reply
}

// requestGlyphByCodepoint sends a tier-2 glyph request by codepoint
// (used by the legacy sharedFace path). fontsvc converts codepoint→GID internally.
func (fc *FontCache) requestGlyphByCodepoint(fontID int32, cp rune) *wm.GlyphReplyMsg {
	var msg wm.RequestGlyphMsg
	msg.Type = wm.MsgRequestGlyph
	msg.FontID = fontID
	msg.GID = 0 // 0 signals "use Codepoint"
	msg.Codepoint = int32(cp)

	fc.requestRb.Push(unsafe.Pointer(&msg))
	if err := sys.MailboxSend(fc.rachelSID, wm.FontNotify, fc.requestRb.Addr()); err != nil {
		return nil
	}

	raw := <-fc.replyCh
	msgType := *(*int64)(unsafe.Pointer(&raw[0]))
	if msgType != wm.MsgGlyphReply {
		return nil
	}
	reply := (*wm.GlyphReplyMsg)(unsafe.Pointer(&raw[0]))
	return reply
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
