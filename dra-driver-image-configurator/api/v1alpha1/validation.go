package v1alpha1

import (
	"fmt"

	"github.com/distribution/reference"
	resourceapi "k8s.io/api/resource/v1"
)

func DecodeAndValidateOpaque(cfg *resourceapi.OpaqueDeviceConfiguration) (*ImageConfig, error) {
	if cfg == nil || cfg.Driver != DriverName || cfg.Parameters.Raw == nil {
		return nil, nil
	}
	ic, err := decodeImageConfig(cfg.Parameters.Raw)
	if err != nil {
		return nil, fmt.Errorf("Opaque parameter decode failure: %w", err)
	}
	if err := ic.validate(); err != nil {
		return nil, err
	}
	return ic, nil
}

func decodeImageConfig(raw []byte) (*ImageConfig, error) {
	obj, _, err := decoder.Decode(raw, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ImageConfig parameters: %w", err)
	}
	ic, ok := obj.(*ImageConfig)
	if !ok {
		return nil, fmt.Errorf("unexpected type in ImageConfig parameters: %T", obj)
	}
	return ic, nil
}

func (ic *ImageConfig) validate() error {
	if ic.ContainerName == "" || ic.Image == "" {
		return fmt.Errorf("ContainerName or Image empty")
	}
	if _, err := reference.ParseNormalizedNamed(ic.Image); err != nil {
		return fmt.Errorf("invalid image reference %q: %w", ic.Image, err)
	}
	return nil
}
