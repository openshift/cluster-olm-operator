package assets

import (
	"embed"

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
	relatedObjects := make([]configv1.ObjectReference, 0, len(files))
	for _, f := range files {
		data, err := ReadFile(f)
		if err != nil {
			return nil, err
		}
		var u unstructured.Unstructured
		if err := yaml.Unmarshal(data, &u); err != nil {
			return nil, err
		}

		m, err := rm.RESTMapping(u.GroupVersionKind().GroupKind(), u.GroupVersionKind().Version)
		if err != nil {
			return nil, err
		}
		relatedObjects = append(relatedObjects, configv1.ObjectReference{
			Group:     m.GroupVersionKind.Group,
			Resource:  m.Resource.Resource,
			Namespace: u.GetNamespace(),
			Name:      u.GetName(),
		})
	}
	return relatedObjects, nil
}
