package textutil

import "unicode/utf8"

// PrefixBytes returns a prefix within the byte budget without splitting a UTF-8 rune.
func PrefixBytes(value string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(value) <= budget {
		return value
	}
	for budget > 0 && !utf8.RuneStart(value[budget]) {
		budget--
	}
	return value[:budget]
}

// SuffixBytes returns a suffix within the byte budget without splitting a UTF-8 rune.
func SuffixBytes(value string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(value) <= budget {
		return value
	}
	start := len(value) - budget
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}
