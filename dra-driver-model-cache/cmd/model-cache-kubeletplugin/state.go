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
	"fmt"
	"sync"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	drapbv1 "k8s.io/kubelet/pkg/apis/dra/v1beta1"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"

	"github.com/google/dra-driver-model-cache/internal"
)

type PreparedClaims map[string]internal.PreparedDevices

type DeviceState struct {
	sync.Mutex
	driverName        string
	cdi               *CDIHandler
	checkpointManager checkpointmanager.CheckpointManager
	configDecoder     runtime.Decoder
	config            *Config
}

func NewDeviceState(config *Config) (*DeviceState, error) {
	cdi, err := NewCDIHandler(config.flags.cdiRoot, config.flags.driverName, "model")
	if err != nil {
		return nil, fmt.Errorf("unable to create CDI handler: %v", err)
	}

	checkpointManager, err := checkpointmanager.NewCheckpointManager(config.DriverPluginPath())
	if err != nil {
		return nil, fmt.Errorf("unable to create checkpoint manager: %v", err)
	}

	configScheme := runtime.NewScheme()
	sb := config.SchemeBuilder()
	if err := sb.AddToScheme(configScheme); err != nil {
		return nil, fmt.Errorf("create config scheme: %w", err)
	}

	decoder := json.NewSerializerWithOptions(
		json.DefaultMetaFactory, configScheme, configScheme,
		json.SerializerOptions{Pretty: true, Strict: true},
	)

	state := &DeviceState{
		driverName:        config.flags.driverName,
		cdi:               cdi,
		checkpointManager: checkpointManager,
		configDecoder:     decoder,
		config:            config,
	}

	return state, nil
}

func (s *DeviceState) Prepare(claim *resourceapi.ResourceClaim) (internal.PreparedDevices, error) {
	s.Lock()
	defer s.Unlock()

	claimUID := string(claim.UID)

	// In a real driver, we'd check checkpoint first.
	// For now, prepare via provider.
	if claim.Status.Allocation == nil {
		return nil, fmt.Errorf("claim not yet allocated")
	}

	// Retrieve config (simplified: take first ModelLoader if present)
	var config runtime.Object
	for _, c := range claim.Status.Allocation.Devices.Config {
		if c.Opaque != nil && c.Opaque.Driver == s.driverName {
			var err error
			config, err = runtime.Decode(s.configDecoder, c.Opaque.Parameters.Raw)
			if err == nil {
				break
			}
		}
	}

	// Filter results for this driver
	var results []*resourceapi.DeviceRequestAllocationResult
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver == s.driverName {
			res := result // copy
			results = append(results, &res)
		}
	}

	perDeviceEdits, err := s.config.PrepareClaims(claimUID, config, results)
	if err != nil {
		return nil, err
	}

	var prepared internal.PreparedDevices
	for _, result := range results {
		device := &internal.PreparedDevice{
			Device: drapbv1.Device{
				RequestNames: []string{result.Request},
				PoolName:     result.Pool,
				DeviceName:   result.Device,
				CdiDeviceIds: []string{"k8s." + s.driverName + "/model=" + claimUID + "-" + result.Device},
			},
			ContainerEdits: perDeviceEdits[result.Device],
		}
		prepared = append(prepared, device)
	}

	// Create CDI spec for the claim
	if err := s.cdi.CreateClaimSpecFile(claimUID, prepared); err != nil {
		return nil, err
	}

	return prepared, nil
}

func (s *DeviceState) Unprepare(claimUID string) error {
	s.Lock()
	defer s.Unlock()

	// In a real driver, we'd look up which results were allocated from the checkpoint.
	// For this prototype, we'll assume no results for unprepare (or track them).
	if err := s.config.UnprepareClaims(claimUID, nil); err != nil {
		return err
	}

	return s.cdi.DeleteClaimSpecFile(claimUID)
}
