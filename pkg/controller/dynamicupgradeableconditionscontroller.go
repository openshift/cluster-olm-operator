package controller

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	semver "github.com/blang/semver/v4"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	helmclient "github.com/operator-framework/helm-operator-plugins/pkg/client"
	storage "github.com/operator-framework/helm-operator-plugins/pkg/storage"
	ocv1alpha1 "github.com/operator-framework/operator-controller/api/v1alpha1"
	helm "helm.sh/helm/v3/pkg/storage"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
)

const (
	incompatibleOperatorsInstalled = "IncompatibleOperatorsInstalled"
	MaxOpenShiftVersionProperty    = "olm.maxOpenShiftVersion"
	OwnerKindKey                   = "olm.operatorframework.io/owner-kind"
	OwnerNameKey                   = "olm.operatorframework.io/owner-name"
	PackageNameKey                 = "olm.operatorframework.io/package-name"
	BundleNameKey                  = "olm.operatorframework.io/bundle-name"
	BundleVersionKey               = "olm.operatorframework.io/bundle-version"
)

type dynamicUpgradeableConditionController struct {
	kubeclient     kubernetes.Interface
	informer       informers.GenericInformer
	client         dynamic.Interface
	name           string
	operatorClient *clients.OperatorClient
	prefixes       []string
}

type ChartMetadata struct {
	Metadata struct {
		Annotations map[string]string `yaml:"annotations"`
	} `yaml:"metadata"`
}

type SecretData struct {
	Chart struct {
		ChartMetadata
	} `yaml:"chart"`
}

func NewDynamicUpgradeableConditionController(kubeclient kubernetes.Interface, client dynamic.Interface, name string, operatorClient *clients.OperatorClient, eventRecorder events.Recorder, prefixes []string) factory.Controller {
	infFact := dynamicinformer.NewDynamicSharedInformerFactory(client, 0)

	clusterExtensionGVR := schema.GroupVersionResource{
		Group:    ocv1alpha1.ClusterExtensionGVK.Group,
		Version:  ocv1alpha1.ClusterExtensionGVK.Version,
		Resource: ocv1alpha1.ClusterExtensionGVK.Kind,
	}

	inf := infFact.ForResource(clusterExtensionGVR)

	c := &dynamicUpgradeableConditionController{
		informer:       inf,
		client:         client,
		kubeclient:     kubeclient,
		name:           name,
		operatorClient: operatorClient,
		prefixes:       prefixes,
	}

	inf.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.handleClusterExtension,
		UpdateFunc: func(oldObj, newObj interface{}) { c.handleClusterExtension(newObj) },
		DeleteFunc: c.handleClusterExtension,
	})

	return factory.New().WithInformers(inf.Informer()).ToController(name, eventRecorder)
}

func (c *dynamicUpgradeableConditionController) handleClusterExtension(obj interface{}) {
	_, ok := obj.(*ocv1alpha1.ClusterExtension)
	if !ok {
		log.Println("Error: could not cast to ClusterExtension object")
		return
	}

	c.syncIncompatibleOperators()
}

func (c *dynamicUpgradeableConditionController) syncIncompatibleOperators() {
	current, err := getCurrentRelease()
	if err != nil {
		log.Printf("Error getting current OCP release: %v", err)
		return
	}

	if current == nil {
		// Note: This shouldn't happen
		err = fmt.Errorf("failed to determine current OpenShift Y-stream release")
		log.Printf("Error processing secrets: %v", err)
		return
	}

	next, err := nextY(*current)
	if err != nil {
		log.Printf("Error finding next OCP Y-stream release: %v", err)
		return
	}

	var incompatibleOperators []string

	ceList, err := c.informer.Lister().List(nil)
	if err != nil {
		log.Printf("Error listing cluster extensions: %v", err)
		return
	}

	// Get all ClusterExtensions incompatible with next Y-stream
	for _, obj := range ceList {
		ce, ok := obj.(*ocv1alpha1.ClusterExtension)
		if !ok {
			log.Println("Error: could not cast to ClusterExtension object")
			return
		}
		store := buildHelmStore(ce, c.kubeclient.CoreV1().Secrets(ce.Namespace))

		rel, _ := store.Deployed(ce.Name)
		version, ok := rel.Chart.Metadata.Annotations[MaxOpenShiftVersionProperty]
		if ok {
			maxOCPVersion, err := toSemver(version)
			if err != nil {
				log.Printf("Error converting to semver for version %s: %v", version, err)
				continue
			}
			if maxOCPVersion == nil || maxOCPVersion.GTE(next) {
				// All good
			} else {
				// Incompatible
				name := rel.Labels[BundleNameKey]
				incompatibleOperators = append(incompatibleOperators, name)
			}
		}
	}

	updateStatusFuncs := make([]v1helpers.UpdateStatusFunc, 0, 1)
	if len(incompatibleOperators) > 0 {
		updateStatusFuncs = append(updateStatusFuncs, v1helpers.UpdateConditionFn(operatorv1.OperatorCondition{
			Type:    "Upgradeable",
			Status:  operatorv1.ConditionFalse,
			Reason:  incompatibleOperatorsInstalled,
			Message: strings.Join(incompatibleOperators, ","),
		}))

		if _, _, updateErr := v1helpers.UpdateStatus(context.TODO(), c.operatorClient, updateStatusFuncs...); updateErr != nil {
			log.Printf("Error listing secrets: %v", err)
			return
		}
	} else {
		updateStatusFuncs = append(updateStatusFuncs, v1helpers.UpdateConditionFn(operatorv1.OperatorCondition{
			Type:   "Upgradeable",
			Status: operatorv1.ConditionTrue,
		}))

		if _, _, updateErr := v1helpers.UpdateStatus(context.TODO(), c.operatorClient, updateStatusFuncs...); updateErr != nil {
			log.Printf("Error listing secrets: %v", err)
			return
		}
	}
	return
}

func buildHelmStore(ce *ocv1alpha1.ClusterExtension, secretClient v1.SecretInterface) helm.Storage {
	csConfig := storage.ChunkedSecretsConfig{
		ChunkSize:      0,
		MaxReadChunks:  0,
		MaxWriteChunks: 0,
		Log:            nil,
	}

	owner := ce.Name
	ownerRefs := []metav1.OwnerReference{*metav1.NewControllerRef(ce, ce.GetObjectKind().GroupVersionKind())}
	ownerRefSecretClient := helmclient.NewOwnerRefSecretClient(secretClient, ownerRefs, func(secret *corev1.Secret) bool {
		return secret.Type == storage.SecretTypeChunkedIndex
	})

	return helm.Storage{
		Driver:     storage.NewChunkedSecrets(ownerRefSecretClient, owner, csConfig),
		MaxHistory: 0,
		Log:        log.Printf,
	}
}

type openshiftRelease struct {
	version *semver.Version
	mu      sync.Mutex
}

var (
	currentRelease = &openshiftRelease{}
)

const (
	releaseEnvVar = "RELEASE_VERSION" // OpenShift's env variable for defining the current release
)

func getCurrentRelease() (*semver.Version, error) {
	currentRelease.mu.Lock()
	defer currentRelease.mu.Unlock()

	if currentRelease.version != nil {
		/*
			If the version is already set, we don't want to set it again as the currentRelease
			is designed to be a singleton. If a new version is set, we are making an assumption
			that this controller will be restarted and thus pull in the new version from the
			environment into memory.

			Note: sync.Once is not used here as it was difficult to reliably test without hitting
			race conditions.
		*/
		return currentRelease.version, nil
	}

	// Get the raw version from the releaseEnvVar environment variable
	raw, ok := os.LookupEnv(releaseEnvVar)
	if !ok || raw == "" {
		// No env var set, try again later
		return nil, fmt.Errorf("desired release version missing from %v env variable", releaseEnvVar)
	}

	release, err := semver.ParseTolerant(raw)
	if err != nil {
		return nil, fmt.Errorf("cluster version has invalid desired release version: %w", err)
	}

	currentRelease.version = &release

	return currentRelease.version, nil
}

func nextY(v semver.Version) (semver.Version, error) {
	v.Build = nil // Builds are irrelevant

	if len(v.Pre) > 0 {
		// Dropping pre-releases is equivalent to incrementing Y
		v.Pre = nil
		v.Patch = 0

		return v, nil
	}

	return v, v.IncrementMinor() // Sets Y=Y+1 and Z=0
}

func toSemver(max string) (*semver.Version, error) {
	value := strings.Trim(max, "\"")
	if value == "" {
		// Handle "" separately, so parse doesn't treat it as a zero
		return nil, fmt.Errorf(`value cannot be "" (an empty string)`)
	}

	version, err := semver.ParseTolerant(value)
	if err != nil {
		return nil, fmt.Errorf(`failed to parse "%s" as semver: %w`, value, err)
	}

	truncatedVersion := semver.Version{Major: version.Major, Minor: version.Minor}
	if !version.EQ(truncatedVersion) {
		return nil, fmt.Errorf("property %s must specify only <major>.<minor> version, got invalid value %s", MaxOpenShiftVersionProperty, version)
	}
	return &truncatedVersion, nil
}
