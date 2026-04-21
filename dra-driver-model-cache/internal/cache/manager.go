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

package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type CachedModel struct {
	ModelID    string
	Path       string
	Size       int64
	LastUsed   time.Time
	InUseByUID map[string]bool
}

type CacheManager struct {
	sync.Mutex
	rootDirectory string
	maxSize       int64
	currentSize   int64
	models        map[string]*CachedModel
	onUpdate      func()
}

func NewCacheManager(root string, maxSize int64) (*CacheManager, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache root: %w", err)
	}

	cm := &CacheManager{
		rootDirectory: root,
		maxSize:       maxSize,
		models:        make(map[string]*CachedModel),
	}

	// In a real implementation, we would scan the directory to populate current state.
	return cm, nil
}

func (cm *CacheManager) RootDirectory() string {
	return cm.rootDirectory
}

func (cm *CacheManager) Scan(validator func(path string) (string, int64, bool)) error {
	cm.Lock()
	defer cm.Unlock()

	return filepath.Walk(cm.rootDirectory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == cm.rootDirectory {
			return nil
		}

		if info.IsDir() {
			if modelID, size, ok := validator(path); ok {
				if _, alreadyExists := cm.models[modelID]; !alreadyExists {
					cm.models[modelID] = &CachedModel{
						ModelID:    modelID,
						Path:       path,
						Size:       size,
						LastUsed:   time.Now(),
						InUseByUID: make(map[string]bool),
					}
					cm.currentSize += size
				}
				return filepath.SkipDir
			}
		}
		return nil
	})
}

func (cm *CacheManager) GetModel(modelID string) (string, bool) {
	cm.Lock()
	defer cm.Unlock()
	model, ok := cm.models[modelID]
	if !ok {
		return "", false
	}
	return model.Path, true
}

func (cm *CacheManager) UseModel(modelID string, claimUID string) (string, error) {
	cm.Lock()
	defer cm.Unlock()

	model, ok := cm.models[modelID]
	if !ok {
		return "", fmt.Errorf("model %s not in cache", modelID)
	}

	model.LastUsed = time.Now()
	model.InUseByUID[claimUID] = true
	return model.Path, nil
}

func (cm *CacheManager) ReleaseModel(modelID string, claimUID string) {
	cm.Lock()
	defer cm.Unlock()

	if model, ok := cm.models[modelID]; ok {
		delete(model.InUseByUID, claimUID)
	}
}

func (cm *CacheManager) EnsureSpace(needed int64) error {
	cm.Lock()
	defer cm.Unlock()

	for cm.currentSize+needed > cm.maxSize {
		if !cm.evictLRU() {
			return fmt.Errorf("insufficient space and no models can be evicted")
		}
	}
	return nil
}

func (cm *CacheManager) AddModel(modelID string, size int64) (string, error) {
	cm.Lock()
	defer cm.Unlock()

	if _, ok := cm.models[modelID]; ok {
		return cm.models[modelID].Path, nil
	}

	path := filepath.Join(cm.rootDirectory, modelID)
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("failed to create model directory: %w", err)
	}
	cm.models[modelID] = &CachedModel{
		ModelID:    modelID,
		Path:       path,
		Size:       size,
		LastUsed:   time.Now(),
		InUseByUID: make(map[string]bool),
	}
	cm.currentSize += size
	if cm.onUpdate != nil {
		go cm.onUpdate()
	}
	return path, nil
}

func (cm *CacheManager) SetOnUpdate(f func()) {
	cm.Lock()
	defer cm.Unlock()
	cm.onUpdate = f
}

func (cm *CacheManager) evictLRU() bool {
	var oldestModel *CachedModel
	for _, m := range cm.models {
		if len(m.InUseByUID) > 0 {
			continue
		}
		if oldestModel == nil || m.LastUsed.Before(oldestModel.LastUsed) {
			oldestModel = m
		}
	}

	if oldestModel != nil {
		os.RemoveAll(oldestModel.Path)
		cm.currentSize -= oldestModel.Size
		delete(cm.models, oldestModel.ModelID)
		if cm.onUpdate != nil {
			go cm.onUpdate()
		}
		return true
	}
	return false
}

func (cm *CacheManager) GetCachedModels() []string {
	cm.Lock()
	defer cm.Unlock()
	var ids []string
	for id := range cm.models {
		ids = append(ids, id)
	}
	return ids
}
