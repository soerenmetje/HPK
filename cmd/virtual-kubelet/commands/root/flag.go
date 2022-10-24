// Copyright © 2021 FORTH-ICS
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.package main

package root

import (
	"time"

	"github.com/carv-ics-forth/knoc/api"
	"github.com/spf13/pflag"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	corev1 "k8s.io/api/core/v1"
)

// Opts stores all the options for configuring the root virtual-kubelet command.
// It is used for setting flag values.
//
// You can set the default options by creating a new `Opts` struct and passing
// it into `SetDefaultOpts`
type Opts struct {
	// Namespace to watch for pods and other resources
	KubeNamespace string
	// Sets the port to listen for requests from the Kubernetes API server
	ListenPort int32

	// Node name to use when creating a node in Kubernetes
	NodeName string

	ProviderConfigPath string

	MetricsAddr string

	// Number of workers to use to handle pod notifications
	PodSyncWorkers       int
	InformerResyncPeriod time.Duration

	// Use node leases when supported by Kubernetes (instead of node status updates)
	EnableNodeLease bool

	// Startup Timeout is how long to wait for the kubelet to start
	StartupTimeout time.Duration

	DisableTaint bool
	TaintKey     string
	TaintValue   string
	TaintEffect  string
}

func installFlags(flags *pflag.FlagSet, c *Opts) {
	flags.StringVar(&c.KubeNamespace, "namespace", api.DefaultKubeNamespace, "kubernetes namespace (default is 'all')")

	flags.StringVar(&c.NodeName, "nodename", api.DefaultNodeName, "kubernetes node name")

	flags.StringVar(&c.ProviderConfigPath, "provider-config", "", "HPC provider configuration file")
	flags.StringVar(&c.MetricsAddr, "metrics-addr", api.DefaultMetricsAddr, "address to listen for metrics/stats requests")

	flags.IntVar(&c.PodSyncWorkers, "pod-sync-workers", api.DefaultPodSyncWorkers, `set the number of pod synchronization workers`)
	flags.BoolVar(&c.EnableNodeLease, "enable-node-lease", true, `use node leases (1.13) for node heartbeats`)

	flags.DurationVar(&c.InformerResyncPeriod, "full-resync-period", api.DefaultInformerResyncPeriod, "how often to perform a full resync of pods between kubernetes and the provider")
	flags.DurationVar(&c.StartupTimeout, "startup-timeout", 0, "How long to wait for the virtual-kubelet to start")

	flags.BoolVar(&c.DisableTaint, "disable-taint", false, "disable the virtual-kubelet node taint")

	flags.StringVar(&c.TaintKey, "taint-key", api.DefaultTaintKey, "Set node taint key")
	flags.StringVar(&c.TaintValue, "taint-value", api.DefaultTaintValue, "Set node taint value")
	flags.StringVar(&c.TaintEffect, "taint-effect", api.DefaultTaintEffect, "Set node taint effect")
}

// getTaint creates a taint using the provided key/value.
// Taint effect is read from the environment
// The taint key/value may be overwritten by the environment.
func getTaint(o Opts) (*corev1.Taint, error) {
	var effect corev1.TaintEffect
	switch o.TaintEffect {
	case "NoSchedule":
		effect = corev1.TaintEffectNoSchedule
	case "NoExecute":
		effect = corev1.TaintEffectNoExecute
	case "PreferNoSchedule":
		effect = corev1.TaintEffectPreferNoSchedule
	default:
		return nil, errdefs.InvalidInputf("taint effect %q is not supported", o.TaintEffect)
	}

	return &corev1.Taint{
		Key:    o.TaintKey,
		Value:  o.TaintValue,
		Effect: effect,
	}, nil
}