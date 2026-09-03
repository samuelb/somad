package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeUTF8_ValidString(t *testing.T) {
	input := "Hello, World!"
	assert.Equal(t, input, SanitizeUTF8(input))
}

func TestSanitizeUTF8_ValidUnicode(t *testing.T) {
	input := "Café del Mar — Música Ambiental 日本語"
	assert.Equal(t, input, SanitizeUTF8(input))
}

func TestSanitizeUTF8_EmptyString(t *testing.T) {
	assert.Equal(t, "", SanitizeUTF8(""))
}

func TestSanitizeUTF8_InvalidBytes(t *testing.T) {
	// \xff is not valid UTF-8
	input := "Hello\xff World"
	result := SanitizeUTF8(input)
	assert.Equal(t, "Hello World", result)
}

func TestSanitizeUTF8_AllInvalid(t *testing.T) {
	input := "\xff\xfe\xfd"
	result := SanitizeUTF8(input)
	assert.Equal(t, "", result)
}

func TestSanitizeUTF8_MixedValidInvalid(t *testing.T) {
	input := "A\xffB\xfeC"
	result := SanitizeUTF8(input)
	assert.Equal(t, "ABC", result)
}
