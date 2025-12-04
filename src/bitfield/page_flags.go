package bitfield

// PageFlags represents the flags for a memory page.
// Fields are packed into a 32-bit word using bitfield tags.
type PageFlags struct {
	// Allocated indicates if the page is currently allocated
	Allocated bool `bitfield:",1"`

	// KernelPage indicates if this is a kernel page (not available for user allocation)
	KernelPage bool `bitfield:",1"`

	// Reserved bits for future use (30 bits)
	Reserved uint32 `bitfield:",30"`
}

