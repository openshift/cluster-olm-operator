package main

import (
	"context"
	goflag "flag"
	"fmt"
	"os"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

	_ "github.com/openshift/api/operator/v1/zz_generated.crd-manifests"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/loglevel"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/cli"
	utilflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-olm-operator/internal/utils"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/cluster-olm-operator/pkg/controller"
	"github.com/openshift/cluster-olm-operator/pkg/version"

	catalogdv1 "github.com/operator-framework/catalogd/api/v1"
)

const (
	olmProxyController = "OLMProxyController"
)

func main() {
	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	command := newRootCommand()
	code := cli.Run(command)
	os.Exit(code)
}

func newRootCommand() *cobra.Command {
	var versionFlag bool

	cmd := &cobra.Command{
		Use:   "cluster-olm-operator",
		Short: "OpenShift Cluster OLM Operator",
		Run: func(cmd *cobra.Command, args []string) {
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
	).NewCommandWithContext(context.Background())
	cmd.Use = "start"
	cmd.Short = "Start the Cluster OLM Operator"
	return cmd
}

func runOperator(ctx context.Context, cc *controllercmd.ControllerContext) error {
	cl, err := clients.New(cc)
	if err != nil {
		return err
	}

	clusterCatalogGvk := catalogdv1.GroupVersion.WithKind("ClusterCatalog")
	cb := controller.Builder{
		Assets:            os.DirFS("/operand-assets"),
		Clients:           cl,
		ControllerContext: cc,
		KnownRESTMappings: map[schema.GroupVersionKind]*meta.RESTMapping{
			clusterCatalogGvk: {
				Resource:         catalogdv1.GroupVersion.WithResource("clustercatalogs"),
				GroupVersionKind: clusterCatalogGvk,
				Scope:            meta.RESTScopeRoot,
			},
		},
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
	nextOCPMinorVersion, err := utils.GetNextOCPMinorVersion(operatorImageVersion)
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
		nextOCPMinorVersion,
		cl.KubeClient,
		cl.ClusterExtensionClient,
		cl.OperatorClient,
		cc.EventRecorder.ForComponent("OLMIncompatibleOperatorController"),
	)

	// Side Effect! Update our environment immediately
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

	versionGetter := status.NewVersionGetter()
	versionGetter.SetVersion("operator", status.VersionForOperatorFromEnv())

	// Add OLM resource and openshift-cluster-olm-operator namespace to relatedObjects
	// to ensure that must-gather picks them up.
	// Note: These two resources are also hard-coded in the ClusterOperator manifest. This way,
	// must-gather will pick them up in case of catastrophic failure before we cluster-olm-operator
	// gets a chance to dynamically update the relatedObjects. Thus, making the pod logs accessible
	// for troubleshooting in the must-gather.
	relatedObjects = append(relatedObjects, newOLMObjectReference(), newNamespaceObjectReference())

	clusterOperatorController := status.NewClusterOperatorStatusController(
		"olm",
		relatedObjects,
		cl.ConfigClient.ConfigV1(),
		cl.ConfigInformerFactory.Config().V1().ClusterOperators(),
		cl.OperatorClient,
		versionGetter,
		cc.EventRecorder.ForComponent("olm"),
	)

	operatorLoggingController := loglevel.NewClusterOperatorLoggingController(cl.OperatorClient, cc.EventRecorder.ForComponent("ClusterOLMOperatorLoggingController"))

	cl.StartInformers(ctx)

	for _, c := range append(staticResourceControllerList, upgradeableConditionController, incompatibleOperatorController, clusterOperatorController, operatorLoggingController, proxyController) {
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

// newOLMObjectReference creates a configv1.ObjectReference for
// the cluster scoped OLM resources
func newOLMObjectReference() configv1.ObjectReference {
	return configv1.ObjectReference{
		Group:    operatorv1alpha1.GroupName,
		Resource: "olms",
		Name:     "cluster",
	}
}

// newNamespaceObjectReferences creates a configv1.ObjectReference for
// the OCP namespaces where this operator is installed: openshift-cluster-olm-operator
func newNamespaceObjectReference() configv1.ObjectReference {
	return configv1.ObjectReference{
		Group:    "",
		Resource: "namespaces",
		Name:     "openshift-cluster-olm-operator",
	}
}
