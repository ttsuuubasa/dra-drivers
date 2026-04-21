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

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/dra-driver-model-cache/internal"
	drapbv1 "k8s.io/kubelet/pkg/apis/dra/v1beta1"
	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	cdispec "tags.cncf.io/container-device-interface/specs-go"
)

func TestCDIHandler(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cdi-handler-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	driverName := "modelcache.x-k8s.io"
	className := "model"
	handler, err := NewCDIHandler(tempDir, driverName, className)
	if err != nil {
		t.Fatalf("failed to create CDI handler: %v", err)
	}

	// Test CreateCommonSpecFile
	os.Setenv("NODE_NAME", "test-node")
	if err := handler.CreateCommonSpecFile(); err != nil {
		t.Errorf("failed to create common spec file: %v", err)
	}

	commonSpecPath := filepath.Join(tempDir, "k8s.modelcache.x-k8s.io-model_common.yaml")
	if _, err := os.Stat(commonSpecPath); os.IsNotExist(err) {
		t.Errorf("common spec file not created at %s", commonSpecPath)
	}

	// Test CreateClaimSpecFile
	claimUID := "test-uid"
	devices := internal.PreparedDevices{
		{
			Device: drapbv1.Device{
				DeviceName: "test-device",
			},
			ContainerEdits: &cdiapi.ContainerEdits{
				ContainerEdits: &cdispec.ContainerEdits{
					Env: []string{"MODEL_PATH=/models/test"},
				},
			},
		},
	}

	if err := handler.CreateClaimSpecFile(claimUID, devices); err != nil {
		t.Errorf("failed to create claim spec file: %v", err)
	}

	claimSpecPath := filepath.Join(tempDir, "k8s.modelcache.x-k8s.io-model_test-uid.yaml")
	if _, err := os.Stat(claimSpecPath); os.IsNotExist(err) {
		t.Errorf("claim spec file not created at %s", claimSpecPath)
	}

	// Test GetClaimDevices
	deviceNames := []string{"test-device"}
	qualifiedNames := handler.GetClaimDevices(claimUID, deviceNames)
	if len(qualifiedNames) != 2 {
		t.Errorf("expected 2 qualified names, got %d", len(qualifiedNames))
	}
	// Expected: common and claim-specific device
	expectedCommon := "k8s.modelcache.x-k8s.io/model=common"
	if qualifiedNames[0] != expectedCommon {
		t.Errorf("expected %s, got %s", expectedCommon, qualifiedNames[0])
	}

	// Test DeleteClaimSpecFile
	if err := handler.DeleteClaimSpecFile(claimUID); err != nil {
		t.Errorf("failed to delete claim spec file: %v", err)
	}
	if _, err := os.Stat(claimSpecPath); !os.IsNotExist(err) {
		t.Errorf("claim spec file still exists after deletion")
	}
}
