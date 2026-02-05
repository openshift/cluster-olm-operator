package main

import (
	"context"
	"errors"
	goflag "flag"
	"fmt"
	"os"
	"time"

	configv1 "github.com/openshift/api/config/v1"

	_ "github.com/openshift/api/operator/v1/zz_generated.crd-manifests"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configv1helpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/cli"
	utilflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	ocv1 "github.com/operator-framework/operator-controller/api/v1"

	"github.com/openshift/cluster-olm-operator/internal/utils"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/cluster-olm-operator/pkg/controller"
	"github.com/openshift/cluster-olm-operator/pkg/version"
)

const (
	olmProxyController = "OLMProxyController"
	assetPath          = "/operand-assets"
	manifestsPath      = "/manifests"
)

func main() {
	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	klog.InitFlags(goflag.CommandLine)

	command := newRootCommand()
	code := cli.Run(command)
	os.Exit(code)
}

func newRootCommand() *cobra.Command {
	var versionFlag bool

	cmd := &cobra.Command{
		Use:   "cluster-olm-operator",
		Short: "OpenShift Cluster OLM Operator",
		Run: func(cmd *cobra.Command, _ []string) {
			if versionFlag {
				fmt.Println(version.Get())
				os.Exit(0)
			}
			if err := cmd.Help(); err != nil {
				fmt.Println("Error displaying help:", err)
				os.Exit(1)
			}
		},
	}
	cmd.PersistentFlags().BoolVarP(&versionFlag, "version", "V", false, "Print the version number and exit")
	cmd.AddCommand(newStartCommand())
	return cmd
}

func newStartCommand() *cobra.Command {
	cmd := controllercmd.NewControllerCommandConfig(
		"cluster-olm-operator",
		version.Get(),
		runOperator,
		clock.RealClock{},
	).NewCommandWithContext(context.Background())
	cmd.Use = "start"
	cmd.Short = "Start the Cluster OLM Operator"
	return cmd
}

// configClientWrapper wraps ConfigV1Interface to intercept ClusterOperator status updates
type configClientWrapper struct {
	configv1client.ConfigV1Interface
	coClient       configv1client.ClusterOperatorInterface
	releaseVersion string
	clock          clock.PassiveClock
}

// ClusterOperators returns wrapped ClusterOperatorInterface
func (w *configClientWrapper) ClusterOperators() configv1client.ClusterOperatorInterface {
	return &coWrapper{w.coClient, w.releaseVersion, w.clock}
}

// coWrapper wraps ClusterOperatorInterface to intercept UpdateStatus calls
type coWrapper struct {
	configv1client.ClusterOperatorInterface
	releaseVersion string
	clock          clock.PassiveClock
}

func (w *coWrapper) UpdateStatus(ctx context.Context, co *configv1.ClusterOperator, opts metav1.UpdateOptions) (*configv1.ClusterOperator, error) {
	if w.releaseVersion != "" {
		// Get current ClusterOperator to compare versions
		if original, err := w.ClusterOperatorInterface.Get(ctx, co.Name, metav1.GetOptions{}); err == nil {
			// Check if RELEASE_VERSION exists in ClusterOperator.Status.Versions
			for _, v := range original.Status.Versions {
				if v.Version == w.releaseVersion {
					// Version matches, and so we are not in an upgrade
					return w.ClusterOperatorInterface.UpdateStatus(ctx, co, opts)
				}
			}
			// If RELEASE_VERSION not found, then we are in an upgrade, and so set Progressing to True
			klog.Infof("Version change detected, setting Progressing=True for version %s", w.releaseVersion)
			configv1helpers.SetStatusCondition(&co.Status.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Reason:  "UpgradeInProgress",
				Message: fmt.Sprintf("Progressing towards operator version %s", w.releaseVersion),
			}, w.clock)
		}
	}

	return w.ClusterOperatorInterface.UpdateStatus(ctx, co, opts)
}

func runOperator(ctx context.Context, cc *controllercmd.ControllerContext) error {
	cl, err := clients.New(cc)
	if err != nil {
		return err
	}

	log := klog.FromContext(ctx)

	fg, err := cl.ConfigClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to retrieve featureSet: %w", err)
	}

	clusterCatalogGvk := ocv1.GroupVersion.WithKind("ClusterCatalog")
	cb := controller.Builder{
		Assets:            assetPath,
		Clients:           cl,
		ControllerContext: cc,
		KnownRESTMappings: map[schema.GroupVersionKind]*meta.RESTMapping{
			clusterCatalogGvk: {
				Resource:         ocv1.GroupVersion.WithResource("clustercatalogs"),
				GroupVersionKind: clusterCatalogGvk,
				Scope:            meta.RESTScopeRoot,
			},
		},
		FeatureGate: *fg,
	}

	staticResourceControllers, deploymentControllers, clusterCatalogControllers, relatedObjects, err := cb.BuildControllers("catalogd", "operator-controller")
	if err != nil {
		return err
	}

	namespaces := sets.New[string]()
	for _, obj := range relatedObjects {
		namespaces.Insert(obj.Namespace)
	}

	cl.KubeInformersForNamespaces = v1helpers.NewKubeInformersForNamespaces(cl.KubeClient, namespaces.UnsortedList()...)

	controllerNames := make([]string, 0, len(staticResourceControllers)+len(deploymentControllers))
	staticResourceControllerList := make([]factory.Controller, 0, len(staticResourceControllers))
	deploymentControllerList := make([]factory.Controller, 0, len(deploymentControllers))
	clusterCatalogControllerList := make([]factory.Controller, 0, len(clusterCatalogControllers))

	for name, controller := range staticResourceControllers {
		controllerNames = append(controllerNames, name)
		staticResourceControllerList = append(staticResourceControllerList, controller)
	}

	for name, controller := range deploymentControllers {
		controllerNames = append(controllerNames, name)
		deploymentControllerList = append(deploymentControllerList, controller)
	}

	for name, controller := range clusterCatalogControllers {
		controllerNames = append(controllerNames, name)
		clusterCatalogControllerList = append(clusterCatalogControllerList, controller)
	}

	operatorImageVersion := status.VersionForOperatorFromEnv()
	currentOCPMinorVersion, err := utils.GetCurrentOCPMinorVersion(operatorImageVersion)
	if err != nil {
		return err
	}

	upgradeableConditionController := controller.NewStaticUpgradeableConditionController(
		"OLMStaticUpgradeableConditionController",
		cl.OperatorClient,
		cc.EventRecorder.ForComponent("OLMStaticUpgradeableConditionController"),
		controllerNames,
	)

	incompatibleOperatorController := controller.NewIncompatibleOperatorController(
		"OLMIncompatibleOperatorController",
		currentOCPMinorVersion,
		cl.KubeClient,
		cl.ClusterExtensionClient,
		cl.OperatorClient,
		cc.EventRecorder.ForComponent("OLMIncompatibleOperatorController"),
	)

	// Update the environment if proxy information is available
	err = controller.UpdateProxyEnvironment(klog.FromContext(ctx).WithName("main"), cl.ProxyClient)
	if err != nil {
		return err
	}

	proxyController := controller.NewProxyController(
		olmProxyController,
		cl.ProxyClient,
		cl.OperatorClient,
		cc.EventRecorder.ForComponent(olmProxyController),
	)

	tlsObserverController := controller.NewTLSObserverController(
		"OLMTLSSecurityProfileObserver",
		cl.OperatorClient,
		cl.ConfigInformerFactory,
		cc.EventRecorder.ForComponent("OLMTLSSecurityProfileObserver"),
	)

	versionGetter := status.NewVersionGetter()
	versionGetter.SetVersion("operator", status.VersionForOperatorFromEnv())

	// Add all resources to relatedObjects to ensure that must-gather picks them up.
	// Note: These resources are also hard-coded in the ClusterOperator manifest. This way,
	// must-gather will pick them up in case of catastrophic failure before we cluster-olm-operator
	// gets a chance to dynamically update the relatedObjects. Thus, making the pod logs accessible
	// for troubleshooting in the must-gather.
	manifestRelatedObjects, err := parseClusterOperatorManifestObjectReferences(manifestsPath, cl.RESTMapper)
	if err != nil {
		return err
	}
	relatedObjects = append(relatedObjects, manifestRelatedObjects...)

	// create wrapper here and pass it instead of raw configclient
	wrappedConfigClient := &configClientWrapper{
		ConfigV1Interface: cl.ConfigClient.ConfigV1(),
		coClient:          cl.ConfigClient.ConfigV1().ClusterOperators(),
		releaseVersion:    os.Getenv("RELEASE_VERSION"),
		clock:             cc.Clock,
	}

	// Pass wrapped client
	clusterOperatorController := status.NewClusterOperatorStatusController(
		"olm",
		relatedObjects,
		wrappedConfigClient, // wrapper instead of cl.ConfigClient.ConfigV1()
		cl.ConfigInformerFactory.Config().V1().ClusterOperators(),
		cl.OperatorClient,
		versionGetter,
		cc.EventRecorder.ForComponent("olm"),
		cc.Clock,
	)

	operatorLoggingController := loglevel.NewClusterOperatorLoggingController(cl.OperatorClient, cc.EventRecorder.ForComponent("ClusterOLMOperatorLoggingController"))

	cl.StartInformers(ctx)

	select {
	case <-cl.FeatureGatesAccessor.InitialFeatureGatesObserved():
		featureGates, _ := cl.FeatureGatesAccessor.CurrentFeatureGates()
		log.Info("FeatureGates initialized", "knownFeatures", featureGates.KnownFeatures())
	case <-time.After(1 * time.Minute):
		return errors.New("timed out waiting for FeatureGate detection")
	}

	for _, c := range append(staticResourceControllerList, upgradeableConditionController, incompatibleOperatorController, clusterOperatorController, operatorLoggingController, proxyController, tlsObserverController.Controller) {
		go func(c factory.Controller) {
			defer runtime.HandleCrash()
			c.Run(ctx, 1)
		}(c)
	}

	time.Sleep(10 * time.Second)

	for _, c := range deploymentControllerList {
		go func(c factory.Controller) {
			defer runtime.HandleCrash()
			c.Run(ctx, 1)
		}(c)
	}

	for _, c := range clusterCatalogControllerList {
		go func(c factory.Controller) {
			defer runtime.HandleCrash()
			c.Run(ctx, 1)
		}(c)
	}

	<-ctx.Done()
	return nil
}

// parseClusterOperatorManifestObjectReferences parses the manifests directory for olm ClusterOperator
// and provides the ObjectReferences of all resources except the ClusterOperator manifest.
func parseClusterOperatorManifestObjectReferences(manifestDirPath string, restMapper meta.RESTMapper) ([]configv1.ObjectReference, error) {
	relatedObjects := []configv1.ObjectReference{}
	if err := controller.WalkYAMLManifestsDir(manifestDirPath, func(path string, manifest *unstructured.Unstructured, _ []byte) error {
		objReference, err := controller.ToObjectReference(manifest, restMapper, nil)
		if err != nil {
			return fmt.Errorf("failed gvk lookup for file %s: %w", path, err)
		}

		if objReference == nil {
			// empty manifest, ignore
			return nil
		}

		// Do not add the ClusterOperator to its relatedObjects
		if objReference.Group == configv1.GroupName && objReference.Resource == "clusteroperators" {
			return nil
		}

		relatedObjects = append(relatedObjects, *objReference)
		return nil
	}); err != nil {
		return nil, err
	}

	return relatedObjects, nil
}
