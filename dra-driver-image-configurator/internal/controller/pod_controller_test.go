package controller

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	imagev1alpha1 "github.com/gke-labs/dra-drivers/dra-driver-image-configurator/api/v1alpha1"
)

func createTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = resourceapi.AddToScheme(s)
	_ = imagev1alpha1.AddToScheme(s)
	return s
}

// ── builders ──────────────────────────────────────────────────────────────────

type claimOption func(*resourceapi.ResourceClaim)
type podOption func(*corev1.Pod)

// DeviceRef uniquely identifies a device and optionally declares binding conditions.
// Used as a named-argument style parameter across multiple builder functions.
type DeviceRef struct {
	Request           string
	Driver            string
	Pool              string
	Device            string
	ShareID           *types.UID
	BindingConditions []string
}

// ImageRef specifies a container image override delivered via ImageConfig.
// Source and Driver are used only when the ref is passed to withImageConfig
// (they are ignored by withContainer).
type ImageRef struct {
	Source        string
	Driver        string
	ContainerName string
	Image         string
}

// NameRef identifies a namespaced resource by name and namespace.
type NameRef struct {
	Name      string
	Namespace string
}

// newClaim builds a ResourceClaim with the given name/namespace.
func newClaim(ref NameRef, opts ...claimOption) *resourceapi.ResourceClaim {
	c := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: ref.Namespace},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// withResult appends a device allocation result to the claim.
func withResult(ref DeviceRef) claimOption {
	return func(c *resourceapi.ResourceClaim) {
		if c.Status.Allocation == nil {
			c.Status.Allocation = &resourceapi.AllocationResult{}
		}
		c.Status.Allocation.Devices.Results = append(
			c.Status.Allocation.Devices.Results,
			resourceapi.DeviceRequestAllocationResult{
				Request:           ref.Request,
				Driver:            ref.Driver,
				Pool:              ref.Pool,
				Device:            ref.Device,
				ShareID:           ref.ShareID,
				BindingConditions: ref.BindingConditions,
			},
		)
	}
}

// withDeviceCondition appends an AllocatedDeviceStatus with the given condition.
func withDeviceCondition(ref DeviceRef, condition string, status metav1.ConditionStatus) claimOption {
	return func(c *resourceapi.ResourceClaim) {
		c.Status.Devices = append(c.Status.Devices, resourceapi.AllocatedDeviceStatus{
			Driver: ref.Driver,
			Pool:   ref.Pool,
			Device: ref.Device,
			Conditions: []metav1.Condition{
				{Type: condition, Status: status},
			},
		})
	}
}

// withImageConfig appends an opaque DeviceAllocationConfiguration whose payload
// is an ImageConfig built from ref.ContainerName / ref.Image (JSON-marshaled).
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
		if c.Status.Allocation == nil {
			c.Status.Allocation = &resourceapi.AllocationResult{}
		}
		c.Status.Allocation.Devices.Config = append(
			c.Status.Allocation.Devices.Config,
			resourceapi.DeviceAllocationConfiguration{
				Source: resourceapi.AllocationConfigSource(ref.Source),
				DeviceConfiguration: resourceapi.DeviceConfiguration{
					Opaque: &resourceapi.OpaqueDeviceConfiguration{
						Driver:     ref.Driver,
						Parameters: runtime.RawExtension{Raw: raw},
					},
				},
			},
		)
	}
}

// newPod builds a Pod with the given name/namespace.
func newPod(ref NameRef, opts ...podOption) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: ref.Namespace},
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

// withClaimRef adds a PodResourceClaimStatus referencing the given claim.
func withClaimRef(claimName string) podOption {
	name := claimName
	return func(p *corev1.Pod) {
		p.Status.ResourceClaimStatuses = append(
			p.Status.ResourceClaimStatuses,
			corev1.PodResourceClaimStatus{Name: "ref", ResourceClaimName: &name},
		)
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
			name: "expected condition to be already set",
			claim: newClaim(NameRef{Name: "c", Namespace: "default"},
				withDeviceCondition(
					DeviceRef{Driver: "test-driver", Pool: "test-pool", Device: "test-device"},
					BindingConditionUpdateImage, metav1.ConditionTrue,
				),
			),
			result: &resourceapi.DeviceRequestAllocationResult{
				Driver: "test-driver", Pool: "test-pool", Device: "test-device",
			},
			condition: BindingConditionUpdateImage,
			want:      true,
		},
		{
			name: "Test non-matching condition status",
			claim: newClaim(NameRef{Name: "c", Namespace: "default"},
				withDeviceCondition(
					DeviceRef{Driver: "test-driver", Pool: "test-pool", Device: "test-device"},
					BindingConditionUpdateImage, metav1.ConditionFalse,
				),
			),
			result: &resourceapi.DeviceRequestAllocationResult{
				Driver: "test-driver", Pool: "test-pool", Device: "test-device",
			},
			condition: BindingConditionUpdateImage,
			want:      false,
		},
		{
			name: "Test non-matching device",
			claim: newClaim(NameRef{Name: "c", Namespace: "default"},
				withDeviceCondition(
					DeviceRef{Driver: "test-driver", Pool: "test-pool", Device: "test-device"},
					BindingConditionUpdateImage, metav1.ConditionTrue,
				),
			),
			result: &resourceapi.DeviceRequestAllocationResult{
				Driver: "test-driver", Pool: "test-pool", Device: "other-device",
			},
			condition: BindingConditionUpdateImage,
			want:      false,
		},
		{
			name:  "Test empty devices list",
			claim: &resourceapi.ResourceClaim{},
			result: &resourceapi.DeviceRequestAllocationResult{
				Driver: "test-driver", Pool: "test-pool", Device: "test-device",
			},
			condition: BindingConditionUpdateImage,
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBindingConditionAlreadySet(tc.claim, tc.result, tc.condition); got != tc.want {
				t.Errorf("isBindingConditionAlreadySet = %v, want %v", got, tc.want)
			}
		})
	}
}

// ── collectPendingBindingResults ──────────────────────────────────────────────

func TestCollectPendingBindingResults(t *testing.T) {
	shareID := types.UID("share-123")

	tests := []struct {
		name          string
		claims        []*resourceapi.ResourceClaim
		wantLen       int
		wantClaimName string
		wantDevice    string
		wantShareID   *types.UID
	}{
		{
			name: "returns only claims with pending binding condition not yet satisfied",
			claims: []*resourceapi.ResourceClaim{
				// claim-1: one device requires the binding condition (pending), another has no condition.
				newClaim(NameRef{Name: "claim-1", Namespace: "default"},
					withResult(DeviceRef{
						Request: "req-1", Driver: "test-driver", Pool: "test-pool", Device: "dev-1",
						ShareID:           &shareID,
						BindingConditions: []string{BindingConditionUpdateImage},
					}),
					withResult(DeviceRef{
						Request: "req-other", Driver: "test-driver", Pool: "test-pool", Device: "dev-other",
					}),
				),
				// claim-2: condition is required but already set to True.
				newClaim(NameRef{Name: "claim-2", Namespace: "default"},
					withResult(DeviceRef{
						Request: "req-2", Driver: "test-driver", Pool: "test-pool", Device: "dev-2",
						BindingConditions: []string{BindingConditionUpdateImage},
					}),
					withDeviceCondition(
						DeviceRef{Driver: "test-driver", Pool: "test-pool", Device: "dev-2"},
						BindingConditionUpdateImage, metav1.ConditionTrue,
					),
				),
			},
			wantLen:       1,
			wantClaimName: "claim-1",
			wantDevice:    "dev-1",
			wantShareID:   &shareID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pending := collectPendingBindingResults(tc.claims)

			if len(pending) != tc.wantLen {
				t.Fatalf("expected %d pending claim result, got %d", tc.wantLen, len(pending))
			}
			if pending[0].Claim.Name != tc.wantClaimName {
				t.Errorf("expected pending claim to be %s, got %s", tc.wantClaimName, pending[0].Claim.Name)
			}
			if len(pending[0].Results) != 1 {
				t.Fatalf("expected 1 pending device result, got %d", len(pending[0].Results))
			}
			if pending[0].Results[0].Device != tc.wantDevice {
				t.Errorf("expected pending device to be %s, got %s", tc.wantDevice, pending[0].Results[0].Device)
			}
			if pending[0].Results[0].ShareID == nil || *pending[0].Results[0].ShareID != *tc.wantShareID {
				t.Errorf("expected share ID to be %q, got %v", *tc.wantShareID, pending[0].Results[0].ShareID)
			}
		})
	}
}

// ── collectImageConfigs ───────────────────────────────────────────────────────

func TestCollectImageConfigs(t *testing.T) {
	tests := []struct {
		name              string
		claim             *resourceapi.ResourceClaim
		wantLen           int
		wantContainerName string
		wantImage         string
	}{
		{
			name: "decodes valid ImageConfig and skips invalid/missing entries",
			claim: newClaim(NameRef{Name: "c", Namespace: "default"},
				withImageConfig(t, ImageRef{
					Source:        "test-source",
					Driver:        "test-driver",
					ContainerName: "test-container",
					Image:         "custom-image:v1",
				}),
				// Invalid/incomplete config (empty ContainerName).
				withImageConfig(t, ImageRef{
					Source:        "invalid-source",
					Driver:        "test-driver",
					ContainerName: "",
					Image:         "custom-image:v1",
				}),
				// Missing opaque.
				func(c *resourceapi.ResourceClaim) {
					c.Status.Allocation.Devices.Config = append(
						c.Status.Allocation.Devices.Config,
						resourceapi.DeviceAllocationConfiguration{Source: "other-source"},
					)
				},
			),
			wantLen:           1,
			wantContainerName: "test-container",
			wantImage:         "custom-image:v1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configs := collectImageConfigs([]*resourceapi.ResourceClaim{tc.claim})
			if len(configs) != tc.wantLen {
				t.Fatalf("expected %d image config(s), got %d", tc.wantLen, len(configs))
			}
			if tc.wantLen > 0 {
				if configs[0].ContainerName != tc.wantContainerName {
					t.Errorf("ContainerName = %q, want %q", configs[0].ContainerName, tc.wantContainerName)
				}
				if configs[0].Image != tc.wantImage {
					t.Errorf("Image = %q, want %q", configs[0].Image, tc.wantImage)
				}
			}
		})
	}
}

// ── fetchClaims ───────────────────────────────────────────────────────────────

func TestFetchClaims(t *testing.T) {
	s := createTestScheme()
	claimName := "test-claim"

	pod := newPod(NameRef{Name: "test-pod", Namespace: "default"}, withClaimRef(claimName))

	tests := []struct {
		name    string
		claims  []client.Object
		wantLen int
		wantErr bool
	}{
		{
			name: "fetches allocated claim referenced by pod",
			claims: []client.Object{
				newClaim(NameRef{Name: claimName, Namespace: "default"}, func(c *resourceapi.ResourceClaim) {
					c.Status.Allocation = &resourceapi.AllocationResult{}
				}),
			},
			wantLen: 1,
		},
		{
			name:    "Test claim not found",
			claims:  nil,
			wantErr: true,
		},
		{
			name: "Test claim not allocated",
			claims: []client.Object{
				newClaim(NameRef{Name: claimName, Namespace: "default"}), // no Allocation
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(tc.claims...).Build()
			r := &PodReconciler{Client: fakeClient}

			claims, err := r.fetchClaims(context.Background(), pod)
			if (err != nil) != tc.wantErr {
				t.Fatalf("fetchClaims() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if len(claims) != tc.wantLen {
					t.Fatalf("expected %d claim(s), got %d", tc.wantLen, len(claims))
				}
				if claims[0].Name != claimName {
					t.Errorf("expected claim name %q, got %q", claimName, claims[0].Name)
				}
			}
		})
	}
}

// ── Reconcile ─────────────────────────────────────────────────────────────────

func TestReconcile(t *testing.T) {
	s := createTestScheme()
	claimName := "reconcile-claim"

	tests := []struct {
		name             string
		pod              *corev1.Pod
		claim            *resourceapi.ResourceClaim
		wantImages       []string
		wantConditionTyp string
	}{
		{
			name: "patches container image and sets binding condition",
			pod: newPod(NameRef{Name: "reconcile-pod", Namespace: "test-ns"},
				withContainer(ImageRef{ContainerName: "target-container", Image: "old-image:v1"}),
				withContainer(ImageRef{ContainerName: "other-container", Image: "other-image:v1"}),
				withClaimRef(claimName),
			),
			claim: newClaim(NameRef{Name: claimName, Namespace: "test-ns"},
				withImageConfig(t, ImageRef{
					ContainerName: "target-container",
					Image:         "new-image:v2",
				}),
				withResult(DeviceRef{
					Driver: "test-driver", Pool: "test-pool", Device: "test-device",
					BindingConditions: []string{BindingConditionUpdateImage},
				}),
			),
			wantImages:       []string{"new-image:v2", "other-image:v1"},
			wantConditionTyp: BindingConditionUpdateImage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithObjects(tc.pod, tc.claim).
				WithStatusSubresource(&resourceapi.ResourceClaim{}).
				Build()
			reconciler := &PodReconciler{Client: fakeClient}

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: tc.pod.Namespace,
					Name:      tc.pod.Name,
				},
			}

			// Run Reconcile
			res, err := reconciler.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("Reconcile failed: %v", err)
			}
			if res.Requeue {
				t.Errorf("unexpected Requeue")
			}

			// Verify pod images were updated as expected.
			updatedPod := &corev1.Pod{}
			if err := fakeClient.Get(context.Background(), req.NamespacedName, updatedPod); err != nil {
				t.Fatalf("failed to get updated pod: %v", err)
			}

			for i, want := range tc.wantImages {
				if got := updatedPod.Spec.Containers[i].Image; got != want {
					t.Errorf("container %d image = %q, want %q", i, got, want)
				}
			}

			// Verify ResourceClaim status was updated with the binding condition.
			if tc.wantConditionTyp == "" {
				return
			}
			updatedClaim := &resourceapi.ResourceClaim{}
			if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: tc.claim.Namespace, Name: tc.claim.Name}, updatedClaim); err != nil {
				t.Fatalf("failed to get updated claim: %v", err)
			}

			if len(updatedClaim.Status.Devices) != 1 {
				t.Fatalf("expected 1 allocated device status in claim, got %d", len(updatedClaim.Status.Devices))
			}
			if len(updatedClaim.Status.Devices[0].Conditions) != 1 {
				t.Fatalf("expected 1 condition in allocated device status, got %d", len(updatedClaim.Status.Devices[0].Conditions))
			}
			if updatedClaim.Status.Devices[0].Conditions[0].Type != tc.wantConditionTyp {
				t.Errorf("expected condition type %q, got %q", tc.wantConditionTyp, updatedClaim.Status.Devices[0].Conditions[0].Type)
			}
			if updatedClaim.Status.Devices[0].Conditions[0].Status != metav1.ConditionTrue {
				t.Errorf("expected condition status True, got %v", updatedClaim.Status.Devices[0].Conditions[0].Status)
			}

			// Re-running Reconcile should now do nothing since binding condition is already set.
			res, err = reconciler.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("second Reconcile failed: %v", err)
			}
			if res.Requeue {
				t.Errorf("unexpected Requeue on second Reconcile")
			}
		})
	}
}

func TestReconcile_PodNotFound(t *testing.T) {
	s := createTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	reconciler := &PodReconciler{Client: fakeClient}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "test-ns",
			Name:      "non-existent-pod",
		},
	}

	res, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error for non-existent pod, got %v", err)
	}
	if res.Requeue {
		t.Errorf("unexpected Requeue")
	}
}

func TestReconcile_NoPendingBindingResults(t *testing.T) {
	s := createTestScheme()
	claimName := "claim-no-pending"

	pod := newPod(NameRef{Name: "pod-no-pending", Namespace: "test-ns"},
		withClaimRef(claimName),
	)
	claim := newClaim(NameRef{Name: claimName, Namespace: "test-ns"}, func(c *resourceapi.ResourceClaim) {
		c.Status.Allocation = &resourceapi.AllocationResult{}
	})

	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(pod, claim).Build()
	reconciler := &PodReconciler{Client: fakeClient}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "test-ns",
			Name:      "pod-no-pending",
		},
	}

	res, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Requeue {
		t.Errorf("unexpected Requeue")
	}
}

func TestReconcile_NoImageConfigs(t *testing.T) {
	s := createTestScheme()
	claimName := "claim-no-configs"

	pod := newPod(NameRef{Name: "pod-no-configs", Namespace: "test-ns"},
		withClaimRef(claimName),
	)
	claim := newClaim(NameRef{Name: claimName, Namespace: "test-ns"},
		withResult(DeviceRef{
			Driver:            "test-driver",
			BindingConditions: []string{BindingConditionUpdateImage},
		}),
	)

	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(pod, claim).Build()
	reconciler := &PodReconciler{Client: fakeClient}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "test-ns",
			Name:      "pod-no-configs",
		},
	}

	res, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Requeue {
		t.Errorf("unexpected Requeue")
	}
}
