package webapi

import (
	"encoding/json"
	"net/http"
	"reflect"
)

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeErr(w http.ResponseWriter, status int, msg, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{Error: msg, Code: code})
}

// writeJSON writes v as a JSON response. If v is a nil slice, it is replaced
// with an empty slice of the same element type so the wire format is "[]"
// rather than "null". Without this, sqlc's nil result for an empty query
// surfaces in the browser as `null`, and SPA components doing `data.map(...)`
// throw "Cannot read properties of null". List endpoints should always
// return a JSON array; this enforces it at one place rather than asking
// every handler to remember.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(emptySliceIfNil(v))
}

// emptySliceIfNil returns an empty slice of the same type when v is a typed
// nil slice; otherwise returns v unchanged. Untyped nil and non-slice values
// pass through (the caller probably wants `null` or whatever else).
func emptySliceIfNil(v any) any {
	if v == nil {
		return v
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return reflect.MakeSlice(rv.Type(), 0, 0).Interface()
	}
	return v
}
