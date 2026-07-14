package v1alpha1

import (
	"fmt"

	"github.com/distribution/reference"
	"k8s.io/apimachinery/pkg/runtime"
)

func DecodeImageConfig(raw []byte, decoder runtime.Decoder) (*ImageConfig, error) {
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

func (ic *ImageConfig) Validate() error {
	if ic.ContainerName == "" || ic.Image == "" {
		return fmt.Errorf("ContainerName or Image empty")
	}
	if _, err := reference.ParseNormalizedNamed(ic.Image); err != nil {
		return fmt.Errorf("invalid image reference %q: %w", ic.Image, err)
	}
	return nil
}
