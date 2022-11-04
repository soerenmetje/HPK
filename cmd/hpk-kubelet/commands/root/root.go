// Copyright © 2022 FORTH-ICS
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
// limitations under the License.

package root

import (
	"context"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/carv-ics-forth/hpk/cmd/hpk-kubelet/commands"
	"github.com/carv-ics-forth/hpk/compute"
	"github.com/carv-ics-forth/hpk/pkg/resourcemanager"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/carv-ics-forth/hpk/provider"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// NewCommand creates a new top-level command.
// This command is used to start the virtual-kubelet daemon
func NewCommand(ctx context.Context, name string, c Opts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   name,
		Short: name + " provides a virtual kubelet interface for your kubernetes cluster.",
		Long: name + ` implements the Kubelet interface with a pluggable
backend implementation allowing users to create kubernetes nodes without running the kubelet.
This allows users to schedule kubernetes workloads on nodes that aren't running Kubernetes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if c.PodSyncWorkers == 0 {
				return errors.Errorf("pod sync workers must be greater than 0")
			}

			return runRootCommand(ctx, c)
		},
	}

	installFlags(cmd.Flags(), &c)
	return cmd
}

func runRootCommand(ctx context.Context, c Opts) error {
	log := zap.New(zap.UseDevMode(true)).WithValues(
		"node", c.NodeName,
		"watchedNamespace", c.KubeNamespace,
	)

	/*---------------------------------------------------
	 * Starting Kubernetes Client
	 *---------------------------------------------------*/
	log.Info(" Starting Kubernetes Client")

	// Config precedence
	//
	// * --kubeconfig flag pointing at a file
	//
	// * KUBECONFIG environment variable pointing at a file
	//
	// * In-cluster config if running in cluster
	//
	// * $HOME/.kube/config if exists.
	cfg, err := config.GetConfig()
	if err != nil {
		return errors.Wrapf(err, "unable to get kubeconfig")
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return errors.Wrapf(err, "unable to start kubernetes client")
	}

	/*---------------------------------------------------
	 * Load Kubernetes Informers
	 *---------------------------------------------------*/
	log.Info("Load Kubernetes Informers")

	// Create a shared informer factory for Kubernetes pods in the current namespace (if specified) and scheduled to the current node.
	podInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(
		client,
		c.InformerResyncPeriod,
		kubeinformers.WithNamespace(c.KubeNamespace),
		kubeinformers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", c.NodeName).String()
		}))

	// Create another shared informer factory for Kubernetes secrets and configmaps (not subject to any selectors).
	podInformer := podInformerFactory.Core().V1().Pods()

	scmInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(client, c.InformerResyncPeriod)
	secretInformer := scmInformerFactory.Core().V1().Secrets()
	configMapInformer := scmInformerFactory.Core().V1().ConfigMaps()
	serviceInformer := scmInformerFactory.Core().V1().Services()
	serviceAccountInformer := scmInformerFactory.Core().V1().ServiceAccounts()

	rm, err := resourcemanager.NewResourceManager(podInformer.Lister(),
		secretInformer.Lister(),
		configMapInformer.Lister(),
		serviceInformer.Lister(),
		serviceAccountInformer.Lister(),
	)
	if err != nil {
		return errors.Wrap(err, "could not create resource manager")
	}

	// Start the informers now, so the provider will get a functional resource manager.
	podInformerFactory.Start(ctx.Done())
	scmInformerFactory.Start(ctx.Done())

	/*---------------------------------------------------
	 * Discover Kubernetes DNS server
	 *---------------------------------------------------*/
	log.Info("Discover Kubernetes DNS server")

	dnsEndpoint, err := client.CoreV1().Endpoints("kube-system").Get(ctx, "kube-dns", metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "unable to discover dns server")
	}

	if len(dnsEndpoint.Subsets) == 0 {
		return errors.Wrapf(err, "empty dns subsets")
	}

	if len(dnsEndpoint.Subsets[0].Addresses) == 0 {
		return errors.Wrapf(err, "empty dns addresses")
	}

	dnsIP := dnsEndpoint.Subsets[0].Addresses[0]

	log.Info("DNSServer Discovered", "ip", dnsIP)

	/*---------------------------------------------------
	 * Register the Provisioner of Virtual Nodes
	 *---------------------------------------------------*/
	log.Info("Register the Provisioner of Virtual Nodes")

	compute.ContainerRegistry = c.ContainerRegistry
	compute.KubeDNSIPAddress = dnsIP.IP

	newProvider, err := provider.NewProvider(provider.InitConfig{
		NodeName:        c.NodeName,
		InternalIP:      envOr("VKUBELET_POD_IP", "127.0.0.1"),
		DaemonPort:      c.ListenPort,
		ResourceManager: rm,
		BuildVersion:    commands.BuildVersion,
	})
	if err != nil {
		return err
	}

	/*---------------------------------------------------
	 * Start an HTTPs server for serving metrics/logs
	 *---------------------------------------------------*/
	log.Info("Initialize HTTPS server")
	{
		cancelHTTP, err := setupHTTPServer(ctx, newProvider, &apiServerConfig{
			CertPath:    os.Getenv("APISERVER_CERT_LOCATION"),
			KeyPath:     os.Getenv("APISERVER_KEY_LOCATION"),
			Addr:        fmt.Sprintf(":%d", c.ListenPort),
			MetricsAddr: "",
		})
		if err != nil {
			return errors.Wrapf(err, "unable to start http server")
		}

		defer cancelHTTP()
	}

	/*---------------------------------------------------
	 * Register a new Virtual Node
	 *---------------------------------------------------*/
	log.Info("Register a new Virtual Node")

	var taint *corev1.Taint
	if !c.DisableTaint {
		taint, err = getTaint(c)
		if err != nil {
			return err
		}
	}

	pNode := newProvider.CreateVirtualNode(ctx, c.NodeName, taint)

	// activate fs notifier
	nodeControllerOpts := []node.NodeControllerOpt{
		node.WithNodeStatusUpdateErrorHandler(func(ctx context.Context, err error) error {
			if !k8serrors.IsNotFound(err) {
				return err
			}

			log.V(0).Info("node not found")
			newNode := pNode.DeepCopy()
			newNode.ResourceVersion = ""

			if _, err := client.CoreV1().Nodes().Create(ctx, newNode, metav1.CreateOptions{}); err != nil {
				return err
			}

			log.V(0).Info("created new node")
			return nil
		}),
	}

	if c.EnableNodeLease {
		leaseClient := client.CoordinationV1().Leases(corev1.NamespaceNodeLease)
		nodeControllerOpts = append(nodeControllerOpts, node.WithNodeEnableLeaseV1(leaseClient, 0))
	}

	nodeController, err := node.NewNodeController(
		node.NaiveNodeProvider{},
		pNode,
		client.CoreV1().Nodes(),
		nodeControllerOpts...,
	)
	if err != nil {
		return errors.Wrap(err, "cannot start node controller")
	}

	eb := record.NewBroadcaster()
	eb.StartLogging(logrus.Infof)
	eb.StartRecordingToSink(&corev1client.EventSinkImpl{Interface: client.CoreV1().Events(c.KubeNamespace)})

	/*---------------------------------------------------
	 * Start the controller for the Virtual Node
	 *---------------------------------------------------*/
	log.Info("Start the controller for the Virtual Node")

	podController, err := node.NewPodController(node.PodControllerConfig{
		PodClient:         client.CoreV1(),
		PodInformer:       podInformer,
		EventRecorder:     eb.NewRecorder(scheme.Scheme, corev1.EventSource{Component: path.Join(pNode.Name, "pod-controller")}),
		Provider:          newProvider,
		SecretInformer:    secretInformer,
		ConfigMapInformer: configMapInformer,
		ServiceInformer:   serviceInformer,
	})
	if err != nil {
		return errors.Wrap(err, "error setting up pod controller")
	}

	go func() {
		if err := podController.Run(ctx, c.PodSyncWorkers); err != nil && errors.Cause(err) != context.Canceled {
			log.Error(err, "pod controller failed")
			os.Exit(-1)
		}
	}()

	if c.StartupTimeout > 0 {
		// If there is a startup timeout, it does two things:
		// 1. It causes the VK to shut down if we haven't gotten into an operational state in a time period
		// 2. It prevents node advertisement from happening until we're in an operational state
		err = waitFor(ctx, c.StartupTimeout, podController.Ready())
		if err != nil {
			return err
		}
	}

	return nodeController.Run(ctx)
}

func waitFor(ctx context.Context, time time.Duration, ready <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(ctx, time)
	defer cancel()

	// Wait for the VK / PC close the ready channel, or time out and return
	logrus.Warn("Waiting for pod controller / VK to be ready")

	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "Error while starting up VK")
	}
}

func envOr(name, alt string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}

	return alt
}