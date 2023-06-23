package main

import (
	"context"
	goflag "flag"
	"os"

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
		Short: "OpenShift cluster olm operator",
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
	cmd.Use = "operator"
	cmd.Short = "Start the cluster olm operator"
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
		"catalogd/02-customresourcedefinition-catalogs.catalogd.operatorframework.io.yml",
		"catalogd/03-customresourcedefinition-packages.catalogd.operatorframework.io.yml",
		"catalogd/04-serviceaccount-openshift-catalogd-catalogd-controller-manager.yml",
		"catalogd/05-role-openshift-catalogd-catalogd-leader-election-role.yml",
		"catalogd/06-clusterrole-catalogd-manager-role.yml",
		"catalogd/07-clusterrole-catalogd-metrics-reader.yml",
		"catalogd/08-clusterrole-catalogd-proxy-role.yml",
		"catalogd/09-rolebinding-openshift-catalogd-catalogd-leader-election-rolebinding.yml",
		"catalogd/10-clusterrolebinding-catalogd-manager-rolebinding.yml",
		"catalogd/11-clusterrolebinding-catalogd-proxy-rolebinding.yml",
		"catalogd/12-service-openshift-catalogd-catalogd-controller-manager-metrics-service.yml",
	}

	catalogdDeployment := "catalogd/13-deployment-openshift-catalogd-catalogd-controller-manager.yml"

	catalogdRelatedObjects, err := assets.RelatedObjects(cl.RESTMapper, append(catalogdStaticFiles, catalogdDeployment))
	if err != nil {
		return err
	}

	namespaces := sets.New[string]()
	for _, obj := range catalogdRelatedObjects {
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
		nil,
	)

	versionGetter := status.NewVersionGetter()
	versionGetter.SetVersion("operator", status.VersionForOperatorFromEnv())

	clusterOperatorController := status.NewClusterOperatorStatusController(
		"cluster-olm-operator",
		catalogdRelatedObjects,
		cl.ConfigClient.ConfigV1(),
		cl.ConfigInformerFactory.Config().V1().ClusterOperators(),
		cl.OperatorClient,
		versionGetter,
		cc.EventRecorder.ForComponent("cluster-olm-operator"),
	)

	cl.StartInformers(ctx)

	for _, c := range []factory.Controller{
		catalogdStaticResourceController,
		catalogdDeploymentController,
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
