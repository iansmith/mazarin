module github.com/fogleman/gg

go 1.25.5

require (
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0
	golang.org/x/image v0.23.0
	mazarin/textshape v0.0.0-00010101000000-000000000000
)

require github.com/go-text/typesetting v0.3.4 // indirect

replace mazarin/textshape => ../../mazarin/textshape
