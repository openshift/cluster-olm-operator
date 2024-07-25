package controller

import (
	"bytes"
	"errors"
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
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/openshift/cluster-olm-operator/pkg/clients"
)

type Builder struct {
	Assets            fs.FS
	Clients           *clients.Clients
	ControllerContext *controllercmd.ControllerContext
}

func (b *Builder) BuildControllers(subDirectories ...string) (map[string]factory.Controller, []configv1.ObjectReference, error) {
	var (
		controllers    = map[string]factory.Controller{}
		relatedObjects []configv1.ObjectReference
		errs           []error
	)

	titler := cases.Title(language.English)
	for _, subDirectory := range subDirectories {
		var staticResourceFiles []string
		namePrefix := strings.ReplaceAll(titler.String(subDirectory), "-", "")
		if err := fs.WalkDir(b.Assets, subDirectory, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
				return nil
			}

			manifestData, err := fs.ReadFile(b.Assets, path)
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
			restMapping, err := b.Clients.RESTMapper.RESTMapping(manifestGVK.GroupKind(), manifestGVK.Version)
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
				controllerName := controllerNameForObject(namePrefix, &manifest)
				controllers[controllerName] = deploymentcontroller.NewDeploymentController(
					controllerName,
					manifestData,
					b.ControllerContext.EventRecorder.ForComponent(controllerName),
					b.Clients.OperatorClient,
					b.Clients.KubeClient,
					b.Clients.KubeInformerFactory.Apps().V1().Deployments(),
					nil,
					[]deploymentcontroller.ManifestHookFunc{
						replaceImageHook("${CATALOGD_IMAGE}", "CATALOGD_IMAGE"),
						replaceImageHook("${OPERATOR_CONTROLLER_IMAGE}", "OPERATOR_CONTROLLER_IMAGE"),
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
				func(name string) ([]byte, error) { return fs.ReadFile(b.Assets, name) },
				staticResourceFiles,
				b.Clients.ClientHolder(),
				b.Clients.OperatorClient,
				b.ControllerContext.EventRecorder.ForComponent(controllerName),
			)
		}
	}
	if len(errs) > 0 {
		return nil, nil, fmt.Errorf("error building controllers: %w", errors.Join(errs...))
	}
	return controllers, relatedObjects, nil
}

type object interface {
	metav1.Object
	runtime.Object
}

func controllerNameForObject(prefix string, obj object) string {
	titler := cases.Title(language.English)
	return fmt.Sprintf("%s%s%s",
		strings.ReplaceAll(titler.String(prefix), "-", ""),
		obj.GetObjectKind().GroupVersionKind().Kind,
		strings.ReplaceAll(titler.String(obj.GetName()), "-", ""),
	)
}

func replaceImageHook(placeholder string, desiredImageEnvVar string) deploymentcontroller.ManifestHookFunc {
	return func(spec *operatorv1.OperatorSpec, deployment []byte) ([]byte, error) {
		replacer := strings.NewReplacer(placeholder, os.Getenv(desiredImageEnvVar))
		newDeployment := replacer.Replace(string(deployment))
		return []byte(newDeployment), nil
	}
}
