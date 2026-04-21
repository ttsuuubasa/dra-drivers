/*
 * Copyright Google LLC.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package huggingface

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	resourceapi "k8s.io/api/resource/v1"

	configapi "github.com/google/dra-driver-model-cache/api/modelcache.x-k8s.io/v1"
	"github.com/google/dra-driver-model-cache/internal/cache"
	hfapi "github.com/seasonjs/hf-hub/api"
)

type mockModelRepo struct {
	info *hfapi.RepoInfo
	err  error
}

func (m *mockModelRepo) Info() (*hfapi.RepoInfo, error) {
	return m.info, m.err
}

func (m *mockModelRepo) Download(filename string) (string, error) {
	return filename, nil
}

type mockHFAPI struct {
	repos map[string]*mockModelRepo
}

func (m *mockHFAPI) Model(modelId string) ModelRepo {
	return m.repos[modelId]
}

func TestHuggingFaceProvider(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "hf-provider-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cm, err := cache.NewCacheManager(tempDir, 10*1024*1024)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	modelID := "google/gemma-2b"
	sha := "1234567890abcdef"

	// Create mock snapshot directory to satisfy getSnapshotPath
	dirName := "models--" + strings.ReplaceAll(modelID, "/", "--")
	snapshotPath := filepath.Join(tempDir, "huggingface", dirName, "snapshots", sha)
	if err := os.MkdirAll(snapshotPath, 0755); err != nil {
		t.Fatalf("failed to create mock snapshot dir: %v", err)
	}

	mockApi := &mockHFAPI{
		repos: map[string]*mockModelRepo{
			modelID: {
				info: &hfapi.RepoInfo{
					Sha: sha,
					Siblings: []hfapi.Siblings{
						{Rfilename: "config.json"},
					},
				},
			},
		},
	}

	p := NewHuggingFaceProvider(cm, "dummy-token")
	p.hfApi = mockApi

	// Test Devices() - should only have stub initially
	devices := p.Devices()
	if len(devices) != 1 {
		t.Errorf("expected 1 device (stub), got %d", len(devices))
	}

	// Test PrepareClaims with stub
	claimUID := "test-claim"
	config := &configapi.ModelLoader{
		ModelID: modelID,
	}
	results := []*resourceapi.DeviceRequestAllocationResult{
		{
			Device: "provider-huggingface",
			Driver: "modelcache.x-k8s.io",
			Pool:   "test-pool",
		},
	}

	edits, err := p.PrepareClaims(claimUID, config, results)
	if err != nil {
		t.Fatalf("failed to prepare claims: %v", err)
	}

	if len(edits) != 1 {
		t.Errorf("expected 1 edit, got %d", len(edits))
	}

	edit, ok := edits["provider-huggingface"]
	if !ok {
		t.Fatal("edit for provider-huggingface missing")
	}

	// Verify snapshot path in mounts
	if len(edit.Mounts) != 1 || edit.Mounts[0].HostPath != snapshotPath {
		t.Errorf("expected hostPath %s, got %s", snapshotPath, edit.Mounts[0].HostPath)
	}

	// Verify Devices() now includes the cached model
	devices = p.Devices()
	if len(devices) != 2 {
		t.Errorf("expected 2 devices (stub + cached), got %d", len(devices))
	}
}
