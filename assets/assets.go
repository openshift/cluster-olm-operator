package assets

import (
	"embed"
	"errors"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

//go:embed *
var f embed.FS

// ReadFile reads and returns the content of the named file.
func ReadFile(name string) ([]byte, error) {
	return f.ReadFile(name)
}

func RelatedObjects(rm meta.RESTMapper, files []string) ([]configv1.ObjectReference, error) {
	var errs []error
	relatedObjects := make([]configv1.ObjectReference, 0, len(files))
	for _, f := range files {
		data, err := ReadFile(f)
		if err != nil {
			errs = append(errs, fmt.Errorf("error reading file %q: %w", f, err))
			continue
		}
		var u unstructured.Unstructured
		if err := yaml.Unmarshal(data, &u); err != nil {
			errs = append(errs, fmt.Errorf("error unmarshalling file %q: %w", f, err))
			continue
		}

		m, err := rm.RESTMapping(u.GroupVersionKind().GroupKind(), u.GroupVersionKind().Version)
		if err != nil {
			errs = append(errs, fmt.Errorf("error looking up RESTMapping for file %q, gvk %v: %w", f, u.GroupVersionKind(), err))
			continue
		}
		relatedObjects = append(relatedObjects, configv1.ObjectReference{
			Group:     m.GroupVersionKind.Group,
			Resource:  m.Resource.Resource,
			Namespace: u.GetNamespace(),
			Name:      u.GetName(),
		})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return relatedObjects, nil
}
