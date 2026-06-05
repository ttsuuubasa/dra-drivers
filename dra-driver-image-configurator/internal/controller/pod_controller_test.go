package controller

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1alpha1 "github.com/ttsuuubasa/dra-driver-image-configurator/api/v1alpha1"
)

// ── builders ──────────────────────────────────────────────────────────────────

type claimOption func(*resourceapi.ResourceClaim)
type podOption func(*corev1.Pod)

// DeviceRef uniquely identifies a device and optionally declares binding conditions.
// Used as a named-argument style parameter across multiple builder functions.
type DeviceRef struct {
	Driver            string
	Pool              string
	Device            string
	BindingConditions []string
}

// newClaim builds a ResourceClaim with Allocation always initialized.
func newClaim(opts ...claimOption) *resourceapi.ResourceClaim {
	c := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "my-claim", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{},
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// withResult adds a device to the claim. If ref.BindingConditions is set, those
// conditions are required before the device binding is considered complete.
func withResult(ref DeviceRef) claimOption {
	return func(c *resourceapi.ResourceClaim) {
		c.Status.Allocation.Devices.Results = append(
			c.Status.Allocation.Devices.Results,
			resourceapi.DeviceRequestAllocationResult{
				Driver:            ref.Driver,
				Pool:              ref.Pool,
				Device:            ref.Device,
				BindingConditions: ref.BindingConditions,
			},
		)
	}
}

// withConditionSet marks the given condition as ConditionTrue on the device.
func withConditionSet(ref DeviceRef, condition string) claimOption {
	return func(c *resourceapi.ResourceClaim) {
		c.Status.Devices = append(c.Status.Devices, resourceapi.AllocatedDeviceStatus{
			Driver: ref.Driver,
			Pool:   ref.Pool,
			Device: ref.Device,
			Conditions: []metav1.Condition{
				{Type: condition, Status: metav1.ConditionTrue},
			},
		})
	}
}

// ImageRef specifies a container image override delivered via ImageConfig.
type ImageRef struct {
	ContainerName string
	Image         string
}

// withImageConfig encodes an ImageConfig as opaque parameters and appends it to the claim.
func withImageConfig(t *testing.T, ref ImageRef) claimOption {
	t.Helper()
	ic := &imagev1alpha1.ImageConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: imagev1alpha1.SchemeGroupVersion.String(),
			Kind:       "ImageConfig",
		},
		ContainerName: ref.ContainerName,
		Image:         ref.Image,
	}
	raw, err := json.Marshal(ic)
	if err != nil {
		t.Fatalf("marshal ImageConfig: %v", err)
	}
	return func(c *resourceapi.ResourceClaim) {
		c.Status.Allocation.Devices.Config = append(
			c.Status.Allocation.Devices.Config,
			resourceapi.DeviceAllocationConfiguration{
				DeviceConfiguration: resourceapi.DeviceConfiguration{
					Opaque: &resourceapi.OpaqueDeviceConfiguration{
						Driver:     "driver.example.com",
						Parameters: runtime.RawExtension{Raw: raw},
					},
				},
			},
		)
	}
}

// newPod builds a Pod.
func newPod(opts ...podOption) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "default"},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// withContainer adds a container with the given name and image to the Pod.
func withContainer(ref ImageRef) podOption {
	return func(p *corev1.Pod) {
		p.Spec.Containers = append(p.Spec.Containers, corev1.Container{
			Name:  ref.ContainerName,
			Image: ref.Image,
		})
	}
}

// withClaimRef adds a ResourceClaim reference to the Pod's ResourceClaimStatuses.
func withClaimRef(claimName string) podOption {
	name := claimName
	return func(p *corev1.Pod) {
		p.Status.ResourceClaimStatuses = append(
			p.Status.ResourceClaimStatuses,
			corev1.PodResourceClaimStatus{ResourceClaimName: &name},
		)
	}
}

// newScheme builds a runtime.Scheme that includes all types used by the controller.
func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		resourceapi.AddToScheme,
		imagev1alpha1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("add scheme: %v", err)
		}
	}
	return s
}

// ── collectPendingBindingResults ──────────────────────────────────────────────

func TestCollectPendingBindingResults(t *testing.T) {
	tests := []struct {
		name    string
		claims  []*resourceapi.ResourceClaim
		wantLen int
	}{
		{
			name: "returns pending result when image-verified condition is required but not yet set",
			claims: []*resourceapi.ResourceClaim{
				newClaim(withResult(DeviceRef{
					Driver:            "driver.example.com",
					Pool:              "pool-a",
					Device:            "device-0",
					BindingConditions: []string{BindingConditionValidateImage},
				})),
			},
			wantLen: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := collectPendingBindingResults(tc.claims)
			if len(got) != tc.wantLen {
				t.Errorf("len = %d, want %d", len(got), tc.wantLen)
			}
		})
	}
}

// ── collectImageConfigs ───────────────────────────────────────────────────────

func TestCollectImageConfigs(t *testing.T) {
	tests := []struct {
		name      string
		claims    []*resourceapi.ResourceClaim
		wantLen   int
		wantImage string
	}{
		{
			name: "decodes valid ImageConfig from opaque parameters",
			claims: []*resourceapi.ResourceClaim{
				newClaim(withImageConfig(t, ImageRef{ContainerName: "app", Image: "nginx:1.27"})),
			},
			wantLen:   1,
			wantImage: "nginx:1.27",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := collectImageConfigs(tc.claims)
			if len(got) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tc.wantLen)
			}
			if tc.wantLen > 0 && got[0].Image != tc.wantImage {
				t.Errorf("Image = %q, want %q", got[0].Image, tc.wantImage)
			}
		})
	}
}

// ── isBindingConditionAlreadySet ─────────────────────────────────────────────

func TestIsBindingConditionAlreadySet(t *testing.T) {
	tests := []struct {
		name      string
		claim     *resourceapi.ResourceClaim
		result    *resourceapi.DeviceRequestAllocationResult
		condition string
		want      bool
	}{
		{
			name: "returns true when condition is already set to True",
			claim: newClaim(
				withConditionSet(
					DeviceRef{
						Driver: "driver.example.com",
						Pool:   "pool-a",
						Device: "device-0",
					},
					BindingConditionValidateImage,
				)),
			result: &resourceapi.DeviceRequestAllocationResult{
				Driver: "driver.example.com",
				Pool:   "pool-a",
				Device: "device-0",
			},
			condition: BindingConditionValidateImage,
			want:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isBindingConditionAlreadySet(tc.claim, tc.result, tc.condition)
			if got != tc.want {
				t.Errorf("isBindingConditionAlreadySet = %v, want %v", got, tc.want)
			}
		})
	}
}

// ── fetchClaims ───────────────────────────────────────────────────────────────

func TestFetchClaims(t *testing.T) {
	scheme := newScheme(t)

	tests := []struct {
		name    string
		pod     *corev1.Pod
		claims  []*resourceapi.ResourceClaim
		wantLen int
		wantErr bool
	}{
		{
			name: "fetches allocated claim referenced by pod",
			pod: newPod(
				withClaimRef("my-claim"),
			),
			claims:  []*resourceapi.ResourceClaim{newClaim()},
			wantLen: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs := make([]runtime.Object, len(tc.claims))
			for i, c := range tc.claims {
				objs[i] = c
			}
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objs...).
				Build()

			r := &PodReconciler{Client: c}
			got, err := r.fetchClaims(context.Background(), tc.pod)

			if (err != nil) != tc.wantErr {
				t.Fatalf("fetchClaims() error = %v, wantErr %v", err, tc.wantErr)
			}
			if len(got) != tc.wantLen {
				t.Errorf("len = %d, want %d", len(got), tc.wantLen)
			}
		})
	}
}

// ── patchImages ───────────────────────────────────────────────────────────────

func TestPatchImages(t *testing.T) {
	scheme := newScheme(t)

	tests := []struct {
		name         string
		pod          *corev1.Pod
		imageConfigs []*imagev1alpha1.ImageConfig
		wantImage    string
		wantErr      bool
	}{
		{
			name: "updates container image matching ContainerName",
			pod: newPod(
				withContainer(ImageRef{ContainerName: "app", Image: "registry.k8s.io/pause:3.10"}),
			),
			imageConfigs: []*imagev1alpha1.ImageConfig{
				{ContainerName: "app", Image: "nginx:1.27"},
			},
			wantImage: "nginx:1.27",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tc.pod).
				Build()

			r := &PodReconciler{Client: c}
			err := r.patchImages(context.Background(), tc.pod, tc.imageConfigs)

			if (err != nil) != tc.wantErr {
				t.Fatalf("patchImages() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && tc.pod.Spec.Containers[0].Image != tc.wantImage {
				t.Errorf("container image = %q, want %q", tc.pod.Spec.Containers[0].Image, tc.wantImage)
			}
		})
	}
}

// ── setBindingCondition ───────────────────────────────────────────────────────

func TestSetBindingCondition(t *testing.T) {
	scheme := newScheme(t)

	tests := []struct {
		name    string
		claim   *resourceapi.ResourceClaim
		results []resourceapi.DeviceRequestAllocationResult
		wantErr bool
	}{
		{
			name: "appends image-verified condition to Status.Devices",
			claim: newClaim(
				withResult(DeviceRef{
					Driver: "driver.example.com",
					Pool:   "pool-a",
					Device: "device-0",
				}),
			),
			results: []resourceapi.DeviceRequestAllocationResult{
				{
					Driver: "driver.example.com",
					Pool:   "pool-a",
					Device: "device-0",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claim := tc.claim
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(claim).
				WithStatusSubresource(claim).
				Build()

			r := &PodReconciler{Client: c}
			err := r.setBindingCondition(context.Background(), claimBindingResult{
				Claim:   claim,
				Results: tc.results,
			})

			if (err != nil) != tc.wantErr {
				t.Fatalf("setBindingCondition() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if len(claim.Status.Devices) == 0 {
					t.Fatal("claim.Status.Devices is empty after setBindingCondition")
				}
				if got := claim.Status.Devices[0].Conditions[0].Type; got != BindingConditionValidateImage {
					t.Errorf("condition type = %q, want %q", got, BindingConditionValidateImage)
				}
			}
		})
	}
}
