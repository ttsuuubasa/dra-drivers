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
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/urfave/cli/v2"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/runtime"
	coreclientset "k8s.io/client-go/kubernetes"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/klog/v2"

	configapi "github.com/google/dra-driver-model-cache/api/modelcache.x-k8s.io/v1"
	"github.com/google/dra-driver-model-cache/internal"
	"github.com/google/dra-driver-model-cache/internal/cache"
	"github.com/google/dra-driver-model-cache/internal/config"
	"github.com/google/dra-driver-model-cache/internal/provider/gcs"
	"github.com/google/dra-driver-model-cache/internal/provider/huggingface"
	"github.com/google/dra-driver-model-cache/pkg/flags"
)

type Flags struct {
	kubeClientConfig flags.KubeClientConfig
	loggingConfig    *flags.LoggingConfig

	nodeName                      string
	cdiRoot                       string
	kubeletRegistrarDirectoryPath string
	kubeletPluginsDirectoryPath   string
	healthcheckPort               int
	driverName                    string
	configPath                    string
	maxCacheSize                  int64
}

type Config struct {
	flags         *Flags
	coreclient    coreclientset.Interface
	cancelMainCtx func(error)

	cacheManager *cache.CacheManager
	huggingface  *huggingface.HuggingFaceProvider
	gcs          *gcs.GCSProvider
}

func (c Config) DriverPluginPath() string {
	return filepath.Join(c.flags.kubeletPluginsDirectoryPath, c.flags.driverName)
}

func (c *Config) Devices() []resourceapi.Device {
	var devices []resourceapi.Device
	if c.huggingface != nil {
		devices = append(devices, c.huggingface.Devices()...)
	}
	if c.gcs != nil {
		devices = append(devices, c.gcs.Devices()...)
	}
	return devices
}

func (c *Config) PrepareClaims(claimUID string, config runtime.Object, results []*resourceapi.DeviceRequestAllocationResult) (internal.PerDeviceCDIContainerEdits, error) {
	allEdits := make(internal.PerDeviceCDIContainerEdits)

	for _, result := range results {
		found := false
		if c.huggingface != nil {
			for _, d := range c.huggingface.Devices() {
				if d.Name == result.Device {
					edits, err := c.huggingface.PrepareClaims(claimUID, config, []*resourceapi.DeviceRequestAllocationResult{result})
					if err != nil {
						return nil, err
					}
					for k, v := range edits {
						allEdits[k] = v
					}
					found = true
					break
				}
			}
		}
		if !found && c.gcs != nil {
			for _, d := range c.gcs.Devices() {
				if d.Name == result.Device {
					edits, err := c.gcs.PrepareClaims(claimUID, config, []*resourceapi.DeviceRequestAllocationResult{result})
					if err != nil {
						return nil, err
					}
					for k, v := range edits {
						allEdits[k] = v
					}
					found = true
					break
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("could not determine provider for device %s", result.Device)
		}
	}

	return allEdits, nil
}

func (c *Config) UnprepareClaims(claimUID string, results []*resourceapi.DeviceRequestAllocationResult) error {
	if c.huggingface != nil {
		c.huggingface.UnprepareClaims(claimUID, results)
	}
	if c.gcs != nil {
		c.gcs.UnprepareClaims(claimUID, results)
	}
	return nil
}

func (c *Config) SchemeBuilder() runtime.SchemeBuilder {
	return runtime.NewSchemeBuilder(configapi.AddToScheme)
}

func main() {
	if err := newApp().Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newApp() *cli.App {
	flags := &Flags{
		loggingConfig: flags.NewLoggingConfig(),
	}
	cliFlags := []cli.Flag{
		&cli.StringFlag{
			Name:        "node-name",
			Usage:       "The name of the node to be worked on.",
			Required:    true,
			Destination: &flags.nodeName,
			EnvVars:     []string{"NODE_NAME"},
		},
		&cli.StringFlag{
			Name:        "cdi-root",
			Usage:       "Absolute path to the directory where CDI files will be generated.",
			Value:       "/etc/cdi",
			Destination: &flags.cdiRoot,
			EnvVars:     []string{"CDI_ROOT"},
		},
		&cli.StringFlag{
			Name:        "kubelet-registrar-directory-path",
			Usage:       "Absolute path to the directory where kubelet stores plugin registrations.",
			Value:       kubeletplugin.KubeletRegistryDir,
			Destination: &flags.kubeletRegistrarDirectoryPath,
			EnvVars:     []string{"KUBELET_REGISTRAR_DIRECTORY_PATH"},
		},
		&cli.StringFlag{
			Name:        "kubelet-plugins-directory-path",
			Usage:       "Absolute path to the directory where kubelet stores plugin data.",
			Value:       kubeletplugin.KubeletPluginsDir,
			Destination: &flags.kubeletPluginsDirectoryPath,
			EnvVars:     []string{"KUBELET_PLUGINS_DIRECTORY_PATH"},
		},
		&cli.IntFlag{
			Name:        "healthcheck-port",
			Usage:       "Port to start a gRPC healthcheck service.",
			Value:       -1,
			Destination: &flags.healthcheckPort,
			EnvVars:     []string{"HEALTHCHECK_PORT"},
		},
		&cli.StringFlag{
			Name:        "driver-name",
			Usage:       "Name of the DRA driver.",
			Value:       "modelcache.x-k8s.io",
			Destination: &flags.driverName,
			EnvVars:     []string{"DRIVER_NAME"},
		},
		&cli.StringFlag{
			Name:        "config",
			Usage:       "Path to the driver configuration file.",
			Destination: &flags.configPath,
			Required:    true,
		},
		&cli.Int64Flag{
			Name:        "max-cache-size",
			Usage:       "Maximum size of the model cache in bytes.",
			Value:       10 * 1024 * 1024 * 1024, // 10GB
			Destination: &flags.maxCacheSize,
			EnvVars:     []string{"MAX_CACHE_SIZE"},
		},
	}
	cliFlags = append(cliFlags, flags.kubeClientConfig.Flags()...)
	cliFlags = append(cliFlags, flags.loggingConfig.Flags()...)

	app := &cli.App{
		Name:            "model-cache-kubeletplugin",
		Usage:           "model-cache-kubeletplugin implements a DRA driver plugin for model caching.",
		ArgsUsage:       " ",
		HideHelpCommand: true,
		Flags:           cliFlags,
		Before: func(c *cli.Context) error {
			return flags.loggingConfig.Apply()
		},
		Action: func(c *cli.Context) error {
			ctx := c.Context
			clientSets, err := flags.kubeClientConfig.NewClientSets()
			if err != nil {
				return fmt.Errorf("create client: %w", err)
			}

			cm, err := cache.NewCacheManager("/var/lib/model-cache", flags.maxCacheSize)
			if err != nil {
				return err
			}

			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			namespace := os.Getenv("NAMESPACE")
			if namespace == "" {
				namespace = "default"
			}

			drvConfig := &Config{
				flags:        flags,
				coreclient:   clientSets.Core,
				cacheManager: cm,
			}

			if cfg.Providers.HuggingFace.Enabled {
				drvConfig.huggingface = huggingface.NewHuggingFaceProvider(cm, os.Getenv("HF_TOKEN"))
			}
			if cfg.Providers.GCS.Enabled {
				drvConfig.gcs = gcs.NewGCSProvider(cm)
			}

			// Scan for existing models
			klog.InfoS("Scanning for existing models in cache")
			err = cm.Scan(func(path string) (string, int64, bool) {
				if drvConfig.huggingface != nil {
					if id, size, ok := drvConfig.huggingface.DiscoverModels(path); ok {
						return id, size, true
					}
				}
				if drvConfig.gcs != nil {
					if id, size, ok := drvConfig.gcs.DiscoverModels(path); ok {
						return id, size, true
					}
				}
				return "", 0, false
			})
			if err != nil {
				klog.ErrorS(err, "failed to scan cache")
			} else {
				klog.InfoS("Cache scan complete", "models", cm.GetCachedModels())
			}

			return RunPlugin(ctx, drvConfig)
		},
	}

	return app
}

func RunPlugin(ctx context.Context, config *Config) error {
	logger := klog.FromContext(ctx)

	err := os.MkdirAll(config.DriverPluginPath(), 0750)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()
	ctx, cancel := context.WithCancelCause(ctx)
	config.cancelMainCtx = cancel

	driver, err := NewDriver(ctx, config)
	if err != nil {
		return err
	}

	<-ctx.Done()
	stop()

	err = driver.Shutdown(logger)
	if err != nil {
		logger.Error(err, "Unable to cleanly shutdown driver")
	}

	return nil
}
