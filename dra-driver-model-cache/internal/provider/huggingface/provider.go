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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	cdispec "tags.cncf.io/container-device-interface/specs-go"

	hfapi "github.com/seasonjs/hf-hub/api"

	configapi "github.com/google/dra-driver-model-cache/api/modelcache.x-k8s.io/v1"
	"github.com/google/dra-driver-model-cache/internal"
	"github.com/google/dra-driver-model-cache/internal/cache"
)

type ModelRepo interface {
	Info() (*hfapi.RepoInfo, error)
	Download(filename string) (string, error)
}

type HuggingFaceAPI interface {
	Model(modelId string) ModelRepo
}

type realHFAPI struct {
	api *hfapi.Api
}

func (a *realHFAPI) Model(modelId string) ModelRepo {
	return a.api.Model(modelId)
}

type HuggingFaceProvider struct {
	cacheManager *cache.CacheManager
	authToken    string
	hfApi        HuggingFaceAPI

	mu               sync.Mutex
	ongoingDownloads map[string]chan struct{}
}

func NewHuggingFaceProvider(cm *cache.CacheManager, token string) *HuggingFaceProvider {
	builder, _ := hfapi.NewApiBuilder()
	if token != "" {
		builder.WithToken(token)
	}
	// Use a subdirectory of the cache manager's root for the HF hub cache
	hfCacheDir := filepath.Join(cm.RootDirectory(), "huggingface")
	builder.WithCacheDir(hfCacheDir)

	return &HuggingFaceProvider{
		cacheManager:     cm,
		authToken:        token,
		hfApi:            &realHFAPI{api: builder.Build()},
		ongoingDownloads: make(map[string]chan struct{}),
	}
}

func (p *HuggingFaceProvider) DiscoverModels(path string) (string, int64, bool) {
	// HF hub cache uses: models--<namespace>--<repo>/snapshots/<sha>
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "models--") {
		return "", 0, false
	}

	// Check if it's in our huggingface subdirectory
	if !strings.Contains(path, filepath.Join(p.cacheManager.RootDirectory(), "huggingface")) {
		return "", 0, false
	}

	modelID := strings.TrimPrefix(base, "models--")
	modelID = strings.ReplaceAll(modelID, "--", "/")

	// Get size by walking the directory
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})

	if size == 0 {
		// Might not be fully downloaded or empty
		return "", 0, false
	}

	return modelID, size, true
}

func (p *HuggingFaceProvider) ProviderId() string {
	return "huggingface"
}

func (p *HuggingFaceProvider) CacheDirectory() string {
	return filepath.Join(p.cacheManager.RootDirectory(), "huggingface")
}

func (p *HuggingFaceProvider) Devices() []resourceapi.Device {
	var devices []resourceapi.Device

	// Add stub device
	devices = append(devices, resourceapi.Device{
		Name:                     "provider-huggingface",
		AllowMultipleAllocations: ptr.To(true),
		Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"id":       {StringValue: ptr.To("*")},
			"provider": {StringValue: ptr.To("huggingface")},
			"cached":   {BoolValue: ptr.To(false)},
		},
	})

	// Add cached models
	for _, modelID := range p.cacheManager.GetCachedModels() {
		// Only include models that are managed by this provider
		// In a more robust implementation, we would have a way to know which provider owns which model.
		// For now, we'll assume anything in the cache manager that can be found in our cache dir.
		if p.isModelInCache(modelID) {
			deviceName := "model-" + strings.ReplaceAll(strings.ToLower(modelID), "/", "-")
			devices = append(devices, resourceapi.Device{
				Name:                     deviceName,
				AllowMultipleAllocations: ptr.To(true),
				Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
					"id":       {StringValue: ptr.To(modelID)},
					"provider": {StringValue: ptr.To("huggingface")},
					"cached":   {BoolValue: ptr.To(true)},
				},
			})
		}
	}

	return devices
}

func (p *HuggingFaceProvider) isModelInCache(modelID string) bool {
	// HF hub cache uses a specific directory structure: models--<namespace>--<repo>/snapshots/<sha>
	// For simplicity, we just check if the models--... directory exists.
	dirName := "models--" + strings.ReplaceAll(modelID, "/", "--")
	repoDir := filepath.Join(p.CacheDirectory(), dirName)
	_, err := os.Stat(repoDir)
	return err == nil
}

func (p *HuggingFaceProvider) PrepareClaims(claimUID string, config runtime.Object, results []*resourceapi.DeviceRequestAllocationResult) (internal.PerDeviceCDIContainerEdits, error) {
	edits := make(internal.PerDeviceCDIContainerEdits)

	for _, result := range results {
		var modelID string
		if result.Device == "provider-huggingface" {
			loader, ok := config.(*configapi.ModelLoader)
			if !ok {
				return nil, fmt.Errorf("ModelLoader config required for stub allocation")
			}
			modelID = loader.ModelID

			// Ensure model is downloaded
			if _, err := p.cacheManager.UseModel(modelID, claimUID); err != nil {
				klog.InfoS("Model not in cache, downloading", "modelID", modelID)
				if err := p.downloadModel(modelID); err != nil {
					return nil, fmt.Errorf("failed to download model %s: %w", modelID, err)
				}
				p.cacheManager.UseModel(modelID, claimUID)
			}
		} else {
			// Find modelID from device attributes
			for _, d := range p.Devices() {
				if d.Name == result.Device {
					if idAttr, ok := d.Attributes["id"]; ok && idAttr.StringValue != nil {
						modelID = *idAttr.StringValue
						break
					}
				}
			}
			if modelID == "" {
				return nil, fmt.Errorf("could not determine model ID for device %s", result.Device)
			}
			if _, err := p.cacheManager.UseModel(modelID, claimUID); err != nil {
				return nil, fmt.Errorf("cached model %s not found in manager", modelID)
			}
		}

		// Determine the snapshot path to mount
		snapshotPath, err := p.getSnapshotPath(modelID)
		if err != nil {
			return nil, err
		}

		edits[result.Device] = &cdiapi.ContainerEdits{
			ContainerEdits: &cdispec.ContainerEdits{
				Mounts: []*cdispec.Mount{
					{
						HostPath:      p.CacheDirectory(),
						ContainerPath: p.CacheDirectory(),
						Options:       []string{"ro", "bind"},
					},
				},
				Env: []string{
					fmt.Sprintf("MODEL_PATH=%s", snapshotPath),
					fmt.Sprintf("MODEL_NAME=%s", modelID),
				},
			},
		}
	}

	return edits, nil
}

func (p *HuggingFaceProvider) downloadModel(modelID string) error {
	p.mu.Lock()
	if _, ok := p.cacheManager.GetModel(modelID); ok {
		p.mu.Unlock()
		return nil
	}

	if ch, ok := p.ongoingDownloads[modelID]; ok {
		p.mu.Unlock()
		select {
		case <-ch:
			return nil
		default:
			return fmt.Errorf("download in progress")
		}
	}

	ch := make(chan struct{})
	p.ongoingDownloads[modelID] = ch
	p.mu.Unlock()

	go func() {
		defer func() {
			p.mu.Lock()
			delete(p.ongoingDownloads, modelID)
			close(ch)
			p.mu.Unlock()
		}()

		repo := p.hfApi.Model(modelID)
		info, err := repo.Info()
		if err != nil {
			klog.ErrorS(err, "failed to get repo info in background", "modelID", modelID)
			return
		}

		size := int64(1 * 1024 * 1024)
		if err := p.cacheManager.EnsureSpace(size); err != nil {
			klog.ErrorS(err, "failed to ensure space in background", "modelID", modelID)
			return
		}

		dirName := "models--" + strings.ReplaceAll(modelID, "/", "--")
		snapshotBase := filepath.Join(p.CacheDirectory(), dirName, "snapshots")
		if err := os.MkdirAll(snapshotBase, 0755); err != nil {
			klog.ErrorS(err, "failed to create snapshot base directory in background")
			return
		}

		for _, sibling := range info.Siblings {
			klog.InfoS("Downloading file", "modelID", modelID, "file", sibling.Rfilename)
			if _, err := repo.Download(sibling.Rfilename); err != nil {
				klog.ErrorS(err, "failed to download file in background", "file", sibling.Rfilename)
				return
			}
		}

		snapshotPath := filepath.Join(snapshotBase, info.Sha)
		if err := os.MkdirAll(snapshotPath, 0755); err != nil {
			klog.ErrorS(err, "failed to ensure snapshot directory in background")
			return
		}

		if _, err := p.cacheManager.AddModel(modelID, size); err != nil {
			klog.ErrorS(err, "failed to add model to cache manager in background")
		}
	}()

	return fmt.Errorf("download in progress")
}

func (p *HuggingFaceProvider) getSnapshotPath(modelID string) (string, error) {
	repo := p.hfApi.Model(modelID)
	info, err := repo.Info()
	if err != nil {
		return "", err
	}

	dirName := "models--" + strings.ReplaceAll(modelID, "/", "--")
	snapshotPath := filepath.Join(p.CacheDirectory(), dirName, "snapshots", info.Sha)

	if _, err := os.Stat(snapshotPath); err != nil {
		return "", fmt.Errorf("snapshot directory not found: %w", err)
	}

	return snapshotPath, nil
}

func (p *HuggingFaceProvider) UnprepareClaims(claimUID string, results []*resourceapi.DeviceRequestAllocationResult) error {
	for _, result := range results {
		var modelID string
		for _, d := range p.Devices() {
			if d.Name == result.Device {
				if idAttr, ok := d.Attributes["id"]; ok && idAttr.StringValue != nil {
					modelID = *idAttr.StringValue
					break
				}
			}
		}
		if modelID != "" && modelID != "*" {
			p.cacheManager.ReleaseModel(modelID, claimUID)
		}
	}
	return nil
}

func (p *HuggingFaceProvider) SchemeBuilder() runtime.SchemeBuilder {
	return runtime.NewSchemeBuilder(configapi.AddToScheme)
}
