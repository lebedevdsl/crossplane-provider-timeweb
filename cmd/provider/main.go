/*
Copyright 2026 Dmitry Lebedev.

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

// Command provider-timeweb is the Timeweb Crossplane provider binary.
// It runs as a long-lived Kubernetes controller: watches the published
// CRDs, reconciles them against the Timeweb Cloud API, and publishes
// connection details as Kubernetes Secrets.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/lebedevdsl/crossplane-provider-timeweb/apis"
	containerregistryctrl "github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/containerregistry"
	projectctrl "github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/project"
	providerconfigctrl "github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/providerconfig"
	s3bucketctrl "github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/s3bucket"
	sshkeyctrl "github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/sshkey"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/version"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apis.AddToScheme(scheme))
}

func main() {
	var (
		debug               bool
		leaderElection      bool
		metricsAddr         string
		probeAddr           string
		syncPeriod          time.Duration
		pollInterval        time.Duration
		presetSyncInterval  time.Duration
		presetTargetNS      string
		printVersionAndExit bool
	)

	flag.BoolVar(&debug, "debug", false, "Enable debug logging.")
	flag.BoolVar(&leaderElection, "leader-election", true, "Enable leader election to ensure only one provider replica reconciles.")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "Bind address for the /metrics endpoint.")
	flag.StringVar(&probeAddr, "health-probe-addr", ":8081", "Bind address for the /healthz and /readyz endpoints.")
	flag.DurationVar(&syncPeriod, "sync-period", time.Hour, "Manager-level cache resync period.")
	flag.DurationVar(&pollInterval, "poll-interval", time.Minute, "Default reconcile poll interval for managed resources.")
	flag.DurationVar(&presetSyncInterval, "preset-sync-interval", 30*time.Minute, "Interval for polling Timeweb catalog endpoints (e.g. ContainerRegistryPreset).")
	flag.StringVar(&presetTargetNS, "preset-target-namespace", "crossplane-system", "Namespace where observe-only catalog CRDs are written.")
	flag.BoolVar(&printVersionAndExit, "version", false, "Print the provider version and exit.")
	flag.Parse()

	if printVersionAndExit {
		fmt.Println(version.Version)
		os.Exit(0)
	}

	// Structured zap logger via crossplane-runtime's logging wrapper.
	level := zapcore.InfoLevel
	if debug {
		level = zapcore.DebugLevel
	}
	zapLog := zap.New(zap.UseDevMode(debug), zap.Level(level))
	ctrl.SetLogger(zapLog)
	log := logging.NewLogrLogger(zapLog.WithName("provider-timeweb"))

	log.Info("starting provider-timeweb",
		"version", version.Version,
		"leader-election", leaderElection,
		"poll-interval", pollInterval,
	)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElection,
		LeaderElectionID:       "provider-timeweb-leader.timeweb.crossplane.io",
	})
	if err != nil {
		log.Info("unable to construct manager", "error", err.Error())
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Info("unable to set up healthz", "error", err.Error())
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Info("unable to set up readyz", "error", err.Error())
		os.Exit(1)
	}

	if err := providerconfigctrl.Setup(mgr, log); err != nil {
		log.Info("unable to register ProviderConfig controller", "error", err.Error())
		os.Exit(1)
	}
	if err := projectctrl.Setup(mgr, log, pollInterval); err != nil {
		log.Info("unable to register Project controller", "error", err.Error())
		os.Exit(1)
	}
	if err := sshkeyctrl.Setup(mgr, log, pollInterval); err != nil {
		log.Info("unable to register SshKey controller", "error", err.Error())
		os.Exit(1)
	}
	if err := s3bucketctrl.Setup(mgr, log, pollInterval); err != nil {
		log.Info("unable to register S3Bucket controller", "error", err.Error())
		os.Exit(1)
	}
	if err := containerregistryctrl.SetupAll(mgr, log, containerregistryctrl.SetupOptions{
		PollInterval:       pollInterval,
		PresetSyncInterval: presetSyncInterval,
		PresetNamespace:    presetTargetNS,
		PresetPCName:       "default",
	}); err != nil {
		log.Info("unable to register ContainerRegistry controllers", "error", err.Error())
		os.Exit(1)
	}

	log.Info("manager starting")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Info("manager exited with error", "error", err.Error())
		os.Exit(1)
	}
}
