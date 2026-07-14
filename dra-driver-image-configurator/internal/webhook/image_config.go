package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	resourceapi "k8s.io/api/resource/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	imagev1alpha1 "github.com/gke-labs/dra-drivers/dra-driver-image-configurator/api/v1alpha1"
	controller "github.com/gke-labs/dra-drivers/dra-driver-image-configurator/internal/controller"
)

type ResourceClaimValidator struct{}

func (v *ResourceClaimValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	claim := &resourceapi.ResourceClaim{}
	if err := json.Unmarshal(req.Object.Raw, claim); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decode ResourceClaim: %w", err))
	}
	if err := validateClaimConfigs(claim.Spec.Devices.Config); err != nil {
		return admission.Denied(err.Error())
	}
	return admission.Allowed("")
}

type ResourceClaimTemplateValidator struct{}

func (v *ResourceClaimTemplateValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	template := &resourceapi.ResourceClaimTemplate{}
	if err := json.Unmarshal(req.Object.Raw, template); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decode ResourceClaimTemplate: %w", err))
	}
	if err := validateClaimConfigs(template.Spec.Spec.Devices.Config); err != nil {
		return admission.Denied(err.Error())
	}
	return admission.Allowed("")
}

func validateClaimConfigs(configs []resourceapi.DeviceClaimConfiguration) error {
	decoder := imagev1alpha1.Codec.UniversalDeserializer()
	for _, cfg := range configs {
		if cfg.Opaque == nil || cfg.Opaque.Driver != controller.DriverName {
			continue
		}
		if cfg.Opaque.Parameters.Raw == nil {
			continue
		}
		ic, err := imagev1alpha1.DecodeImageConfig(cfg.Opaque.Parameters.Raw, decoder)
		if err != nil {
			return err
		}
		if err := ic.Validate(); err != nil {
			return err
		}
	}
	return nil
}
