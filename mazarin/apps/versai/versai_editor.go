// VersaiEditor wraps a MultiLineText with command processing and
// shared-page text export for the BAG interactor.
//
// Lines starting with "/", "//", or "!" are commands and are not
// rendered to the BAG interactor. Empty lines immediately following
// a command line are also suppressed.
//
// Commands are activated when the user types the closing '\n' of the
// first logical line.
//
// Text export: the VE maintains a 4096-byte shared page. After every
// edit, the renderable text (with command lines stripped) is copied to
// the page and a textLen attribute is set. The BAG has an eager
// constraint on textLen, so it redraws automatically.
package main

import (
	"fmt"
	"strings"
	"unsafe"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/file"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	"mazzy/mazarin/mem"
)

// PageSize is the size of the shared text page.
const PageSize = 4096

// VersaiEditor extends MultiLineText with command processing.
type VersaiEditor struct {
	*std.MultiLineText

	// Shared page for text export to the BAG interactor.
	textPage     []byte
	textLenAttr  *attr.Attribute[int64]

	// OnPageUpdate is called after the shared page is updated.
	// Unit wires this to BAG.FullDamage so the vsplitter knows
	// the BAG needs redrawing.
	OnPageUpdate func()

	// FocusParent is the interactor that owns this editor. When the
	// editor is clicked, it calls FocusParent.SetFocusToSelf() before
	// delegating to MultiLineText.Click().
	FocusParent mancini.FocusClaimer
}

// NewVersaiEditor creates a VersaiEditor wrapping an existing MultiLineText.
// myName is the constraint-system name used for publishing attributes.
func NewVersaiEditor(myName string, mte *std.MultiLineText) *VersaiEditor {
	ve := &VersaiEditor{
		MultiLineText: mte,
		textPage:      make([]byte, PageSize),
	}

	// Publish the text length as a value attribute.
	lenURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutTextLen)
	ve.textLenAttr = attr.ValueI64(lenURI, 0)

	mte.AfterEdit = func() {
		ve.checkCommand()
		ve.updateSharedPage()
	}

	// Re-register so dispatch sees VersaiEditor (not MultiLineText)
	// as the interactor — VE's Click/DoubleClick/TripleClick/ClickDragStart
	// overrides get called.
	ve.ReInitializeOwner(ve)

	return ve
}

// TextLenURI returns the constraint URI for the text length attribute.
func (ve *VersaiEditor) TextLenURI() string {
	return ve.textLenAttr.URI()
}

// TextPageAddr returns the address of the shared text page.
func (ve *VersaiEditor) TextPageAddr() uintptr {
	return uintptr(unsafe.Pointer(&ve.textPage[0]))
}

// updateSharedPage copies the renderable text to the shared page and
// sets the length attribute. The length Set() triggers eager propagation
// synchronously, so the BAG's eagerCh fires before this returns.
func (ve *VersaiEditor) updateSharedPage() {
	ve.Mu.Lock()
	rt := ve.renderableTextLocked()
	ve.Mu.Unlock()

	n := len(rt)
	if n > PageSize {
		n = PageSize
	}
	copy(ve.textPage[:n], rt[:n])
	ve.textLenAttr.Set(int64(n))
	if ve.OnPageUpdate != nil {
		ve.OnPageUpdate()
	}
	fmt.Printf("[VE:page] updateSharedPage len=%d\n", n)
}

// Command returns true if the text starts with "/", "//", or "!".
func (ve *VersaiEditor) Command() bool {
	ve.Mu.Lock()
	defer ve.Mu.Unlock()
	return ve.commandLocked()
}

// commandLocked is the lock-held version of Command.
func (ve *VersaiEditor) commandLocked() bool {
	text := ve.MultiLineText.Text()
	return strings.HasPrefix(text, "/") || strings.HasPrefix(text, "!")
}

// commandPrefix returns the command prefix ("//", "/", or "!") and
// the rest of the first line (after the prefix), or empty strings if
// not a command. Caller must hold Mu.
func (ve *VersaiEditor) commandPrefix() (prefix, firstLine string) {
	text := ve.MultiLineText.Text()
	if len(text) == 0 {
		return "", ""
	}
	// Find end of first line.
	nlIdx := strings.IndexByte(text, '\n')
	if nlIdx < 0 {
		// No newline yet — command not activated.
		return "", ""
	}
	line := text[:nlIdx]
	if strings.HasPrefix(line, "//") {
		return "//", strings.TrimSpace(line[2:])
	}
	if strings.HasPrefix(line, "/") {
		return "/", strings.TrimSpace(line[1:])
	}
	if strings.HasPrefix(line, "!") {
		return "!", strings.TrimSpace(line[1:])
	}
	return "", ""
}

// CommandTextEntryLine returns the line number of the first non-empty
// line after the command and its trailing blank lines. Returns a
// negative value if Command() is false.
func (ve *VersaiEditor) CommandTextEntryLine() int {
	ve.Mu.Lock()
	defer ve.Mu.Unlock()
	return ve.commandTextEntryLineLocked()
}

func (ve *VersaiEditor) commandTextEntryLineLocked() int {
	if !ve.commandLocked() {
		return -1
	}
	text := ve.MultiLineText.Text()
	lines := strings.Split(text, "\n")
	// Line 0 is the command line. Skip it, then skip empty lines.
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return i
		}
	}
	return len(lines) - 1
}

// CommandTextEntryLineClear removes all text from the
// CommandTextEntryLine to the end of the MTE.
func (ve *VersaiEditor) CommandTextEntryLineClear() string {
	ve.Mu.Lock()
	defer ve.Mu.Unlock()
	return ve.commandTextEntryLineClearLocked()
}

// CommandTextEntryLineInsert inserts content at the beginning of the
// CommandTextEntryLine, pushing any following text forward.
func (ve *VersaiEditor) CommandTextEntryLineInsert(content string) {
	ve.Mu.Lock()
	defer ve.Mu.Unlock()
	ve.commandTextEntryLineInsertLocked(content)
}

// CommandAppend appends text to the end of the VE.
func (ve *VersaiEditor) CommandAppend(s string) {
	ve.Mu.Lock()
	defer ve.Mu.Unlock()

	if !ve.commandLocked() {
		return
	}
	text := ve.MultiLineText.Text()
	if len(text) > 0 && text[len(text)-1] != '\n' {
		text += "\n"
	}
	ve.MultiLineText.SetText(text + s)
}

// RenderableText returns the text that should be sent to the
// rendering engine (BAG). If the text starts with a command, the
// command line and any immediately following empty lines are
// stripped. Otherwise the full text is returned.
func (ve *VersaiEditor) RenderableText() string {
	ve.Mu.Lock()
	defer ve.Mu.Unlock()
	return ve.renderableTextLocked()
}

// renderableTextLocked is the lock-held version of RenderableText.
func (ve *VersaiEditor) renderableTextLocked() string {
	if !ve.commandLocked() {
		return ve.MultiLineText.Text()
	}
	text := ve.MultiLineText.Text()
	entryLine := ve.commandTextEntryLineLocked()
	if entryLine < 0 {
		return text
	}

	offset := ve.byteOffsetOfLine(text, entryLine)
	return text[offset:]
}

// checkCommand is called via AfterEdit on every text modification.
// It checks whether the first line is a completed command (ends with
// '\n') and dispatches it.
func (ve *VersaiEditor) checkCommand() {
	// Already inside AfterEdit which does NOT hold Mu.
	ve.Mu.Lock()
	if !ve.commandLocked() {
		ve.Mu.Unlock()
		return
	}
	prefix, body := ve.commandPrefix()
	ve.Mu.Unlock()

	if prefix == "" {
		return
	}

	// "//" and "!" are no-ops (comment/suppress).
	if prefix == "//" || prefix == "!" {
		return
	}

	// "/" commands: parse verb + args.
	parts := strings.Fields(body)
	if len(parts) == 0 {
		return
	}
	verb := parts[0]
	args := parts[1:]

	switch verb {
	case "rf":
		if len(args) < 1 {
			fmt.Printf("[versai] /rf: missing filename\n")
			return
		}
		replaced := ve.CommandReadFile(args[0])
		if replaced != "" {
			fmt.Printf("[versai] /rf %s: replaced %d bytes\n", args[0], len(replaced))
		}
		ve.deactivateCommand()
	default:
		fmt.Printf("[versai] unknown command: %s\n", verb)
	}
}

// deactivateCommand inserts an extra "/" at the start of the text so
// that "/rf ..." becomes "//rf ..." and won't re-trigger on the next edit.
func (ve *VersaiEditor) deactivateCommand() {
	ve.Mu.Lock()
	defer ve.Mu.Unlock()
	text := ve.MultiLineText.Text()
	if len(text) > 0 && text[0] == '/' {
		ve.MultiLineText.SetText("/" + text)
	}
}

// CommandReadFile loads a file from the filesystem and replaces the
// content area (after the command block) with the file contents.
// Returns the previously replaced text, or "" on error.
func (ve *VersaiEditor) CommandReadFile(path string) string {
	result, loadErr := file.LoadFile(path)
	if loadErr != nil {
		fmt.Printf("[versai] /rf %s: load error: %v\n", path, loadErr)
		return ""
	}
	if result.BytesRead == 0 {
		mem.Munmap(uintptr(result.StartVA), int(result.NumPages)*4096)
		return ""
	}

	data := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(result.StartVA))), result.BytesRead)
	content := string(data)
	mem.Munmap(uintptr(result.StartVA), int(result.NumPages)*4096)

	ve.Mu.Lock()
	old := ve.commandTextEntryLineClearLocked()
	ve.commandTextEntryLineInsertLocked(content)
	ve.Mu.Unlock()

	return old
}

// byteOffsetOfLine returns the byte offset of the start of the
// given 0-based line number in text.
func (ve *VersaiEditor) byteOffsetOfLine(text string, lineNum int) int {
	offset := 0
	for i := 0; i < lineNum; i++ {
		idx := strings.IndexByte(text[offset:], '\n')
		if idx < 0 {
			return len(text)
		}
		offset += idx + 1
	}
	return offset
}

// commandTextEntryLineClearLocked is the lock-held version of
// CommandTextEntryLineClear. Caller must hold Mu.
func (ve *VersaiEditor) commandTextEntryLineClearLocked() string {
	if !ve.commandLocked() {
		return ""
	}
	text := ve.MultiLineText.Text()
	entryLine := ve.commandTextEntryLineLocked()
	if entryLine < 0 {
		return ""
	}

	offset := ve.byteOffsetOfLine(text, entryLine)

	removed := text[offset:]
	prefix := text[:offset]
	ve.MultiLineText.SetText(prefix)
	return removed
}

// Click overrides MultiLineText.Click to claim in-app focus for the
// containing Unit before processing the click for cursor placement.
func (ve *VersaiEditor) Click(ev *mancini.InputEvent) bool {
	fmt.Printf("[VE] Click at (%d,%d)\n", ev.X, ev.Y)
	if ve.FocusParent != nil {
		ve.FocusParent.SetFocusToSelf()
	}
	return ve.MultiLineText.Click(ev)
}

// DoubleClick overrides MultiLineText.DoubleClick to claim focus first.
func (ve *VersaiEditor) DoubleClick(ev *mancini.InputEvent) bool {
	fmt.Printf("[VE] DoubleClick at (%d,%d)\n", ev.X, ev.Y)
	if ve.FocusParent != nil {
		ve.FocusParent.SetFocusToSelf()
	}
	return ve.MultiLineText.DoubleClick(ev)
}

// TripleClick overrides MultiLineText.TripleClick to claim focus first.
func (ve *VersaiEditor) TripleClick(ev *mancini.InputEvent) bool {
	fmt.Printf("[VE] TripleClick at (%d,%d)\n", ev.X, ev.Y)
	if ve.FocusParent != nil {
		ve.FocusParent.SetFocusToSelf()
	}
	return ve.MultiLineText.TripleClick(ev)
}

// ClickDragStart overrides MultiLineText.ClickDragStart to claim focus first.
func (ve *VersaiEditor) ClickDragStart(ev *mancini.InputEvent) bool {
	fmt.Printf("[VE] ClickDragStart at (%d,%d)\n", ev.X, ev.Y)
	if ve.FocusParent != nil {
		ve.FocusParent.SetFocusToSelf()
	}
	return ve.MultiLineText.ClickDragStart(ev)
}

// commandTextEntryLineInsertLocked is the lock-held version of
// CommandTextEntryLineInsert. Caller must hold Mu.
func (ve *VersaiEditor) commandTextEntryLineInsertLocked(content string) {
	if !ve.commandLocked() {
		return
	}
	text := ve.MultiLineText.Text()
	entryLine := ve.commandTextEntryLineLocked()
	if entryLine < 0 {
		return
	}

	offset := ve.byteOffsetOfLine(text, entryLine)
	newText := text[:offset] + content + text[offset:]
	ve.MultiLineText.SetText(newText)
}
