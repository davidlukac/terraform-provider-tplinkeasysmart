package provider

import (
	"reflect"
	"testing"
)

// mustAs is a checked type assertion for tests: a value of another type fails the test with both
// types named, where a bare assertion would panic.
func mustAs[T any](tb testing.TB, v any) T {
	tb.Helper()
	got, ok := v.(T)
	if !ok {
		tb.Fatalf("%T is not a %v", v, reflect.TypeFor[T]())
	}
	return got
}
