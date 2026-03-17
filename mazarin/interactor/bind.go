package interactor

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm"
)

// BindContent creates the content constraint attribute for a label.
// Must be called after NewLabel and before the draw loop starts.
// The program must return a string.
func (i *Interactor) BindContent(prog *vm.Program, deps ...attr.HandleAny) {
	contentURI := i.uri("str", "content")
	i.Content = attr.ConstraintStr(contentURI, prog, deps...)
}

// SetContentValue creates a string value attribute for content (instead of
// a constraint). The caller updates it manually via i.Content.Set(s).
func (i *Interactor) SetContentValue(initial string) {
	contentURI := i.uri("str", "content")
	i.Content = attr.ValueStr(contentURI, initial)
}
