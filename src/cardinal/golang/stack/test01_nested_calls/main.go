package main

import "fmt"

// Test program to study ARM64 calling conventions with nested function calls
// Each level takes different numbers and types of parameters to observe patterns

func main() {
	fmt.Println("Starting nested call test")

	// Call level1 with 2 parameters: string + int64
	result := level1("hello", 42)

	fmt.Printf("Final result: %d\n", result)
}

// level1: Takes string and int64, calls level2
func level1(msg string, val int64) int64 {
	fmt.Printf("level1: msg=%s, val=%d\n", msg, val)

	// Call level2 with 3 parameters: int64 + int64 + string
	// Pass val through and add 10
	result := level2(val+10, val*2, "from-level1")

	return result + 1
}

// level2: Takes int64, int64, string, calls level3
func level2(a int64, b int64, tag string) int64 {
	fmt.Printf("level2: a=%d, b=%d, tag=%s\n", a, b, tag)

	// Call level3 with 4 parameters: string + string + int64 + int64
	result := level3("first", "second", a+b, a-b)

	return result + 2
}

// level3: Takes string, string, int64, int64, calls level4
func level3(s1 string, s2 string, x int64, y int64) int64 {
	fmt.Printf("level3: s1=%s, s2=%s, x=%d, y=%d\n", s1, s2, x, y)

	// Call level4 with 6 parameters: int64 x5 + string
	// This should push some parameters to stack (more than 8 register params)
	result := level4(x, y, x+y, x-y, x*2, "final")

	return result + 3
}

// level4: Takes 5 int64s and 1 string (6 parameters total)
// With strings being 2 words (ptr+len), this is 7 register slots
// Should fit in registers (X0-X7), but let's see what Go does
func level4(a, b, c, d, e int64, msg string) int64 {
	fmt.Printf("level4: a=%d, b=%d, c=%d, d=%d, e=%d, msg=%s\n", a, b, c, d, e, msg)

	// Do some computation to prevent optimization
	sum := a + b + c + d + e

	// Use the string to prevent it being optimized away
	if len(msg) > 0 {
		sum += int64(msg[0])
	}

	return sum
}
