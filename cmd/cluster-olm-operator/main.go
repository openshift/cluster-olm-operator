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
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/component-base/cli"
	utilflag "k8s.io/component-base/cli/flag"

	"github.com/openshift/cluster-olm-operator/assets"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
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
		// TODO: lookup the actual version
		version.Info{Major: "0", Minor: "0", GitVersion: "0.0.1"},
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

	rukpakStaticFiles := []string{
		"rukpak/00-namespace--openshift-rukpak.yml",
		"rukpak/01-customresourcedefinition--bundledeployments.core.rukpak.io.yml",
		"rukpak/02-customresourcedefinition--bundles.core.rukpak.io.yml",
		"rukpak/03-serviceaccount-openshift-rukpak-core-admin.yml",
		"rukpak/04-serviceaccount-openshift-rukpak-helm-provisioner-admin.yml",
		"rukpak/05-serviceaccount-openshift-rukpak-rukpak-webhooks-admin.yml",
		"rukpak/06-clusterrole--bundle-reader.yml",
		"rukpak/07-clusterrole--bundle-uploader.yml",
		"rukpak/08-clusterrole--core-admin.yml",
		"rukpak/09-clusterrole--helm-provisioner-admin.yml",
		"rukpak/10-clusterrole--rukpak-webhooks-admin.yml",
		"rukpak/11-clusterrolebinding--core-admin.yml",
		"rukpak/12-clusterrolebinding--helm-provisioner-admin.yml",
		"rukpak/13-clusterrolebinding--rukpak-webhooks-admin.yml",
		"rukpak/14-service-openshift-rukpak-core.yml",
		"rukpak/15-service-openshift-rukpak-helm-provisioner.yml",
		"rukpak/16-service-openshift-rukpak-rukpak-webhook-service.yml",
		"rukpak/20-validatingwebhookconfiguration--rukpak-validating-webhook-configuration.yml",
	}
	rukpakDeploymentFiles := map[string]string{
		"Core":            "rukpak/17-deployment-openshift-rukpak-core.yml",
		"HelmProvisioner": "rukpak/18-deployment-openshift-rukpak-helm-provisioner.yml",
		"Webhooks":        "rukpak/19-deployment-openshift-rukpak-rukpak-webhooks.yml",
	}
	rukpakDeploymentFileNames := make([]string, 0, len(rukpakDeploymentFiles))
	for _, file := range rukpakDeploymentFiles {
		rukpakDeploymentFileNames = append(rukpakDeploymentFileNames, file)
	}

	catalogdRelatedObjects, err := assets.RelatedObjects(cl.RESTMapper, append(catalogdStaticFiles, catalogdDeployment))
	if err != nil {
		return err
	}

	operatorControllerRelatedObjects, err := assets.RelatedObjects(cl.RESTMapper, append(operatorControllerStaticFiles, operatorControllerDeployment))
	if err != nil {
		return err
	}

	rukpakRelatedObjects, err := assets.RelatedObjects(cl.RESTMapper, append(rukpakStaticFiles, rukpakDeploymentFileNames...))
	if err != nil {
		return err
	}

	relatedObjects := append(append(catalogdRelatedObjects, operatorControllerRelatedObjects...), rukpakRelatedObjects...)

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

	rukpakStaticResourceController := staticresourcecontroller.NewStaticResourceController(
		"RukpakStaticResources",
		assets.ReadFile,
		rukpakStaticFiles,
		cl.ClientHolder(),
		cl.OperatorClient,
		cc.EventRecorder.ForComponent("RukpakStaticResources"),
	).AddKubeInformers(cl.KubeInformersForNamespaces)

	rukpakCoreDeploymentManifest, err := assets.ReadFile(rukpakDeploymentFiles["Core"])
	if err != nil {
		return err
	}
	rukpakHelmProvisionerDeploymentManifest, err := assets.ReadFile(rukpakDeploymentFiles["HelmProvisioner"])
	if err != nil {
		return err
	}
	rukpakWebhooksDeploymentManifest, err := assets.ReadFile(rukpakDeploymentFiles["Webhooks"])
	if err != nil {
		return err
	}

	rukpakCoreDeploymentController := deploymentcontroller.NewDeploymentController(
		"RukpakCoreControllerDeployment",
		rukpakCoreDeploymentManifest,
		cc.EventRecorder.ForComponent("RukpakCoreControllerDeployment"),
		cl.OperatorClient,
		cl.KubeClient,
		cl.KubeInformerFactory.Apps().V1().Deployments(),
		nil,
		[]deploymentcontroller.ManifestHookFunc{
			replaceImageHook("${RUKPAK_IMAGE}", "RUKPAK_IMAGE"),
			replaceImageHook("${KUBE_RBAC_PROXY_IMAGE}", "KUBE_RBAC_PROXY_IMAGE"),
		},
	)
	rukpakHelmProvisionerDeploymentController := deploymentcontroller.NewDeploymentController(
		"RukpakHelmProvisionerControllerDeployment",
		rukpakHelmProvisionerDeploymentManifest,
		cc.EventRecorder.ForComponent("RukpakHelmProvisionerControllerDeployment"),
		cl.OperatorClient,
		cl.KubeClient,
		cl.KubeInformerFactory.Apps().V1().Deployments(),
		nil,
		[]deploymentcontroller.ManifestHookFunc{
			replaceImageHook("${RUKPAK_IMAGE}", "RUKPAK_IMAGE"),
			replaceImageHook("${KUBE_RBAC_PROXY_IMAGE}", "KUBE_RBAC_PROXY_IMAGE"),
		},
	)
	rukpakWebhooksDeploymentController := deploymentcontroller.NewDeploymentController(
		"RukpakWebhooksControllerDeployment",
		rukpakWebhooksDeploymentManifest,
		cc.EventRecorder.ForComponent("RukpakWebhooksControllerDeployment"),
		cl.OperatorClient,
		cl.KubeClient,
		cl.KubeInformerFactory.Apps().V1().Deployments(),
		nil,
		[]deploymentcontroller.ManifestHookFunc{
			replaceImageHook("${RUKPAK_IMAGE}", "RUKPAK_IMAGE"),
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
		rukpakStaticResourceController,
		rukpakCoreDeploymentController,
		rukpakHelmProvisionerDeploymentController,
		rukpakWebhooksDeploymentController,
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
