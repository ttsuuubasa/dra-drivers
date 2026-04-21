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

package internal

import (
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/runtime"
	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
)

// PerDeviceCDIContainerEdits maps device name to its CDI container edits.
type PerDeviceCDIContainerEdits map[string]*cdiapi.ContainerEdits

// ModelProvider defines the interface for different model sources (e.g., HuggingFace).
type ModelProvider interface {
	// ProviderId returns the unique provider ID string.
	ProviderId() string
	// CacheDirectory returns the directory that stores the cached models.
	CacheDirectory() string
	// Devices returns the Device structures for this provider, including stub and cached models.
	Devices() []resourceapi.Device
	// PrepareClaims processes any claims for this provider and returns CDI edits.
	PrepareClaims(claimUID string, config runtime.Object, results []*resourceapi.DeviceRequestAllocationResult) (PerDeviceCDIContainerEdits, error)
	// UnprepareClaims informs the provider that claims are no longer actively using the models.
	UnprepareClaims(claimUID string, results []*resourceapi.DeviceRequestAllocationResult) error
	// SchemeBuilder returns the scheme builder for the provider's configuration types.
	SchemeBuilder() runtime.SchemeBuilder
	// DiscoverModels returns the model ID and size if the given path corresponds to a cached model for this provider.
	DiscoverModels(path string) (modelID string, size int64, ok bool)
}
