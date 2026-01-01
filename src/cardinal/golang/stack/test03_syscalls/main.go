package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("Testing syscall interface")

	// Test 1: Create a file (syscall: openat with O_CREATE)
	fmt.Println("\n=== Creating file ===")
	f, err := os.Create("/tmp/test_syscall.txt")
	if err != nil {
		fmt.Printf("Error creating file: %v\n", err)
		return
	}
	fmt.Printf("Created file, fd=%v\n", f.Fd())

	// Test 2: Write to file (syscall: write)
	fmt.Println("\n=== Writing to file ===")
	message := "Hello from syscall test\n"
	n, err := f.WriteString(message)
	if err != nil {
		fmt.Printf("Error writing: %v\n", err)
		return
	}
	fmt.Printf("Wrote %d bytes\n", n)

	// Test 3: Close file (syscall: close)
	fmt.Println("\n=== Closing file ===")
	err = f.Close()
	if err != nil {
		fmt.Printf("Error closing: %v\n", err)
		return
	}
	fmt.Println("File closed successfully")

	// Test 4: Open with WRONG filename (syscall: openat - should FAIL)
	fmt.Println("\n=== Opening non-existent file ===")
	f2, err := os.Open("/tmp/this_file_does_not_exist.txt")
	if err != nil {
		fmt.Printf("Expected error opening non-existent file: %v\n", err)
		// This is expected - continue
	} else {
		fmt.Printf("ERROR: Should have failed but got fd=%v\n", f2.Fd())
		f2.Close()
	}

	// Test 5: Open the file we created (syscall: openat - should SUCCESS)
	fmt.Println("\n=== Opening created file ===")
	f3, err := os.Open("/tmp/test_syscall.txt")
	if err != nil {
		fmt.Printf("Error opening created file: %v\n", err)
		return
	}
	fmt.Printf("Opened file successfully, fd=%v\n", f3.Fd())

	// Test 6: Read from file (syscall: read)
	fmt.Println("\n=== Reading from file ===")
	buf := make([]byte, 100)
	n, err = f3.Read(buf)
	if err != nil && err.Error() != "EOF" {
		fmt.Printf("Error reading: %v\n", err)
		return
	}
	fmt.Printf("Read %d bytes: %s\n", n, string(buf[:n]))

	// Test 7: Close the file
	fmt.Println("\n=== Closing file ===")
	err = f3.Close()
	if err != nil {
		fmt.Printf("Error closing: %v\n", err)
		return
	}
	fmt.Println("File closed successfully")

	fmt.Println("\n=== All tests complete ===")
}
