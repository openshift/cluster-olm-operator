package main

import (
	"context"
	goflag "flag"
	"os"
	"strings"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/deploymentcontroller"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/cli"
	utilflag "k8s.io/component-base/cli/flag"

	"github.com/openshift/cluster-olm-operator/assets"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/cluster-olm-operator/pkg/version"
)

func main() {
	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	command := newRootCommand()
	code := cli.Run(command)
	os.Exit(code)
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster-olm-operator",
		Short: "OpenShift Cluster OLM Operator",
	}
	cmd.AddCommand(newOperatorCommand())
	return cmd
}

func newOperatorCommand() *cobra.Command {
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

	catalogdStaticFiles := []string{
		"catalogd/00-namespace-openshift-catalogd.yml",
		"catalogd/01-customresourcedefinition-bundlemetadata.catalogd.operatorframework.io.yml",
		"catalogd/02-customresourcedefinition-catalogmetadata.catalogd.operatorframework.io.yml",
		"catalogd/03-customresourcedefinition-catalogs.catalogd.operatorframework.io.yml",
		"catalogd/04-customresourcedefinition-packages.catalogd.operatorframework.io.yml",
		"catalogd/05-serviceaccount-openshift-catalogd-catalogd-controller-manager.yml",
		"catalogd/06-role-openshift-catalogd-catalogd-leader-election-role.yml",
		"catalogd/07-clusterrole-catalogd-manager-role.yml",
		"catalogd/08-clusterrole-catalogd-metrics-reader.yml",
		"catalogd/09-clusterrole-catalogd-proxy-role.yml",
		"catalogd/10-rolebinding-openshift-catalogd-catalogd-leader-election-rolebinding.yml",
		"catalogd/11-clusterrolebinding-catalogd-manager-rolebinding.yml",
		"catalogd/12-clusterrolebinding-catalogd-proxy-rolebinding.yml",
		"catalogd/13-service-openshift-catalogd-catalogd-controller-manager-metrics-service.yml",
	}
	catalogdDeployment := "catalogd/14-deployment-openshift-catalogd-catalogd-controller-manager.yml"

	operatorControllerStaticFiles := []string{
		"operator-controller/00-namespace-openshift-operator-controller.yml",
		"operator-controller/01-customresourcedefinition-operators.operators.operatorframework.io.yml",
		"operator-controller/02-serviceaccount-openshift-operator-controller-operator-controller-controller-manager.yml",
		"operator-controller/03-role-openshift-operator-controller-operator-controller-leader-election-role.yml",
		"operator-controller/04-clusterrole-operator-controller-manager-role.yml",
		"operator-controller/05-clusterrole-operator-controller-metrics-reader.yml",
		"operator-controller/06-clusterrole-operator-controller-proxy-role.yml",
		"operator-controller/07-rolebinding-openshift-operator-controller-operator-controller-leader-election-rolebinding.yml",
		"operator-controller/08-clusterrolebinding-operator-controller-manager-rolebinding.yml",
		"operator-controller/09-clusterrolebinding-operator-controller-proxy-rolebinding.yml",
		"operator-controller/10-service-openshift-operator-controller-operator-controller-controller-manager-metrics-service.yml",
	}
	operatorControllerDeployment := "operator-controller/11-deployment-openshift-operator-controller-operator-controller-controller-manager.yml"

	catalogdRelatedObjects, err := assets.RelatedObjects(cl.RESTMapper, append(catalogdStaticFiles, catalogdDeployment))
	if err != nil {
		return err
	}

	operatorControllerRelatedObjects, err := assets.RelatedObjects(cl.RESTMapper, append(operatorControllerStaticFiles, operatorControllerDeployment))
	if err != nil {
		return err
	}

	relatedObjects := append(catalogdRelatedObjects, operatorControllerRelatedObjects...)

	namespaces := sets.New[string]()
	for _, obj := range relatedObjects {
		namespaces.Insert(obj.Namespace)
	}

	cl.KubeInformersForNamespaces = v1helpers.NewKubeInformersForNamespaces(cl.KubeClient, namespaces.UnsortedList()...)

	catalogdStaticResourceController := staticresourcecontroller.NewStaticResourceController(
		"CatalogdStaticResources",
		assets.ReadFile,
		catalogdStaticFiles,
		cl.ClientHolder(),
		cl.OperatorClient,
		cc.EventRecorder.ForComponent("CatalogdStaticResources"),
	).AddKubeInformers(cl.KubeInformersForNamespaces)

	catalogDeploymentManifest, err := assets.ReadFile(catalogdDeployment)
	if err != nil {
		return err
	}
	catalogdDeploymentController := deploymentcontroller.NewDeploymentController(
		"CatalogdControllerDeployment",
		catalogDeploymentManifest,
		cc.EventRecorder.ForComponent("CatalogdControllerDeployment"),
		cl.OperatorClient,
		cl.KubeClient,
		cl.KubeInformerFactory.Apps().V1().Deployments(),
		nil,
		[]deploymentcontroller.ManifestHookFunc{
			replaceImageHook("${CATALOGD_IMAGE}", "CATALOGD_IMAGE"),
			replaceImageHook("${KUBE_RBAC_PROXY_IMAGE}", "KUBE_RBAC_PROXY_IMAGE"),
		},
	)

	operatorControllerStaticResourceController := staticresourcecontroller.NewStaticResourceController(
		"OperatorControllerStaticResources",
		assets.ReadFile,
		operatorControllerStaticFiles,
		cl.ClientHolder(),
		cl.OperatorClient,
		cc.EventRecorder.ForComponent("OperatorControllerStaticResources"),
	).AddKubeInformers(cl.KubeInformersForNamespaces)

	operatorControllerDeploymentManifest, err := assets.ReadFile(operatorControllerDeployment)
	if err != nil {
		return err
	}
	operatorControllerDeploymentController := deploymentcontroller.NewDeploymentController(
		"OperatorControllerDeployment",
		operatorControllerDeploymentManifest,
		cc.EventRecorder.ForComponent("OperatorControllerDeployment"),
		cl.OperatorClient,
		cl.KubeClient,
		cl.KubeInformerFactory.Apps().V1().Deployments(),
		nil,
		[]deploymentcontroller.ManifestHookFunc{
			replaceImageHook("${OPERATOR_CONTROLLER_IMAGE}", "OPERATOR_CONTROLLER_IMAGE"),
			replaceImageHook("${KUBE_RBAC_PROXY_IMAGE}", "KUBE_RBAC_PROXY_IMAGE"),
		},
	)

	versionGetter := status.NewVersionGetter()
	versionGetter.SetVersion("operator", status.VersionForOperatorFromEnv())

	clusterOperatorController := status.NewClusterOperatorStatusController(
		"olm",
		relatedObjects,
		cl.ConfigClient.ConfigV1(),
		cl.ConfigInformerFactory.Config().V1().ClusterOperators(),
		cl.OperatorClient,
		versionGetter,
		cc.EventRecorder.ForComponent("olm"),
	)

	cl.StartInformers(ctx)

	for _, c := range []factory.Controller{
		catalogdStaticResourceController,
		catalogdDeploymentController,
		operatorControllerStaticResourceController,
		operatorControllerDeploymentController,
		clusterOperatorController,
	} {
		go func(c factory.Controller) {
			defer runtime.HandleCrash()
			c.Run(ctx, 1)
		}(c)
	}

	<-ctx.Done()
	return nil
}

func replaceImageHook(placeholder string, desiredImageEnvVar string) deploymentcontroller.ManifestHookFunc {
	return func(spec *operatorv1.OperatorSpec, deployment []byte) ([]byte, error) {
		replacer := strings.NewReplacer(placeholder, os.Getenv(desiredImageEnvVar))
		newDeployment := replacer.Replace(string(deployment))
		return []byte(newDeployment), nil
	}
}
