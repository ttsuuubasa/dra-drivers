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

package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type HuggingFaceConfig struct {
	Enabled    bool   `yaml:"enabled"`
	SecretName string `yaml:"secretName"`
	Timeout    int    `yaml:"timeout,omitempty"`
}

type GCSConfig struct {
	Enabled bool `yaml:"enabled"`
	Timeout int  `yaml:"timeout,omitempty"`
}

type ProvidersConfig struct {
	HuggingFace HuggingFaceConfig `yaml:"huggingface"`
	GCS         GCSConfig         `yaml:"gcs"`
}

type DriverConfig struct {
	Providers ProvidersConfig `yaml:"providers"`
}

func LoadConfig(path string) (*DriverConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg DriverConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &cfg, nil
}
