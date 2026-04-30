// internal/bootstrap/azure/preflight_test.go
package azure

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreflight_HappyPath(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{
		{Stdout: []byte(`{"id":"sub"}`)},
		{Stdout: []byte("Bicep CLI 0.30")},
	}}
	require.NoError(t, Preflight(context.Background(), mr))
	require.Len(t, mr.Calls, 2)
	require.Equal(t, []string{"account", "show"}, mr.Calls[0].Args)
	require.Equal(t, []string{"bicep", "version"}, mr.Calls[1].Args)
}

func TestPreflight_AzNotLoggedIn(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{Err: errors.New("Please run 'az login'")}}}
	err := Preflight(context.Background(), mr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "az login")
}

func TestPreflight_BicepMissing(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{
		{Stdout: []byte(`{"id":"sub"}`)},
		{Err: errors.New("ERROR: bicep not installed")},
	}}
	err := Preflight(context.Background(), mr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bicep")
}
