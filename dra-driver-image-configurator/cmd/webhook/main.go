package main

import (
	"os"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	webhookvalidation "github.com/gke-labs/dra-drivers/dra-driver-image-configurator/internal/webhook"
)

func main() {
	ctrl.SetLogger(zap.New())
	log := ctrl.Log.WithName("setup")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		log.Error(err, "unable to create manager")
		os.Exit(1)
	}

	webhookServer := mgr.GetWebhookServer()
	webhookServer.Register("/validate-resourceclaim", &admission.Webhook{
		Handler: &webhookvalidation.ResourceClaimValidator{},
	})
	webhookServer.Register("/validate-resourceclaimtemplate", &admission.Webhook{
		Handler: &webhookvalidation.ResourceClaimTemplateValidator{},
	})

	log.Info("starting webhook server")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "webhook server exited")
		os.Exit(1)
	}
}
