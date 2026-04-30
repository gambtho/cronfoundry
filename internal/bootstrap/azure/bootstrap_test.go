// internal/bootstrap/azure/bootstrap_test.go
package azure

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBootstrap_DryRun_StopsAfterParamsWrite(t *testing.T) {
	dir := t.TempDir()
	paramsPath := filepath.Join(dir, "params.json")
	imageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer imageSrv.Close()

	mr := &MockRunner{Responses: []MockResponse{
		{Stdout: []byte(`{"id":"sub"}`)},   // az account show
		{Stdout: []byte("Bicep CLI 0.30")}, // az bicep version
	}}

	bs := &Bootstrap{
		Runner:       mr,
		Inputs:       goodInputs(),
		MasterKey:    "MK",
		ParamsPath:   paramsPath,
		TemplateFile: "deploy/main.bicep",
		DryRun:       true,
		ImageRoot:    imageSrv.URL,
		Stdout:       &bytes.Buffer{},
	}
	require.NoError(t, bs.Run(context.Background()))

	require.Len(t, mr.Calls, 2)
	for _, c := range mr.Calls {
		require.NotContains(t, strings.Join(c.Args, " "), "deployment sub create")
	}
}
