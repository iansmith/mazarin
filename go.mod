module mazzy

go 1.25.5

require github.com/fogleman/gg v1.3.0

require (
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	golang.org/x/image v0.23.0 // indirect
)

// Tool directives - these can be run with "go tool <name>"
tool (
	mazzy/cmd/build
	mazzy/cmd/compute-linker-values
	mazzy/cmd/fix-go-elf
	mazzy/cmd/gen-ast-stubs
	mazzy/cmd/gen-test-stubs
	mazzy/cmd/incbin2goasm
	mazzy/cmd/mkfat32
	mazzy/cmd/patch-entry
	mazzy/cmd/print-kmazarin-addr
	mazzy/cmd/relocate-kmazarin
	mazzy/cmd/run
	mazzy/cmd/safe-serial-read
	mazzy/cmd/stop
	mazzy/cmd/verify-linker-values
)
