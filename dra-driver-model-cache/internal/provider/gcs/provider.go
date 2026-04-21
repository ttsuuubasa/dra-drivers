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

package gcs

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

	configapi "github.com/google/dra-driver-model-cache/api/modelcache.x-k8s.io/v1"
	"github.com/google/dra-driver-model-cache/internal"
	"github.com/google/dra-driver-model-cache/internal/cache"
)

type GCSProvider struct {
	cacheManager *cache.CacheManager

	mu               sync.Mutex
	ongoingDownloads map[string]chan struct{}
}

func NewGCSProvider(cm *cache.CacheManager) *GCSProvider {
	return &GCSProvider{
		cacheManager:     cm,
		ongoingDownloads: make(map[string]chan struct{}),
	}
}

func (p *GCSProvider) DiscoverModels(path string) (string, int64, bool) {
	// GCS cache uses: <root>/gcs/<modelID>
	// The path passed in is the directory being inspected.
	
	gcsRoot := p.CacheDirectory()
	if !strings.HasPrefix(path, gcsRoot) || path == gcsRoot {
		return "", 0, false
	}

	// The modelID is the relative path from the GCS root
	modelID, err := filepath.Rel(gcsRoot, path)
	if err != nil {
		return "", 0, false
	}

	// Get size by walking the directory
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})

	if size == 0 {
		return "", 0, false
	}

	return modelID, size, true
}

func (p *GCSProvider) ProviderId() string {
	return "gcs"
}

func (p *GCSProvider) CacheDirectory() string {
	return filepath.Join(p.cacheManager.RootDirectory(), "gcs")
}

func (p *GCSProvider) Devices() []resourceapi.Device {
	var devices []resourceapi.Device

	// Add stub device
	devices = append(devices, resourceapi.Device{
		Name:                     "provider-gcs",
		AllowMultipleAllocations: ptr.To(true),
		Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"id":       {StringValue: ptr.To("*")},
			"provider": {StringValue: ptr.To("gcs")},
			"cached":   {BoolValue: ptr.To(false)},
		},
	})

	// Add cached models
	for _, modelID := range p.cacheManager.GetCachedModels() {
		// Only show models that are in our cache directory
		if p.isModelInCache(modelID) {
			deviceName := "gcs-model-" + strings.ReplaceAll(strings.ToLower(modelID), "/", "-")
			devices = append(devices, resourceapi.Device{
				Name:                     deviceName,
				AllowMultipleAllocations: ptr.To(true),
				Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
					"id":       {StringValue: ptr.To(modelID)},
					"provider": {StringValue: ptr.To("gcs")},
					"cached":   {BoolValue: ptr.To(true)},
				},
			})
		}
	}

	return devices
}

func (p *GCSProvider) isModelInCache(modelID string) bool {
	repoDir := filepath.Join(p.CacheDirectory(), modelID)
	_, err := os.Stat(repoDir)
	return err == nil
}

func (p *GCSProvider) PrepareClaims(claimUID string, config runtime.Object, results []*resourceapi.DeviceRequestAllocationResult) (internal.PerDeviceCDIContainerEdits, error) {
	edits := make(internal.PerDeviceCDIContainerEdits)

	for _, result := range results {
		var modelID string
		if result.Device == "provider-gcs" {
			loader, ok := config.(*configapi.ModelLoader)
			if !ok {
				return nil, fmt.Errorf("ModelLoader config required for stub allocation")
			}
			modelID = loader.ModelID

			// Download model if not cached
			if _, err := p.cacheManager.UseModel(modelID, claimUID); err != nil {
				klog.InfoS("Model not in cache, downloading from GCS", "modelID", modelID)
				if err := p.downloadModel(modelID); err != nil {
					return nil, err
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

		hostPath := filepath.Join(p.CacheDirectory(), modelID)
		containerPath := "/models/" + modelID

		if _, err := os.Stat(hostPath); err != nil {
			return nil, fmt.Errorf("model directory %s not found: %w", hostPath, err)
		}

		edits[result.Device] = &cdiapi.ContainerEdits{
			ContainerEdits: &cdispec.ContainerEdits{
				Mounts: []*cdispec.Mount{
					{
						HostPath:      hostPath,
						ContainerPath: containerPath,
						Options:       []string{"ro", "bind"},
					},
				},
				Env: []string{
					fmt.Sprintf("MODEL_PATH=%s", containerPath),
					fmt.Sprintf("MODEL_NAME=%s", modelID),
				},
			},
		}
	}

	return edits, nil
}

func (p *GCSProvider) downloadModel(modelID string) error {
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

		// Simulate download delay
		klog.InfoS("Simulating GCS download", "modelID", modelID)

		size := int64(1024 * 1024)
		if err := p.cacheManager.EnsureSpace(size); err != nil {
			klog.ErrorS(err, "failed to ensure space in background", "modelID", modelID)
			return
		}

		modelDir := filepath.Join(p.CacheDirectory(), modelID)
		if err := os.MkdirAll(modelDir, 0755); err != nil {
			klog.ErrorS(err, "failed to create model directory in background", "modelID", modelID)
			return
		}

		if _, err := p.cacheManager.AddModel(modelID, size); err != nil {
			klog.ErrorS(err, "failed to add model to cache manager in background", "modelID", modelID)
		}
	}()

	return fmt.Errorf("download in progress")
}

func (p *GCSProvider) UnprepareClaims(claimUID string, results []*resourceapi.DeviceRequestAllocationResult) error {
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

func (p *GCSProvider) SchemeBuilder() runtime.SchemeBuilder {
	return runtime.NewSchemeBuilder(configapi.AddToScheme)
}
