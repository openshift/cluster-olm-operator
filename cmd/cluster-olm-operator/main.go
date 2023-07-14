package main

import (
	"bytes"
	"context"
	"errors"
	goflag "flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/deploymentcontroller"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/component-base/cli"
	utilflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"

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

	cb := controllerBuilder{
		assetsFS: assets.FS,
		cl:       cl,
		cc:       cc,
	}

	controllers, relatedObjects, err := cb.BuildControllers("catalogd", "operator-controller", "rukpak")
	if err != nil {
		return err
	}

	namespaces := sets.New[string]()
	for _, obj := range relatedObjects {
		namespaces.Insert(obj.Namespace)
	}

	cl.KubeInformersForNamespaces = v1helpers.NewKubeInformersForNamespaces(cl.KubeClient, namespaces.UnsortedList()...)

	controllerNames := make([]string, 0, len(controllers))
	controllerList := make([]factory.Controller, 0, len(controllers))

	for name, controller := range controllers {
		controllerNames = append(controllerNames, name)
		controllerList = append(controllerList, controller)
	}

	upgradableConditionController := newStaticUpgradeableConditionController(
		"OLMStaticUpgradeableConditionController",
		cl.OperatorClient,
		cc.EventRecorder.ForComponent("OLMStaticUpgradeableConditionController"),
		controllerNames,
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

	for _, c := range append(controllerList, upgradableConditionController, clusterOperatorController) {
		go func(c factory.Controller) {
			defer runtime.HandleCrash()
			c.Run(ctx, 1)
		}(c)
	}

	<-ctx.Done()
	return nil
}

func newStaticUpgradeableConditionController(name string, operatorClient *clients.OperatorClient, eventRecorder events.Recorder, prefixes []string) factory.Controller {
	c := staticUpgradeableConditionController{
		name:           name,
		operatorClient: operatorClient,
		prefixes:       prefixes,
	}

	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(operatorClient).WithInformers(operatorClient.Informer()).ToController(name, eventRecorder)
}

type staticUpgradeableConditionController struct {
	name           string
	operatorClient *clients.OperatorClient
	prefixes       []string
}

func (c staticUpgradeableConditionController) sync(ctx context.Context, _ factory.SyncContext) error {
	logger := klog.FromContext(ctx).WithName(c.name)
	logger.V(4).Info("sync started")
	defer logger.V(4).Info("sync finished")

	opSpec, _, _, err := c.operatorClient.GetOperatorState()
	if err != nil {
		return err
	}
	if opSpec.ManagementState != operatorv1.Managed {
		return nil
	}

	updateStatusFuncs := make([]v1helpers.UpdateStatusFunc, 0, len(c.prefixes))
	for _, prefix := range c.prefixes {
		updateStatusFuncs = append(updateStatusFuncs, v1helpers.UpdateConditionFn(operatorv1.OperatorCondition{
			Type:   fmt.Sprintf("%sUpgradeable", prefix),
			Status: operatorv1.ConditionTrue,
		}))
	}

	if _, _, updateErr := v1helpers.UpdateStatus(ctx, c.operatorClient, updateStatusFuncs...); updateErr != nil {
		return updateErr
	}

	return nil
}

func replaceImageHook(placeholder string, desiredImageEnvVar string) deploymentcontroller.ManifestHookFunc {
	return func(spec *operatorv1.OperatorSpec, deployment []byte) ([]byte, error) {
		replacer := strings.NewReplacer(placeholder, os.Getenv(desiredImageEnvVar))
		newDeployment := replacer.Replace(string(deployment))
		return []byte(newDeployment), nil
	}
}

type controllerBuilder struct {
	assetsFS fs.FS
	cl       *clients.Clients
	cc       *controllercmd.ControllerContext
}

func (b *controllerBuilder) BuildControllers(subDirectories ...string) (map[string]factory.Controller, []configv1.ObjectReference, error) {
	var (
		controllers    = map[string]factory.Controller{}
		relatedObjects []configv1.ObjectReference
		errs           []error
	)

	titler := cases.Title(language.English)

	for _, subDirectory := range subDirectories {
		var staticResourceFiles []string
		namePrefix := strings.ReplaceAll(titler.String(subDirectory), "-", "")
		if err := fs.WalkDir(b.assetsFS, subDirectory, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
				return nil
			}

			manifestData, err := fs.ReadFile(b.assetsFS, path)
			if err != nil {
				errs = append(errs, fmt.Errorf("error reading assets file %q: %w", path, err))
				return nil
			}

			var manifest unstructured.Unstructured
			if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifestData), 4096).Decode(&manifest); err != nil {
				errs = append(errs, fmt.Errorf("error parsing manifest for file %q: %w", path, err))
				return nil
			}

			manifestGVK := manifest.GroupVersionKind()
			restMapping, err := b.cl.RESTMapper.RESTMapping(manifestGVK.GroupKind(), manifestGVK.Version)
			if err != nil {
				errs = append(errs, fmt.Errorf("error looking up RESTMapping for file %q, gvk %v: %w", path, manifestGVK, err))
				return nil
			}
			relatedObjects = append(relatedObjects, configv1.ObjectReference{
				Group:     restMapping.GroupVersionKind.Group,
				Resource:  restMapping.Resource.Resource,
				Namespace: manifest.GetNamespace(),
				Name:      manifest.GetName(),
			})

			if manifestGVK.Kind == "Deployment" && manifestGVK.Group == "apps" {
				controllerName := fmt.Sprintf("%sDeployment%s",
					namePrefix,
					strings.ReplaceAll(titler.String(manifest.GetName()), "-", ""),
				)
				controllers[controllerName] = deploymentcontroller.NewDeploymentController(
					controllerName,
					manifestData,
					b.cc.EventRecorder.ForComponent(controllerName),
					b.cl.OperatorClient,
					b.cl.KubeClient,
					b.cl.KubeInformerFactory.Apps().V1().Deployments(),
					nil,
					[]deploymentcontroller.ManifestHookFunc{
						replaceImageHook("${CATALOGD_IMAGE}", "CATALOGD_IMAGE"),
						replaceImageHook("${OPERATOR_CONTROLLER_IMAGE}", "OPERATOR_CONTROLLER_IMAGE"),
						replaceImageHook("${RUKPAK_IMAGE}", "RUKPAK_IMAGE"),
						replaceImageHook("${KUBE_RBAC_PROXY_IMAGE}", "KUBE_RBAC_PROXY_IMAGE"),
					},
				)
				return nil
			}

			staticResourceFiles = append(staticResourceFiles, path)
			return nil
		}); err != nil {
			return nil, nil, err
		}

		if len(staticResourceFiles) > 0 {
			controllerName := fmt.Sprintf("%sStaticResources", namePrefix)
			controllers[controllerName] = staticresourcecontroller.NewStaticResourceController(
				controllerName,
				func(name string) ([]byte, error) { return fs.ReadFile(b.assetsFS, name) },
				staticResourceFiles,
				b.cl.ClientHolder(),
				b.cl.OperatorClient,
				b.cc.EventRecorder.ForComponent(controllerName),
			)
		}
	}
	if len(errs) > 0 {
		return nil, nil, fmt.Errorf("error building controllers: %w", errors.Join(errs...))
	}
	return controllers, relatedObjects, nil
}
