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
	claim1 := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-1", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{
							Request: "req-1",
							Driver:  "test-driver",
							Pool:    "test-pool",
							Device:  "dev-1",
							ShareID: &shareID,
							BindingConditions: []string{
								BindingConditionUpdateImage,
							},
						},
						{
							// No matching binding condition
							Request: "req-other",
							Driver:  "test-driver",
							Pool:    "test-pool",
							Device:  "dev-other",
						},
					},
				},
			},
		},
	}

	// claim2 has condition already set
	claim2 := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-2", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{
							Request: "req-2",
							Driver:  "test-driver",
							Pool:    "test-pool",
							Device:  "dev-2",
							BindingConditions: []string{
								BindingConditionUpdateImage,
							},
						},
					},
				},
			},
			Devices: []resourceapi.AllocatedDeviceStatus{
				{
					Driver: "test-driver",
					Pool:   "test-pool",
					Device: "dev-2",
					Conditions: []metav1.Condition{
						{
							Type:   BindingConditionUpdateImage,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
		},
	}

	claims := []*resourceapi.ResourceClaim{claim1, claim2}
	pending := collectPendingBindingResults(claims)

	if len(pending) != 1 {
		t.Fatalf("expected 1 pending claim result, got %d", len(pending))
	}
	if pending[0].Claim.Name != "claim-1" {
		t.Errorf("expected pending claim to be claim-1, got %s", pending[0].Claim.Name)
	}
	if len(pending[0].Results) != 1 {
		t.Fatalf("expected 1 pending device result, got %d", len(pending[0].Results))
	}
	if pending[0].Results[0].Device != "dev-1" {
		t.Errorf("expected pending device to be dev-1, got %s", pending[0].Results[0].Device)
	}
	if pending[0].Results[0].ShareID == nil || *pending[0].Results[0].ShareID != shareID {
		t.Errorf("expected share ID to be %q, got %v", shareID, pending[0].Results[0].ShareID)
	}
}

// ── collectImageConfigs ───────────────────────────────────────────────────────

func TestCollectImageConfigs(t *testing.T) {
	ic := &imagev1alpha1.ImageConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "image-configurator.x-k8s.io/v1alpha1",
			Kind:       "ImageConfig",
		},
		ContainerName: "test-container",
		Image:         "custom-image:v1",
	}
	rawBytes, err := json.Marshal(ic)
	if err != nil {
		t.Fatalf("failed to marshal ImageConfig: %v", err)
	}

	invalidJSON := []byte(`{"apiVersion": "image-configurator.x-k8s.io/v1alpha1", "kind": "ImageConfig", "containerName": ""}`)

	claim := &resourceapi.ResourceClaim{
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Config: []resourceapi.DeviceAllocationConfiguration{
						{
							Source: "test-source",
							DeviceConfiguration: resourceapi.DeviceConfiguration{
								Opaque: &resourceapi.OpaqueDeviceConfiguration{
									Driver: "test-driver",
									Parameters: runtime.RawExtension{
										Raw: rawBytes,
									},
								},
							},
						},
						{
							// Invalid/incomplete config
							Source: "invalid-source",
							DeviceConfiguration: resourceapi.DeviceConfiguration{
								Opaque: &resourceapi.OpaqueDeviceConfiguration{
									Driver: "test-driver",
									Parameters: runtime.RawExtension{
										Raw: invalidJSON,
									},
								},
							},
						},
						{
							// Missing opaque
							Source: "other-source",
						},
					},
				},
			},
		},
	}

	configs := collectImageConfigs([]*resourceapi.ResourceClaim{claim})
	if len(configs) != 1 {
		t.Fatalf("expected 1 image config, got %d", len(configs))
	}

	if configs[0].ContainerName != "test-container" {
		t.Errorf("expected ContainerName 'test-container', got %q", configs[0].ContainerName)
	}
	if configs[0].Image != "custom-image:v1" {
		t.Errorf("expected Image 'custom-image:v1', got %q", configs[0].Image)
	}
}

// ── fetchClaims ───────────────────────────────────────────────────────────────

func TestFetchClaims(t *testing.T) {
	s := createTestScheme()

	claimName := "test-claim"
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: "default",
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			ResourceClaimStatuses: []corev1.PodResourceClaimStatus{
				{
					Name:              "claim-ref",
					ResourceClaimName: &claimName,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(claim).Build()
	reconciler := &PodReconciler{Client: fakeClient}

	claims, err := reconciler.fetchClaims(context.Background(), pod)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("expected 1 claim, got %d", len(claims))
	}
	if claims[0].Name != claimName {
		t.Errorf("expected claim name %q, got %q", claimName, claims[0].Name)
	}

	// Test claim not found
	emptyClient := fake.NewClientBuilder().WithScheme(s).Build()
	reconciler.Client = emptyClient
	_, err = reconciler.fetchClaims(context.Background(), pod)
	if err == nil {
		t.Errorf("expected error when claim is not found")
	}

	// Test claim not allocated
	unallocatedClaim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: "default",
		},
	}
	fakeClient = fake.NewClientBuilder().WithScheme(s).WithObjects(unallocatedClaim).Build()
	reconciler.Client = fakeClient
	_, err = reconciler.fetchClaims(context.Background(), pod)
	if err == nil {
		t.Errorf("expected error when claim is not allocated")
	}
}

// ── Reconcile ─────────────────────────────────────────────────────────────────

func TestReconcile(t *testing.T) {
	s := createTestScheme()

	ic := &imagev1alpha1.ImageConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "image-configurator.x-k8s.io/v1alpha1",
			Kind:       "ImageConfig",
		},
		ContainerName: "target-container",
		Image:         "new-image:v2",
	}
	rawBytes, _ := json.Marshal(ic)

	claimName := "reconcile-claim"
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: "test-ns",
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Config: []resourceapi.DeviceAllocationConfiguration{
						{
							DeviceConfiguration: resourceapi.DeviceConfiguration{
								Opaque: &resourceapi.OpaqueDeviceConfiguration{
									Parameters: runtime.RawExtension{Raw: rawBytes},
								},
							},
						},
					},
					Results: []resourceapi.DeviceRequestAllocationResult{
						{
							Driver: "test-driver",
							Pool:   "test-pool",
							Device: "test-device",
							BindingConditions: []string{
								BindingConditionUpdateImage,
							},
						},
					},
				},
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "reconcile-pod",
			Namespace: "test-ns",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "target-container",
					Image: "old-image:v1",
				},
				{
					Name:  "other-container",
					Image: "other-image:v1",
				},
			},
		},
		Status: corev1.PodStatus{
			ResourceClaimStatuses: []corev1.PodResourceClaimStatus{
				{
					Name:              "ref",
					ResourceClaimName: &claimName,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(pod, claim).
		WithStatusSubresource(&resourceapi.ResourceClaim{}).
		Build()

	reconciler := &PodReconciler{Client: fakeClient}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "test-ns",
			Name:      "reconcile-pod",
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

	// Verify pod image was updated
	updatedPod := &corev1.Pod{}
	if err := fakeClient.Get(context.Background(), req.NamespacedName, updatedPod); err != nil {
		t.Fatalf("failed to get updated pod: %v", err)
	}

	if updatedPod.Spec.Containers[0].Image != "new-image:v2" {
		t.Errorf("expected container 0 image to be 'new-image:v2', got %q", updatedPod.Spec.Containers[0].Image)
	}
	if updatedPod.Spec.Containers[1].Image != "other-image:v1" {
		t.Errorf("expected container 1 image to remain 'other-image:v1', got %q", updatedPod.Spec.Containers[1].Image)
	}

	// Verify ResourceClaim status was updated with the binding condition
	updatedClaim := &resourceapi.ResourceClaim{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: claimName}, updatedClaim); err != nil {
		t.Fatalf("failed to get updated claim: %v", err)
	}

	if len(updatedClaim.Status.Devices) != 1 {
		t.Fatalf("expected 1 allocated device status in claim, got %d", len(updatedClaim.Status.Devices))
	}
	if len(updatedClaim.Status.Devices[0].Conditions) != 1 {
		t.Fatalf("expected 1 condition in allocated device status, got %d", len(updatedClaim.Status.Devices[0].Conditions))
	}
	if updatedClaim.Status.Devices[0].Conditions[0].Type != BindingConditionUpdateImage {
		t.Errorf("expected condition type %q, got %q", BindingConditionUpdateImage, updatedClaim.Status.Devices[0].Conditions[0].Type)
	}
	if updatedClaim.Status.Devices[0].Conditions[0].Status != metav1.ConditionTrue {
		t.Errorf("expected condition status True, got %v", updatedClaim.Status.Devices[0].Conditions[0].Status)
	}

	// Re-running Reconcile should now do nothing since binding condition is already set
	res, err = reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}
	if res.Requeue {
		t.Errorf("unexpected Requeue on second Reconcile")
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
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: "test-ns",
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-no-pending",
			Namespace: "test-ns",
		},
		Status: corev1.PodStatus{
			ResourceClaimStatuses: []corev1.PodResourceClaimStatus{
				{
					Name:              "ref",
					ResourceClaimName: &claimName,
				},
			},
		},
	}

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
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: "test-ns",
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{
							Driver: "test-driver",
							BindingConditions: []string{
								BindingConditionUpdateImage,
							},
						},
					},
				},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-no-configs",
			Namespace: "test-ns",
		},
		Status: corev1.PodStatus{
			ResourceClaimStatuses: []corev1.PodResourceClaimStatus{
				{
					Name:              "ref",
					ResourceClaimName: &claimName,
				},
			},
		},
	}

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
