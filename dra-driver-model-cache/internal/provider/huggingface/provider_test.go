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
	"time"

	resourceapi "k8s.io/api/resource/v1"

	configapi "github.com/google/dra-driver-model-cache/api/modelcache.x-k8s.io/v1"
	"github.com/google/dra-driver-model-cache/internal/cache"
	hfapi "github.com/seasonjs/hf-hub/api"
)

type mockModelRepo struct {
	info          *hfapi.RepoInfo
	err           error
	downloadCount int
}

func (m *mockModelRepo) Info() (*hfapi.RepoInfo, error) {
	return m.info, m.err
}

func (m *mockModelRepo) Download(filename string) (string, error) {
	m.downloadCount++
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

	modelID := "google/gemma-2b"
	cm, err := cache.NewCacheManager(tempDir, 10*1024*1024)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	if _, err := cm.AddModel(modelID, 1024); err != nil {
		t.Fatalf("failed to add model to cache manager: %v", err)
	}
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

	p := NewHuggingFaceProvider(cm, "dummy-token", 0)
	p.hfApi = mockApi

	// Test Devices() - should have stub and pre-added cached model
	devices := p.Devices()
	if len(devices) != 2 {
		t.Errorf("expected 2 devices (stub + cached), got %d", len(devices))
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
	if len(edit.Mounts) != 1 || edit.Mounts[0].HostPath != p.CacheDirectory() {
		t.Errorf("expected hostPath %s, got %s", p.CacheDirectory(), edit.Mounts[0].HostPath)
	}

	// Verify Devices() now includes the cached model
	devices = p.Devices()
	if len(devices) != 2 {
		t.Errorf("expected 2 devices (stub + cached), got %d", len(devices))
	}
}

func TestHuggingFaceProvider_ConcurrentDownloads(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "hf-provider-concurrent-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	modelID := "google/gemma-2b"
	cm, err := cache.NewCacheManager(tempDir, 10*1024*1024)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	sha := "1234567890abcdef"
	mockRepo := &mockModelRepo{
		info: &hfapi.RepoInfo{
			Sha: sha,
			Siblings: []hfapi.Siblings{
				{Rfilename: "config.json"},
			},
		},
	}

	mockApi := &mockHFAPI{
		repos: map[string]*mockModelRepo{
			modelID: mockRepo,
		},
	}

	p := NewHuggingFaceProvider(cm, "dummy-token", 0)
	p.hfApi = mockApi

	claimUID1 := "test-claim-1"
	claimUID2 := "test-claim-2"
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

	// First call should return success with hook (download in progress)
	edits1, err := p.PrepareClaims(claimUID1, config, results)
	if err != nil {
		t.Fatalf("failed to prepare claims for pod 1: %v", err)
	}

	edit1, ok := edits1["provider-huggingface"]
	if !ok {
		t.Fatal("edit for provider-huggingface missing in edits1")
	}

	if len(edit1.Hooks) != 1 {
		t.Errorf("expected 1 hook in edits1, got %d", len(edit1.Hooks))
	}

	// Second call for same model should also return success with hook
	edits2, err := p.PrepareClaims(claimUID2, config, results)
	if err != nil {
		t.Fatalf("failed to prepare claims for pod 2: %v", err)
	}

	edit2, ok := edits2["provider-huggingface"]
	if !ok {
		t.Fatal("edit for provider-huggingface missing in edits2")
	}

	if len(edit2.Hooks) != 1 {
		t.Errorf("expected 1 hook in edits2, got %d", len(edit2.Hooks))
	}

	// Wait for download to be counted (background task)
	attempts := 0
	for attempts < 20 && mockRepo.downloadCount == 0 {
		time.Sleep(50 * time.Millisecond)
		attempts++
	}

	// Verify that Download was only called once
	if mockRepo.downloadCount != 1 {
		t.Errorf("expected downloadCount 1, got %d", mockRepo.downloadCount)
	}
}
