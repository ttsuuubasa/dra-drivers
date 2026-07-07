package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	coreclientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/klog/v2"
)

type noopDriver struct {
}

func (d *noopDriver) PrepareResourceClaims(ctx context.Context, claims []*resourceapi.ResourceClaim) (map[types.UID]kubeletplugin.PrepareResult, error) {
	klog.InfoS("PrepareResourceClaims called", "numClaims", len(claims))
	result := make(map[types.UID]kubeletplugin.PrepareResult)
	for _, claim := range claims {
		result[claim.UID] = kubeletplugin.PrepareResult{}
	}
	return result, nil
}

func (d *noopDriver) UnprepareResourceClaims(ctx context.Context, claims []kubeletplugin.NamespacedObject) (map[types.UID]error, error) {
	klog.InfoS("UnprepareResourceClaims called", "numClaims", len(claims))
	result := make(map[types.UID]error)
	for _, claim := range claims {
		result[claim.UID] = nil
	}
	return result, nil
}

func (d *noopDriver) HandleError(ctx context.Context, err error, msg string) {
	utilruntime.HandleErrorWithContext(ctx, err, msg)
}

func main() {
	var driverNamesStr string
	var nodeName string
	var registrarDir string
	var pluginsDir string

	flag.StringVar(&driverNamesStr, "driver-names", "", "Comma-separated list of driver names")
	flag.StringVar(&nodeName, "node-name", "", "Node name")
	flag.StringVar(&registrarDir, "kubelet-registrar-dir", kubeletplugin.KubeletRegistryDir, "Kubelet registrar directory")
	flag.StringVar(&pluginsDir, "kubelet-plugins-dir", kubeletplugin.KubeletPluginsDir, "Kubelet plugins directory")
	flag.Parse()

	if driverNamesStr == "" {
		klog.Fatal("At least one driver name must be specified")
	}
	if nodeName == "" {
		klog.Fatal("Node name must be specified")
	}

	driverNames := strings.Split(driverNamesStr, ",")

	klog.InitFlags(nil)

	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatal("Failed to get in-cluster config: ", err)
	}
	clientset, err := coreclientset.NewForConfig(config)
	if err != nil {
		klog.Fatal("Failed to create clientset: ", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	driver := &noopDriver{}
	var helpers []*kubeletplugin.Helper

	for _, name := range driverNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		klog.InfoS("Starting driver", "name", name)

		pluginDataDir := pluginsDir + "/" + name
		if err := os.MkdirAll(pluginDataDir, 0750); err != nil {
			klog.Fatal("Failed to create plugin data directory: ", err)
		}

		helper, err := kubeletplugin.Start(
			ctx,
			driver,
			kubeletplugin.KubeClient(clientset),
			kubeletplugin.NodeName(nodeName),
			kubeletplugin.DriverName(name),
			kubeletplugin.RegistrarDirectoryPath(registrarDir),
			kubeletplugin.PluginDataDirectoryPath(pluginDataDir),
		)
		if err != nil {
			klog.Fatal("Failed to start kubeletplugin: ", err)
		}
		helpers = append(helpers, helper)
	}

	klog.Info("All drivers started")
	<-ctx.Done()
	klog.Info("Shutting down")

	for _, helper := range helpers {
		helper.Stop()
	}
}
