package main

import (
	"context"
	"errors"
	goflag "flag"
	"fmt"
	"os"
	"reflect"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-olm-operator/internal/versionutils"

	_ "github.com/openshift/api/operator/v1/zz_generated.crd-manifests"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	libgoclient "github.com/openshift/library-go/pkg/config/client"
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
	operatorControllerNamespace                   = "openshift-operator-controller"
	operatorControllerServiceAccount              = "operator-controller-controller-manager"
	operatorControllerClusterAdminRoleBindingName = "operator-controller-cluster-admin-rolebinding" // 5.0+ (cluster-admin)
	operatorControllerManagerRoleName             = "operator-controller-manager-role"

	// object-controller resource identifiers. object-controller is baked into the
	// operator-controller image and runs in the same namespace (operatorControllerNamespace),
	// but is a separate manager with its own ServiceAccount and cluster-admin ClusterRoleBinding.
	objectControllerServiceAccount              = "object-controller-controller-manager"
	objectControllerClusterAdminRoleBindingName = "object-controller-cluster-admin-rolebinding"
)

var legacyOperatorControllerRoleBindingNames = []string{
	"operator-controller-manager-admin-rolebinding", // pre-5.0 TechPreview, DevPreview
	"operator-controller-manager-rolebinding",       // pre-5.0 CustomNoUpgrade
}

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
	// metricsConfigFile is the temp file written by PersistentPreRunE and watched by
	// controllercmd's WithRestartOnChange. It is shared with runOperator via the closure
	// so that the TLS change handler can update it and trigger a graceful restart.
	var metricsConfigFile string
	var startupFeatureGate *configv1.FeatureGate

	cmd := controllercmd.NewControllerCommandConfig(
		"cluster-olm-operator",
		version.Get(),
		func(ctx context.Context, cc *controllercmd.ControllerContext) error {
			return runOperator(ctx, cc, metricsConfigFile, startupFeatureGate)
		},
		clock.RealClock{},
	).NewCommandWithContext(context.Background())
	cmd.Use = "start"
	cmd.Short = "Start the Cluster OLM Operator"

	// Pre-configure the metrics server TLS from the cluster APIServer TLS profile before
	// controllercmd starts the server. This runs only when --config is not already provided.
	cmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("config") {
			return nil
		}
		kubeConfig, err := libgoclient.GetKubeConfigOrInClusterConfig("", nil)
		if err != nil {
			klog.Warningf("Skipping metrics server TLS pre-configuration (no in-cluster config): %v", err)
			return nil
		}
		cfgClient, err := configclient.NewForConfig(kubeConfig)
		if err != nil {
			klog.Warningf("Skipping metrics server TLS pre-configuration (config client error): %v", err)
			return nil
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		featureGate, err := cfgClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			klog.Warning("Skipping TLS group preferences for metrics server: unable to read FeatureGate")
		} else {
			startupFeatureGate = featureGate.DeepCopy()
		}
		tlsGroupPreferencesEnabled := err == nil && controller.FeatureGatesFromStatus(featureGate).Enabled(features.FeatureGateTLSGroupPreferences)
		servingInfo, err := controller.GetMetricsServerTLSServingInfo(ctx, cfgClient, tlsGroupPreferencesEnabled)
		if err != nil || servingInfo.MinTLSVersion == "" {
			klog.Warningf("Skipping metrics server TLS pre-configuration: %v", err)
			return nil
		}
		configFile, err := controller.WriteMetricsServerConfigFile(servingInfo)
		if err != nil {
			klog.Warningf("Skipping metrics server TLS pre-configuration (write error): %v", err)
			return nil
		}
		metricsConfigFile = configFile
		return cmd.Flags().Set("config", configFile)
	}

	return cmd
}

// waitForComponentResources waits for the operatorControllerNamespace and the given
// ServiceAccount to be created by the static resource controllers before attempting to
// verify RBAC propagation. This prevents the SAR check from timing out when resources
// don't exist yet. All OLMv1 managers (operator-controller, object-controller) share
// operatorControllerNamespace, so only the ServiceAccount varies per component.
func waitForComponentResources(ctx context.Context, cl *clients.Clients, serviceAccount string, log klog.Logger) error {
	log.Info("Waiting for component resources to be created", "namespace", operatorControllerNamespace, "serviceAccount", serviceAccount)

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
		_, err := cl.KubeClient.CoreV1().ServiceAccounts(operatorControllerNamespace).Get(ctx, serviceAccount, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.V(2).Info("ServiceAccount does not exist yet, waiting", "serviceAccount", serviceAccount)
				return false, nil // Continue polling
			}
			// For other errors (network, permissions, etc.), fail fast
			return false, err
		}
		log.Info("ServiceAccount created", "serviceAccount", serviceAccount)
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("timeout waiting for ServiceAccount %s/%s to be created: %w", operatorControllerNamespace, serviceAccount, err)
	}

	return nil
}

// waitForComponentRBAC waits for one of the candidate ClusterRoleBindings that grant the
// component ServiceAccount (in operatorControllerNamespace) access to the privileged SCC to
// be created and propagated through the kube-apiserver's RBAC authorization cache. This
// function assumes the namespace and ServiceAccount already exist. Returns the name of the
// ClusterRoleBinding that was found.
//
// RBAC propagation is per-subject, so each component manager (operator-controller,
// object-controller, ...) that runs its own privileged pod under its own ServiceAccount
// needs its own wait: a passing check for one SA says nothing about another.
func waitForComponentRBAC(ctx context.Context, cl *clients.Clients, serviceAccount string, crbNames []string, log klog.Logger) (string, error) {
	log.Info("Waiting for component RBAC to be created and propagated", "serviceAccount", serviceAccount)

	var foundCRB string
	// Wait for ClusterRoleBinding to exist
	err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		for _, name := range crbNames {
			_, err := cl.KubeClient.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
			if err == nil {
				foundCRB = name
				log.Info("ClusterRoleBinding created", "name", name)
				return true, nil
			}
			if !apierrors.IsNotFound(err) {
				return false, err
			}
		}
		log.V(2).Info("ClusterRoleBinding does not exist yet, waiting", "names", crbNames)
		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf("timeout waiting for ClusterRoleBinding %v to be created: %w", crbNames, err)
	}

	// Poll using SubjectAccessReview to verify RBAC authorization cache propagation.
	// The binding grants the ServiceAccount cluster-admin (which subsumes "use" on the
	// "privileged" SCC); we poll "use" on the privileged SCC as a representative signal
	// that the authorization cache has propagated far enough for SCC admission to admit
	// the component's privileged pod.
	log.Info("ClusterRoleBinding exists, polling RBAC authorization for SCC access", "name", foundCRB)
	err = wait.PollUntilContextTimeout(ctx, 1*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		sar := &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				User: fmt.Sprintf("system:serviceaccount:%s:%s", operatorControllerNamespace, serviceAccount),
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
			log.Info("RBAC authorization confirmed: ServiceAccount can use privileged SCC", "serviceAccount", serviceAccount)
			return true, nil
		}
		log.V(2).Info("RBAC not yet propagated, waiting", "reason", result.Status.Reason)
		return false, nil // Continue polling
	})
	if err != nil {
		return "", fmt.Errorf("timeout waiting for RBAC authorization to propagate: %w", err)
	}

	return foundCRB, nil
}

// cleanupOldOperatorControllerRBAC removes pre-5.0 RBAC resources that are no longer
// rendered by the operator-controller Helm chart after the service account deprecation.
// The old custom ClusterRole and conditionally-named ClusterRoleBindings are replaced
// by a single ClusterRoleBinding referencing the built-in cluster-admin ClusterRole.
// Cleanup is best-effort: NotFound errors are ignored, other errors are logged.
func cleanupOldOperatorControllerRBAC(ctx context.Context, cl *clients.Clients, log klog.Logger) {
	for _, name := range legacyOperatorControllerRoleBindingNames {
		err := cl.KubeClient.RbacV1().ClusterRoleBindings().Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			log.Info("Failed to clean up old ClusterRoleBinding", "name", name, "error", err)
		} else if err == nil {
			log.Info("Cleaned up old ClusterRoleBinding", "name", name)
		}
	}

	err := cl.KubeClient.RbacV1().ClusterRoles().Delete(ctx, operatorControllerManagerRoleName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		log.Info("Failed to clean up old ClusterRole", "name", operatorControllerManagerRoleName, "error", err)
	} else if err == nil {
		log.Info("Cleaned up old ClusterRole", "name", operatorControllerManagerRoleName)
	}
}

// waitForOLMv1ComponentsRBAC blocks until the RBAC that lets the privileged OLMv1 component
// pods be admitted has propagated through the kube-apiserver's authorization cache, so their
// Deployments are not created before the SCC admission plugin recognizes the permissions
// (the race that caused flaky SCC denial events; see OCPBUGS-77899).
//
// operator-controller is deployed whenever this is reached. object-controller is a separate
// manager (baked into the operator-controller image) with its own ServiceAccount, and its pod
// also requires the privileged SCC, so it needs its own per-subject wait. It is only waited on
// when its ServiceAccount was actually rendered (relatedObjects reflects the operator-controller
// image's chart output). Keying on the BoxcutterRuntime feature gate instead would hang for the
// full timeout on an operator-controller image that predates the object-controller split, since
// the gate can be enabled before the image ships the object-controller resources.
func waitForOLMv1ComponentsRBAC(ctx context.Context, cl *clients.Clients, relatedObjects []configv1.ObjectReference, log klog.Logger) error {
	// The static resource controllers run asynchronously in goroutines, so we poll until the
	// namespace and ServiceAccount exist before verifying RBAC propagation.
	if err := waitForComponentResources(ctx, cl, operatorControllerServiceAccount, log); err != nil {
		return err
	}
	operatorControllerCRBNames := append([]string{operatorControllerClusterAdminRoleBindingName}, legacyOperatorControllerRoleBindingNames...)
	foundCRB, err := waitForComponentRBAC(ctx, cl, operatorControllerServiceAccount, operatorControllerCRBNames, log)
	if err != nil {
		return err
	}
	if foundCRB == operatorControllerClusterAdminRoleBindingName {
		cleanupOldOperatorControllerRBAC(ctx, cl, log)
	}

	// object-controller (only when its resources were rendered; no legacy RBAC to clean up).
	if objectControllerRendered(relatedObjects) {
		if err := waitForComponentResources(ctx, cl, objectControllerServiceAccount, log); err != nil {
			return err
		}
		if _, err := waitForComponentRBAC(ctx, cl, objectControllerServiceAccount, []string{objectControllerClusterAdminRoleBindingName}, log); err != nil {
			return err
		}
	}

	return nil
}

// objectControllerRendered reports whether the Helm render produced the object-controller
// ServiceAccount in operatorControllerNamespace. relatedObjects contains an ObjectReference for
// every rendered manifest, so this reflects what the operator-controller image's chart actually
// ships rather than what a feature gate implies.
func objectControllerRendered(relatedObjects []configv1.ObjectReference) bool {
	for _, ref := range relatedObjects {
		if ref.Resource == "serviceaccounts" && ref.Namespace == operatorControllerNamespace && ref.Name == objectControllerServiceAccount {
			return true
		}
	}
	return false
}

func runOperator(ctx context.Context, cc *controllercmd.ControllerContext, metricsConfigFile string, startupFeatureGate *configv1.FeatureGate) error {
	cl, err := clients.New(cc)
	if err != nil {
		return err
	}

	log := klog.FromContext(ctx)

	fg := startupFeatureGate
	if fg == nil {
		fg, err = cl.ConfigClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to retrieve featureSet: %w", err)
		}
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
	currentFeatureGates, err := cb.CurrentFeatureGates()
	if err != nil {
		return fmt.Errorf("unable to retrieve current featureSet: %w", err)
	}
	cb.ClusterFeatureGates = currentFeatureGates

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
		currentFeatureGates,
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
	).WithAvailableInertia(status.MustNewInertia(5 * time.Second).Inertia)

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

	// When the TLS security profile changes the TLSObserverController stores the new
	// settings in observedConfig, which the deployment hook then propagates to the operand
	// Deployments. For the operator's own metrics server — which is configured at startup —
	// we must restart the pod so PersistentPreRunE can re-read the new profile.
	//
	// We watch the operator client informer (not the APIServer informer) so that we fire
	// only after the new config is already stored, avoiding a double-watch on the APIServer.
	// The initial TLS profile is read directly from the APIServer so it matches what
	// PersistentPreRunE used; this prevents a spurious restart on the first observer sync.
	//
	// Rather than calling os.Exit directly we update the config file that controllercmd's
	// WithRestartOnChange is already watching — it detects the content change and performs
	// a graceful restart for us.
	tlsGroupPreferencesEnabled := currentFeatureGates.Enabled(features.FeatureGateTLSGroupPreferences)
	initialTLSServingInfo, err := controller.GetMetricsServerTLSServingInfo(ctx, cl.ConfigClient, tlsGroupPreferencesEnabled)
	if err != nil {
		return fmt.Errorf("unable to retrieve initial TLS profile for metrics server: %w", err)
	}
	if _, err := cl.OperatorClient.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(_, _ interface{}) {
			operatorSpec, _, _, err := cl.OperatorClient.GetOperatorState()
			if err != nil {
				log.V(4).Info("Failed to get operator state in TLS change handler", "error", err)
				return
			}
			newMinTLS, newCiphers, newCurvePreferences := controller.TLSProfileFromObservedConfig(operatorSpec, tlsGroupPreferencesEnabled)
			if newMinTLS == "" {
				return // observer hasn't stored a TLS profile yet
			}
			// A failed pre-run FeatureGate read configures metrics without curves.
			// Once runOperator obtains the gate, rewrite that stale file even if
			// the observed TLS values already match its serving configuration.
			if (startupFeatureGate != nil || metricsConfigFile == "") &&
				newMinTLS == initialTLSServingInfo.MinTLSVersion &&
				reflect.DeepEqual(newCiphers, initialTLSServingInfo.CipherSuites) &&
				reflect.DeepEqual(newCurvePreferences, initialTLSServingInfo.CurvePreferences) {
				return // unchanged
			}
			log.Info("TLS security profile changed, updating metrics server config to trigger restart",
				"old", initialTLSServingInfo.MinTLSVersion, "new", newMinTLS)
			newServingInfo := configv1.HTTPServingInfo{
				ServingInfo: configv1.ServingInfo{
					MinTLSVersion:    newMinTLS,
					CipherSuites:     newCiphers,
					CurvePreferences: newCurvePreferences,
				},
			}
			if err := controller.UpdateMetricsServerConfigFile(metricsConfigFile, newServingInfo); err != nil {
				klog.Warningf("Failed to update metrics server TLS config file: %v", err)
			}
		},
	}); err != nil {
		return fmt.Errorf("failed to add operator TLS change event handler: %w", err)
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
		if err := waitForOLMv1ComponentsRBAC(ctx, cl, relatedObjects, log); err != nil {
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
