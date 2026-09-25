package main

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryCLIRequiresExplicitCutoffAndApproval(t *testing.T) {
	var b bytes.Buffer
	require.ErrorContains(t, run([]string{"-manifest", "anything.json"}, &b, &b), "cutoff")
	require.ErrorContains(t, run([]string{"-mode", "apply", "-manifest", "anything.json", "-cutoff", "2026-09-25T16:00:00Z"}, &b, &b), "approved")
}
func TestRecoveryManifestAtomicWriteDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	require.NoError(t, writeManifest(path, []byte("original")))
	require.Error(t, writeManifest(path, []byte("replacement")))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "original", string(raw))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
