package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"
)

// The fleet is declared twice and each declaration has its own reader: the
// POC runs the `cameras:` list in poc/config.yaml, and
// `securityctl site-config --registry poc/registry.yaml` generates the
// configurator's desired state from poc/registry.yaml. Nothing previously
// checked they agree, so adding a camera to one file made the configurator's
// plan describe a fleet the POC is not running — a silent divergence between
// the desired state and the one actually running, which is the entire failure
// mode the configurator exists to prevent.

type registryDoc struct {
	Cameras []struct {
		ID         string `yaml:"id"`
		Name       string `yaml:"name"`
		YouTubeURL string `yaml:"youtube_url"`
	} `yaml:"cameras"`
}

// repoRoot walks up from this test file to the repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate the repository root")
	}
	return filepath.Dir(filepath.Dir(file))
}

func TestPOCConfigAndRegistryDeclareTheSameFleet(t *testing.T) {
	root := repoRoot(t)

	cfgPath := filepath.Join(root, "poc", "config.yaml")
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Skipf("poc/config.yaml not readable in this layout: %v", err)
	}
	var cfg struct {
		Cameras []CameraConfig `yaml:"cameras"`
	}
	if err := yaml.Unmarshal(cfgData, &cfg); err != nil {
		t.Fatalf("parse poc/config.yaml: %v", err)
	}

	regData, err := os.ReadFile(filepath.Join(root, "poc", "registry.yaml"))
	if err != nil {
		t.Fatalf("poc/registry.yaml: %v", err)
	}
	var reg registryDoc
	if err := yaml.Unmarshal(regData, &reg); err != nil {
		t.Fatalf("parse poc/registry.yaml: %v", err)
	}

	configByID := map[string]string{}
	for _, c := range cfg.Cameras {
		configByID[c.ID] = c.YouTubeURL
	}
	regByID := map[string]string{}
	for _, c := range reg.Cameras {
		regByID[c.ID] = c.YouTubeURL
	}

	for id, url := range configByID {
		rurl, ok := regByID[id]
		if !ok {
			t.Errorf("camera %s runs from poc/config.yaml but is not in poc/registry.yaml, "+
				"so the configurator's desired state does not describe this camera", id)
			continue
		}
		if rurl != url {
			t.Errorf("camera %s has stream %q in poc/config.yaml and %q in poc/registry.yaml", id, url, rurl)
		}
	}
	for id := range regByID {
		if _, ok := configByID[id]; !ok {
			t.Errorf("camera %s is in poc/registry.yaml but not in poc/config.yaml, so the "+
				"POC does not run it", id)
		}
	}
}
