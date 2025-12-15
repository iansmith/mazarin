package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: imageconvert <input-image> <output-binary>\n")
		fmt.Fprintf(os.Stderr, "Converts an image to binary format for kernel embedding\n")
		fmt.Fprintf(os.Stderr, "Output format:\n")
		fmt.Fprintf(os.Stderr, "  4 bytes: width (uint32 little-endian)\n")
		fmt.Fprintf(os.Stderr, "  4 bytes: height (uint32 little-endian)\n")
		fmt.Fprintf(os.Stderr, "  width*height*4 bytes: ARGB8888 pixel data\n")
	}
	flag.Parse()

	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(1)
	}

	inputPath := flag.Arg(0)
	outputPath := flag.Arg(1)

	// Open and decode image
	file, err := os.Open(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening image: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error decoding image: %v\n", err)
		os.Exit(1)
	}

	bounds := img.Bounds()
	width := uint32(bounds.Dx())
	height := uint32(bounds.Dy())

	fmt.Printf("Image size: %d x %d\n", width, height)

	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer outFile.Close()

	// Write width and height
	if err := binary.Write(outFile, binary.LittleEndian, width); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing width: %v\n", err)
		os.Exit(1)
	}
	if err := binary.Write(outFile, binary.LittleEndian, height); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing height: %v\n", err)
		os.Exit(1)
	}

	// Write pixel data in ARGB8888 format
	pixelCount := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			// Convert from 16-bit to 8-bit (divide by 257)
			r8 := uint8(r / 257)
			g8 := uint8(g / 257)
			b8 := uint8(b / 257)
			a8 := uint8(a / 257)

			// ARGB8888: [A:8][R:8][G:8][B:8] = 0xAARRGGBB
			pixel := uint32(a8)<<24 | uint32(r8)<<16 | uint32(g8)<<8 | uint32(b8)
			if err := binary.Write(outFile, binary.LittleEndian, pixel); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing pixel data: %v\n", err)
				os.Exit(1)
			}
			pixelCount++
		}
	}

	fmt.Printf("Wrote %d pixels to %s\n", pixelCount, outputPath)
	fileInfo, _ := os.Stat(outputPath)
	fmt.Printf("Output file size: %d bytes\n", fileInfo.Size())
}







