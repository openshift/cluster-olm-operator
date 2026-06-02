package main

import (
	"context"
	"errors"
	goflag "flag"
	"fmt"
	"os"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-olm-operator/internal/versionutils"

	_ "github.com/openshift/api/operator/v1/zz_generated.crd-manifests"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	cache "k8s.io/client-go/tools/cache"
	"k8s.io/component-base/cli"
	utilflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	ocv1 "github.com/operator-framework/operator-controller/api/v1"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/cluster-olm-operator/pkg/controller"
	"github.com/openshift/cluster-olm-operator/pkg/version"
)

const (
	olmProxyController = "OLMProxyController"
	assetPath          = "/operand-assets"
	manifestsPath      = "/manifests"

	// operator-controller resource identifiers
	operatorControllerNamespace      = "openshift-operator-controller"
	operatorControllerServiceAccount = "operator-controller-controller-manager"
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

// waitForOperatorControllerResources waits for the operator-controller namespace and
// ServiceAccount to be created by the static resource controllers before attempting to
// verify RBAC propagation. This prevents the SAR check from timing out when resources
// don't exist yet.
func waitForOperatorControllerResources(ctx context.Context, cl *clients.Clients, log klog.Logger) error {
	log.Info("Waiting for operator-controller resources to be created", "namespace", operatorControllerNamespace, "serviceAccount", operatorControllerServiceAccount)

	// Wait for namespace to exist
	err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		_, err := cl.KubeClient.CoreV1().Namespaces().Get(ctx, operatorControllerNamespace, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.V(2).Info("Namespace does not exist yet, waiting", "namespace", operatorControllerNamespace)
				return false, nil // Continue polling
			}
			// For other errors (network, permissions, etc.), fail fast
			return false, err
		}
		log.Info("Namespace created", "namespace", operatorControllerNamespace)
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("timeout waiting for namespace %s to be created: %w", operatorControllerNamespace, err)
	}

	// Wait for ServiceAccount to exist
	err = wait.PollUntilContextTimeout(ctx, 1*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		_, err := cl.KubeClient.CoreV1().ServiceAccounts(operatorControllerNamespace).Get(ctx, operatorControllerServiceAccount, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.V(2).Info("ServiceAccount does not exist yet, waiting", "serviceAccount", operatorControllerServiceAccount)
				return false, nil // Continue polling
			}
			// For other errors (network, permissions, etc.), fail fast
			return false, err
		}
		log.Info("ServiceAccount created", "serviceAccount", operatorControllerServiceAccount)
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("timeout waiting for ServiceAccount %s/%s to be created: %w", operatorControllerNamespace, operatorControllerServiceAccount, err)
	}

	return nil
}

// waitForOperatorControllerRBAC waits for the ClusterRoleBinding that grants the
// operator-controller ServiceAccount access to the privileged SCC to be created and
// propagated through the kube-apiserver's RBAC authorization cache.
// This function assumes the namespace and ServiceAccount already exist.
func waitForOperatorControllerRBAC(ctx context.Context, cl *clients.Clients, log klog.Logger) error {
	// The ClusterRoleBinding name varies based on feature flags enabled in the Helm chart:
	// - TechPreview/DevPreview: operator-controller-manager-admin-rolebinding
	// - CustomNoUpgrade: operator-controller-manager-rolebinding
	// We check for both possible names to handle all experimental feature set variants.
	crbNames := []string{
		"operator-controller-manager-admin-rolebinding", // TechPreview, DevPreview
		"operator-controller-manager-rolebinding",       // CustomNoUpgrade
	}

	log.Info("Waiting for operator-controller RBAC to be created and propagated")

	var foundCRB string
	// Wait for ClusterRoleBinding to exist
	err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		for _, crbName := range crbNames {
			_, err := cl.KubeClient.RbacV1().ClusterRoleBindings().Get(ctx, crbName, metav1.GetOptions{})
			if err == nil {
				foundCRB = crbName
				log.Info("ClusterRoleBinding created", "name", crbName)
				return true, nil
			}
			// If this error is NOT NotFound (e.g., Forbidden, network error), fail fast
			if !apierrors.IsNotFound(err) {
				return false, err
			}
			// If it's NotFound, continue checking other candidates
		}
		// All ClusterRoleBindings returned NotFound, continue polling
		log.V(2).Info("ClusterRoleBinding does not exist yet, waiting", "names", crbNames)
		return false, nil // Continue polling
	})
	if err != nil {
		return fmt.Errorf("timeout waiting for ClusterRoleBinding %v to be created: %w", crbNames, err)
	}

	// Poll using SubjectAccessReview to verify RBAC authorization cache propagation.
	// The ClusterRoleBinding grants "use" permission on the "privileged" SCC.
	// We verify that the operator-controller ServiceAccount can actually use the SCC
	// by polling the authorization API until the permission is recognized.
	log.Info("ClusterRoleBinding exists, polling RBAC authorization for SCC access", "name", foundCRB)
	err = wait.PollUntilContextTimeout(ctx, 1*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		sar := &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				User: fmt.Sprintf("system:serviceaccount:%s:%s", operatorControllerNamespace, operatorControllerServiceAccount),
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Verb:     "use",
					Group:    "security.openshift.io",
					Resource: "securitycontextconstraints",
					Name:     "privileged",
				},
			},
		}
		result, err := cl.KubeClient.AuthorizationV1().SubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.V(2).Info("SubjectAccessReview API not ready yet, waiting")
				return false, nil // Continue polling
			}
			// For other errors (network, permissions, etc.), fail fast
			return false, err
		}
		if result.Status.Allowed {
			log.Info("RBAC authorization confirmed: ServiceAccount can use privileged SCC")
			return true, nil
		}
		log.V(2).Info("RBAC not yet propagated, waiting", "reason", result.Status.Reason)
		return false, nil // Continue polling
	})
	if err != nil {
		return fmt.Errorf("timeout waiting for RBAC authorization to propagate: %w", err)
	}

	return nil
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

	infra, err := cl.ConfigClient.ConfigV1().Infrastructures().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to retrieve infrastructure: %w", err)
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
		FeatureGate:    *fg,
		Infrastructure: infra,
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
	currentOCPMinorVersion, err := versionutils.GetCurrentOCPMinorVersion(operatorImageVersion)
	if err != nil {
		return err
	}

	upgradeableConditionController := controller.NewStaticUpgradeableConditionController(
		"OLMStaticUpgradeableConditionController",
		cl.OperatorClient,
		cc.EventRecorder.ForComponent("OLMStaticUpgradeableConditionController"),
		controllerNames,
	)

	currentFeatureGates, err := cb.CurrentFeatureGates()
	if err != nil {
		return fmt.Errorf("unable to retrieve current featureSet: %w", err)
	}

	incompatibleOperatorController := controller.NewIncompatibleOperatorController(
		"OLMIncompatibleOperatorController",
		currentOCPMinorVersion,
		cl.KubeClient,
		cl.ClusterExtensionClient,
		cl.ClusterObjectSetClient,
		cl.OperatorClient,
		currentFeatureGates,
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
	wrappedConfigClient := clients.NewConfigClientWrapper(
		cl.ConfigClient.ConfigV1(),
		cl.ConfigInformerFactory.Config().V1().ClusterOperators().Lister(),
		os.Getenv("RELEASE_VERSION"),
		cc.Clock,
	)

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

	// Watch for infrastructure topology changes. Topology changes are exceedingly rare
	// (e.g., SNO to HA conversion) but require re-rendering the Helm manifests with the
	// correct replica count and PDB settings. Exiting causes the deployment controller to
	// restart cluster-olm-operator, which re-renders the manifests on startup.
	initialTopology := infra.Status.ControlPlaneTopology
	checkTopologyChange := func(obj interface{}) {
		newInfra, ok := obj.(*configv1.Infrastructure)
		if !ok {
			return
		}
		if newInfra.Status.ControlPlaneTopology != initialTopology {
			log.Info("Infrastructure topology changed, restarting to re-render manifests",
				"old", initialTopology, "new", newInfra.Status.ControlPlaneTopology)
			os.Exit(0)
		}
	}
	if _, err := cl.InfrastructureClient.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    checkTopologyChange,
		UpdateFunc: func(_, newObj interface{}) { checkTopologyChange(newObj) },
	}); err != nil {
		return fmt.Errorf("failed to add infrastructure event handler: %w", err)
	}

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

	// Wait for operator-controller RBAC if it will be deployed.
	// Must match the deployment decision in renderHelmTemplate (helm.go).
	// See: OCPBUGS-77899
	hasEnabledFeatureGates, err := cb.HasEnabledDownstreamFeatureGates(currentFeatureGates)
	if err != nil {
		return fmt.Errorf("unable to determine if operator-controller will be deployed: %w", err)
	}
	shouldWaitForOperatorController := cb.UseExperimentalFeatureSet() || hasEnabledFeatureGates

	if shouldWaitForOperatorController {
		// First, wait for the static resource controllers to create the namespace and ServiceAccount.
		// The static controllers run asynchronously in goroutines, so we need to poll until the
		// resources exist before we can verify RBAC propagation.
		if err := waitForOperatorControllerResources(ctx, cl, log); err != nil {
			return err
		}

		// Now that the resources exist, wait for the ClusterRoleBinding that grants SCC access
		// to be created and propagated through the kube-apiserver's RBAC cache. This ensures
		// the SCC admission plugin recognizes the permissions before we create the Deployment,
		// preventing the race condition that caused flaky SCC denial events.
		if err := waitForOperatorControllerRBAC(ctx, cl, log); err != nil {
			return err
		}
	}

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
