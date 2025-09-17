package controller

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	internalfeatures "github.com/openshift/cluster-olm-operator/internal/featuregates"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/stretchr/testify/require"
)

func TestRenderHelmTemplate(t *testing.T) {
	// We need to copy the files into a temp directory
	testDir, err := os.MkdirTemp("", "helm-*")
	require.NoError(t, err)
	defer os.RemoveAll(testDir)

	// Get back to repo root directory
	err = os.Chdir("../..")
	require.NoError(t, err)

	cwd, err := os.Getwd()
	require.NoError(t, err)

	b := Builder{
		Assets: filepath.Join(cwd, "testdata"),
		Clients: &clients.Clients{
			FeatureGateMapper: internalfeatures.NewMapper(),
		},
		FeatureGate: configv1.FeatureGate{
			Status: configv1.FeatureGateStatus{
				FeatureGates: []configv1.FeatureGateDetails{},
			},
		},
	}

	require.NoError(t, b.renderHelmTemplate(b.Assets, testDir))

	compareDir := os.Getenv("HELM_OUTPUT")
	require.NotEmpty(t, compareDir)
	os.Remove(compareDir)

	compareFile := filepath.Join(compareDir, "hello-world.yaml")
	compareData, err := os.ReadFile(compareFile)
	require.NoError(t, err)

	var testData []byte

	err = filepath.WalkDir(testDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
			return nil
		}
		t.Logf("Reading file %q", path)
		tmpData, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		testData = append(testData, tmpData...)

		return nil
	})
	require.NoError(t, err)
	require.Equal(t, compareData, testData)
}
