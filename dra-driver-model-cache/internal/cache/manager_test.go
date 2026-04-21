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
	"os"
	"testing"
	"time"
)

func TestCacheManager(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "model-cache-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	maxSize := int64(100)
	cm, err := NewCacheManager(tempDir, maxSize)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	// Add a model
	model1 := "model1"
	size1 := int64(40)
	if _, err := cm.AddModel(model1, size1); err != nil {
		t.Errorf("failed to add model1: %v", err)
	}

	// Use the model
	claim1 := "claim1"
	if _, err := cm.UseModel(model1, claim1); err != nil {
		t.Errorf("failed to use model1: %v", err)
	}

	// Add another model
	model2 := "model2"
	size2 := int64(50)
	if _, err := cm.AddModel(model2, size2); err != nil {
		t.Errorf("failed to add model2: %v", err)
	}

	// Current size: 40 + 50 = 90. Max: 100.
	// Add a third model that triggers eviction
	model3 := "model3"
	size3 := int64(30)
	// model1 is in use, model2 is not. model2 should be evicted.
	if err := cm.EnsureSpace(size3); err != nil {
		t.Errorf("failed to ensure space for model3: %v", err)
	}

	if _, err := cm.AddModel(model3, size3); err != nil {
		t.Errorf("failed to add model3: %v", err)
	}

	cached := cm.GetCachedModels()
	found1, found2, found3 := false, false, false
	for _, m := range cached {
		if m == model1 {
			found1 = true
		}
		if m == model2 {
			found2 = true
		}
		if m == model3 {
			found3 = true
		}
	}

	if !found1 {
		t.Error("model1 should still be in cache")
	}
	if found2 {
		t.Error("model2 should have been evicted")
	}
	if !found3 {
		t.Error("model3 should be in cache")
	}

	// Release model1 and try to evict it
	cm.ReleaseModel(model1, claim1)
	time.Sleep(10 * time.Millisecond) // Ensure timestamp update
	if err := cm.EnsureSpace(int64(90)); err != nil {
		t.Errorf("failed to ensure space for large model: %v", err)
	}

	if len(cm.GetCachedModels()) > 1 {
		t.Error("models should have been evicted")
	}
}

func TestCacheManagerOnUpdate(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "model-cache-update-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cm, err := NewCacheManager(tempDir, 100)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	updateCalled := make(chan bool, 1)
	cm.SetOnUpdate(func() {
		updateCalled <- true
	})

	if _, err := cm.AddModel("test-update-model", 10); err != nil {
		t.Fatalf("failed to add model: %v", err)
	}

	select {
	case <-updateCalled:
		// Callback was successfully triggered!
	case <-time.After(2 * time.Second):
		t.Error("onUpdate callback was not triggered within the expected timeframe")
	}
}
