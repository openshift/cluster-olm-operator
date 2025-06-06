// Code generated by lister-gen. DO NOT EDIT.

package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	listers "k8s.io/client-go/listers"
	cache "k8s.io/client-go/tools/cache"
)

// KubeStorageVersionMigratorLister helps list KubeStorageVersionMigrators.
// All objects returned here must be treated as read-only.
type KubeStorageVersionMigratorLister interface {
	// List lists all KubeStorageVersionMigrators in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*operatorv1.KubeStorageVersionMigrator, err error)
	// Get retrieves the KubeStorageVersionMigrator from the index for a given name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*operatorv1.KubeStorageVersionMigrator, error)
	KubeStorageVersionMigratorListerExpansion
}

// kubeStorageVersionMigratorLister implements the KubeStorageVersionMigratorLister interface.
type kubeStorageVersionMigratorLister struct {
	listers.ResourceIndexer[*operatorv1.KubeStorageVersionMigrator]
}

// NewKubeStorageVersionMigratorLister returns a new KubeStorageVersionMigratorLister.
func NewKubeStorageVersionMigratorLister(indexer cache.Indexer) KubeStorageVersionMigratorLister {
	return &kubeStorageVersionMigratorLister{listers.New[*operatorv1.KubeStorageVersionMigrator](indexer, operatorv1.Resource("kubestorageversionmigrator"))}
}
