// Package bitfield provides functionality to pack and unpack struct fields into integers.
// This is a simplified version based on golang.org/x/text/internal/gen/bitfield
package bitfield

import (
	"fmt"
	"reflect"
)

// Config determines settings for packing and generation.
type Config struct {
	// NumBits fixes the maximum allowed bits for the integer representation.
	// If NumBits is not 8, 16, 32, or 64, the actual underlying integer size
	// will be the next largest available.
	NumBits uint

	// If Package is set, code generation will write a package clause.
	Package string

	// TypeName is the name for the generated type. By default it is the name
	// of the type of the value passed to Gen.
	TypeName string
}

// Pack packs annotated bit ranges of struct x into an integer.
// Only fields that have a "bitfield" tag are compacted.
// Returns the packed value as uint64 and any error encountered.
func Pack(x interface{}, c *Config) (packed uint64, err error) {
	if c == nil {
		c = &Config{NumBits: 64}
	}

	v := reflect.ValueOf(x)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return 0, fmt.Errorf("Pack: expected struct, got %v", v.Kind())
	}

	t := v.Type()
	var bitOffset uint

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("bitfield")
		if tag == "" {
			continue // Skip fields without bitfield tag
		}

		// Parse tag: "methodName,bits" or just ",bits"
		var bits uint
		_, err := fmt.Sscanf(tag, ",%d", &bits)
		if err != nil {
			// Try with method name
			var methodName string
			_, err := fmt.Sscanf(tag, "%s,%d", &methodName, &bits)
			if err != nil {
				return 0, fmt.Errorf("Pack: invalid bitfield tag %q on field %s", tag, field.Name)
			}
		}

		if bits == 0 {
			continue
		}

		fieldValue := v.Field(i)
		var fieldBits uint64

		switch fieldValue.Kind() {
		case reflect.Bool:
			if fieldValue.Bool() {
				fieldBits = 1
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			fieldBits = fieldValue.Uint()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			val := fieldValue.Int()
			if val < 0 {
				return 0, fmt.Errorf("Pack: negative value %d for field %s", val, field.Name)
			}
			fieldBits = uint64(val)
		default:
			return 0, fmt.Errorf("Pack: unsupported field type %v for field %s", fieldValue.Kind(), field.Name)
		}

		// Check if value fits in bits
		maxValue := uint64((1 << bits) - 1)
		if fieldBits > maxValue {
			return 0, fmt.Errorf("Pack: value %d exceeds %d bits for field %s", fieldBits, bits, field.Name)
		}

		// Pack into result
		packed |= (fieldBits << bitOffset)
		bitOffset += bits
	}

	// Check if total bits exceed target size
	if c.NumBits > 0 && bitOffset > c.NumBits {
		return 0, fmt.Errorf("Pack: total bits %d exceeds NumBits %d", bitOffset, c.NumBits)
	}

	return packed, nil
}

