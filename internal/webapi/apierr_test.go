package webapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression: sqlc returns a nil slice when a query has no rows. json.Marshal
// of a nil slice produces literal "null", which the React SPA's `.map(...)`
// then crashes on. writeJSON should serialize a nil slice as "[]".
func TestWriteJSON_NilSliceBecomesEmptyArray(t *testing.T) {
	var nilStrings []string

	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, nilStrings)

	body := strings.TrimSpace(rr.Body.String())
	if body != "[]" {
		t.Errorf("nil []string -> %q, want %q", body, "[]")
	}
}

// Non-nil empty and non-empty slices continue to serialize the obvious way.
func TestWriteJSON_EmptyAndNonEmptySlicesUnchanged(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"empty slice literal", []string{}, "[]"},
		{"non-empty slice", []string{"a", "b"}, `["a","b"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeJSON(rr, http.StatusOK, tc.in)
			body := strings.TrimSpace(rr.Body.String())
			if body != tc.want {
				t.Errorf("%v -> %q, want %q", tc.in, body, tc.want)
			}
		})
	}
}

// Non-slice values (struct, map, primitive, untyped nil) are not transformed
// — they keep their natural JSON encoding, including "null" for untyped nil.
func TestWriteJSON_NonSlicesUntouched(t *testing.T) {
	type point struct{ X, Y int }
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"struct", point{X: 1, Y: 2}, `{"X":1,"Y":2}`},
		{"map", map[string]int{"a": 1}, `{"a":1}`},
		{"int", 42, "42"},
		{"untyped nil", nil, "null"},
		{"nil map", map[string]int(nil), "null"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeJSON(rr, http.StatusOK, tc.in)
			body := strings.TrimSpace(rr.Body.String())
			if body != tc.want {
				t.Errorf("%v -> %q, want %q", tc.in, body, tc.want)
			}
		})
	}
}

// JSON array wire format must round-trip cleanly through the standard
// decoder so the browser sees a real list, not a string.
func TestWriteJSON_NilSliceDecodesAsEmptyArray(t *testing.T) {
	var nilInts []int
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, nilInts)

	var got []int
	if err := json.NewDecoder(bytes.NewReader(rr.Body.Bytes())).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got == nil {
		t.Fatal("decoded slice is nil; want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("decoded slice = %v, want []", got)
	}
}
