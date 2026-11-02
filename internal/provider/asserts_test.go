package provider

import (
	"reflect"
	"testing"
)

// mustAs is a checked type assertion for tests: a value of another type fails the test with both
// types named, where a bare assertion would panic.
func mustAs[T any](t testing.TB, v any) T {
	t.Helper()
	got, ok := v.(T)
	if !ok {
		t.Fatalf("%T is not a %v", v, reflect.TypeFor[T]())
	}
	return got
}
