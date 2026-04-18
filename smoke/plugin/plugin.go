// Mazlink smoke-test plugin.
package main

import "fmt"

func Hello() string {
	return "hello from mazlink plugin"
}

var count int

func Bump() int {
	count++
	return count
}

func Stress(n int) string {
	return fmt.Sprintf("stressed %d times", n)
}

func main() {}
