/*
Copyright Google LLC.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/urfave/cli/v2"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/klog/v2"

	configapi "github.com/google/dra-driver-model-cache/api/modelcache.x-k8s.io/v1"
	"github.com/google/dra-driver-model-cache/pkg/flags"
)

type Flags struct {
	loggingConfig *flags.LoggingConfig

	certFile   string
	keyFile    string
	port       int
	driverName string
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
			Name:        "tls-cert-file",
			Usage:       "File containing the default x509 Certificate for HTTPS.",
			Destination: &flags.certFile,
			Required:    true,
		},
		&cli.StringFlag{
			Name:        "tls-private-key-file",
			Usage:       "File containing the default x509 private key matching --tls-cert-file.",
			Destination: &flags.keyFile,
			Required:    true,
		},
		&cli.IntFlag{
			Name:        "port",
			Usage:       "Secure port that the webhook listens on",
			Value:       443,
			Destination: &flags.port,
		},
		&cli.StringFlag{
			Name:        "driver-name",
			Usage:       "Name of the DRA driver.",
			Value:       "modelcache.x-k8s.io",
			Destination: &flags.driverName,
			EnvVars:     []string{"DRIVER_NAME"},
		},
	}
	cliFlags = append(cliFlags, flags.loggingConfig.Flags()...)

	app := &cli.App{
		Name:            "model-cache-webhook",
		Usage:           "model-cache-webhook implements a validating admission webhook for model cache parameters.",
		ArgsUsage:       " ",
		HideHelpCommand: true,
		Flags:           cliFlags,
		Before: func(c *cli.Context) error {
			return flags.loggingConfig.Apply()
		},
		Action: func(c *cli.Context) error {
			mux, err := newMux(flags.driverName)
			if err != nil {
				return fmt.Errorf("create HTTP mux: %w", err)
			}

			server := &http.Server{
				Handler: mux,
				Addr:    fmt.Sprintf(":%d", flags.port),
			}
			klog.Background().Info("starting webhook server", "addr", server.Addr)
			return server.ListenAndServeTLS(flags.certFile, flags.keyFile)
		},
	}

	return app
}

func newMux(driverName string) (*http.ServeMux, error) {
	configScheme := runtime.NewScheme()
	if err := configapi.AddToScheme(configScheme); err != nil {
		return nil, err
	}
	configDecoder := kjson.NewSerializerWithOptions(
		kjson.DefaultMetaFactory, configScheme, configScheme,
		kjson.SerializerOptions{Pretty: true, Strict: true},
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/validate-resource-claim-parameters", serveResourceClaim(configDecoder, validateModelLoader, driverName))
	mux.HandleFunc("/readyz", readyHandler)
	return mux, nil
}

func validateModelLoader(obj runtime.Object) error {
	loader, ok := obj.(*configapi.ModelLoader)
	if !ok {
		return fmt.Errorf("expected v1.ModelLoader but got: %T", obj)
	}
	if loader.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if loader.ModelID == "" {
		return fmt.Errorf("modelId is required")
	}
	return nil
}

func readyHandler(w http.ResponseWriter, req *http.Request) {
	_, _ = w.Write([]byte("ok"))
}

func serveResourceClaim(configDecoder runtime.Decoder, validate func(runtime.Object) error, driverName string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, r.Context(), admitResourceClaimParameters(configDecoder, validate, driverName))
	}
}

func serve(w http.ResponseWriter, r *http.Request, ctx context.Context, admit func(context.Context, admissionv1.AdmissionReview) *admissionv1.AdmissionResponse) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	requestedAdmissionReview := &admissionv1.AdmissionReview{}
	if err := json.Unmarshal(body, requestedAdmissionReview); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	responseAdmissionReview := &admissionv1.AdmissionReview{}
	responseAdmissionReview.SetGroupVersionKind(requestedAdmissionReview.GroupVersionKind())
	responseAdmissionReview.Response = admit(ctx, *requestedAdmissionReview)
	responseAdmissionReview.Response.UID = requestedAdmissionReview.Request.UID

	respBytes, _ := json.Marshal(responseAdmissionReview)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(respBytes)
}

func admitResourceClaimParameters(configDecoder runtime.Decoder, validate func(runtime.Object) error, driverName string) func(context.Context, admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	return func(ctx context.Context, ar admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
		var opaqueParams []runtime.RawExtension
		switch ar.Request.Resource {
		case resourceClaimResourceV1, resourceClaimResourceV1Beta1, resourceClaimResourceV1Beta2:
			claim, err := extractResourceClaim(ar)
			if err != nil {
				return &admissionv1.AdmissionResponse{
					Allowed: false,
					Result:  &metav1.Status{Message: fmt.Sprintf("failed to extract ResourceClaim: %v", err)},
				}
			}
			for _, cfg := range claim.Spec.Devices.Config {
				if cfg.Opaque != nil && cfg.Opaque.Driver == driverName {
					opaqueParams = append(opaqueParams, cfg.Opaque.Parameters)
				}
			}
		case resourceClaimTemplateResourceV1, resourceClaimTemplateResourceV1Beta1, resourceClaimTemplateResourceV1Beta2:
			template, err := extractResourceClaimTemplate(ar)
			if err != nil {
				return &admissionv1.AdmissionResponse{
					Allowed: false,
					Result:  &metav1.Status{Message: fmt.Sprintf("failed to extract ResourceClaimTemplate: %v", err)},
				}
			}
			for _, cfg := range template.Spec.Spec.Devices.Config {
				if cfg.Opaque != nil && cfg.Opaque.Driver == driverName {
					opaqueParams = append(opaqueParams, cfg.Opaque.Parameters)
				}
			}
		default:
			return &admissionv1.AdmissionResponse{Allowed: true}
		}

		for _, raw := range opaqueParams {
			if raw.Raw == nil {
				continue
			}
			obj, _, err := configDecoder.Decode(raw.Raw, nil, nil)
			if err != nil {
				return &admissionv1.AdmissionResponse{
					Allowed: false,
					Result:  &metav1.Status{Message: fmt.Sprintf("failed to decode parameters: %v", err)},
				}
			}
			if err := validate(obj); err != nil {
				return &admissionv1.AdmissionResponse{
					Allowed: false,
					Result:  &metav1.Status{Message: err.Error()},
				}
			}
		}

		return &admissionv1.AdmissionResponse{Allowed: true}
	}
}
