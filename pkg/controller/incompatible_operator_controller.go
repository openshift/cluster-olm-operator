package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	semver "github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/openshift/api/features"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	storage "github.com/operator-framework/helm-operator-plugins/pkg/storage"
	ocv1 "github.com/operator-framework/operator-controller/api/v1"
	"github.com/operator-framework/operator-registry/alpha/property"
	helm "helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-olm-operator/internal/versionutils"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
)

const (
	reasonIncompatibleOperatorsInstalled = "IncompatibleOperatorsInstalled"
	typeIncompatibelOperatorsUpgradeable = "InstalledOLMOperatorsUpgradeable"
	reasonFailureGettingExtension        = "FailureGettingExtensionMetadata"
	maxOpenShiftVersionProperty          = "olm.maxOpenShiftVersion"
	ownerKindKey                         = "olm.operatorframework.io/owner-kind"
	ownerNameKey                         = "olm.operatorframework.io/owner-name"
	packageNameKey                       = "olm.operatorframework.io/package-name"
	bundleNameKey                        = "olm.operatorframework.io/bundle-name"
	bundleVersionKey                     = "olm.operatorframework.io/bundle-version"
	olmPropertiesKey                     = "olm.properties"
	revisionStateActive                  = "Active"
)

type incompatibleOperatorController struct {
	name                   string
	currentOCPMinorVersion *semver.Version
	kubeclient             kubernetes.Interface
	clusterExtensionClient *clients.ClusterExtensionClient
	clusterObjectSetClient *clients.ClusterObjectSetClient
	operatorClient         v1helpers.OperatorClient
	featureGate            featuregates.FeatureGate
	logger                 logr.Logger
}

func NewIncompatibleOperatorController(
	name string,
	currentOCPMinorVersion *semver.Version,
	kubeclient kubernetes.Interface,
	clusterExtensionClient *clients.ClusterExtensionClient,
	clusterObjectSetClient *clients.ClusterObjectSetClient,
	operatorClient v1helpers.OperatorClient,
	featureGate featuregates.FeatureGate,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &incompatibleOperatorController{
		name:                   name,
		currentOCPMinorVersion: currentOCPMinorVersion,
		kubeclient:             kubeclient,
		clusterExtensionClient: clusterExtensionClient,
		clusterObjectSetClient: clusterObjectSetClient,
		operatorClient:         operatorClient,
		featureGate:            featureGate,
		logger:                 klog.NewKlogr().WithName(name),
	}

	return factory.New().WithSync(c.sync).WithSyncDegradedOnError(operatorClient).WithInformers(operatorClient.Informer(), clusterExtensionClient.Informer().Informer()).ToController(name, eventRecorder)
}

func (c *incompatibleOperatorController) sync(ctx context.Context, _ factory.SyncContext) error {
	c.logger.Info("sync started")
	defer c.logger.Info("sync finished")

	var updateStatusFn v1helpers.UpdateStatusFunc
	incompatibleOperators, err := c.getIncompatibleOperators()
	if len(incompatibleOperators) > 0 {
		// deterministic ordering
		sort.Strings(incompatibleOperators)

		// TODO(4.23): Update this message when main becomes 4.23 development
		message := fmt.Sprintf("Found ClusterExtensions that require upgrades prior to upgrading cluster to version 4.23 or 5.0: %s.", strings.Join(incompatibleOperators, ","))
		if err != nil {
			message += fmt.Sprintf("\n Additionally the following errors were encountered while getting extension metadata: %s", err.Error())
		}
		updateStatusFn = v1helpers.UpdateConditionFn(operatorv1.OperatorCondition{
			Type:    typeIncompatibelOperatorsUpgradeable,
			Status:  operatorv1.ConditionFalse,
			Reason:  reasonIncompatibleOperatorsInstalled,
			Message: message,
		})
	} else {
		if err != nil {
			updateStatusFn = v1helpers.UpdateConditionFn(operatorv1.OperatorCondition{
				Type:    typeIncompatibelOperatorsUpgradeable,
				Status:  operatorv1.ConditionFalse,
				Reason:  reasonFailureGettingExtension,
				Message: err.Error(),
			})
		} else {
			updateStatusFn = v1helpers.UpdateConditionFn(operatorv1.OperatorCondition{
				Type:   typeIncompatibelOperatorsUpgradeable,
				Status: operatorv1.ConditionTrue,
			})
		}
	}

	if _, _, updateErr := v1helpers.UpdateStatus(ctx, c.operatorClient, updateStatusFn); updateErr != nil {
		c.logger.Info(fmt.Sprintf("Error updating operator condition status: %v", updateErr))
		return updateErr
	}
	return err
}

func (c *incompatibleOperatorController) getIncompatibleOperators() ([]string, error) {
	if c.isBoxCutterRuntimeEnabled() {
		return c.getIncompatibleOperatorsFromObjectSet()
	}
	return c.getIncompatibleOperatorsFromHelmRelease()
}

func (c *incompatibleOperatorController) getIncompatibleOperatorsFromHelmRelease() ([]string, error) {
	var incompatibleOperators []string

	ceList, err := c.clusterExtensionClient.Informer().Lister().List(labels.NewSelector())
	if err != nil {
		c.logger.Error(err, "Error listing cluster extensions")
		return nil, err
	}

	store := c.buildHelmStore(c.kubeclient.CoreV1().Secrets("openshift-operator-controller"))

	var errs []error
	// Get all ClusterExtensions not upgradable to next Y-stream
	for _, obj := range ceList {
		metaObj, ok := obj.(metav1.Object)
		if !ok {
			errs = append(errs, fmt.Errorf("metav1.Object type assertion failed for object %v", obj))
			continue
		}
		name := metaObj.GetName()
		logger := c.logger.WithValues("clusterextension", name)
		rel, err := store.Deployed(name)
		if errors.Is(err, driver.ErrNoDeployedReleases) {
			logger.V(1).Info("Cluster Extension not yet deployed - will check again later")
			continue
		}
		if err != nil {
			errMessage := fmt.Sprintf("error returning the last deployed release for %s", name)
			logger.Info(errMessage)
			errs = append(errs, errors.New(errMessage))
			continue
		}

		if rel.Chart == nil || rel.Chart.Metadata == nil {
			logger.Info("Chart or Chart.Metadata is nil")
			continue
		}
		if _, ok := rel.Chart.Metadata.Annotations[olmPropertiesKey]; !ok {
			logger.V(1).Info("Bundle has no properties")
			continue
		}
		logger = logger.WithValues("bundleName", rel.Labels[bundleNameKey])
		props, err := propertyListFromPropertiesAnnotation(rel.Chart.Metadata.Annotations[olmPropertiesKey])
		if err != nil {
			err = fmt.Errorf("could not convert olm.properties: %v", err)
			errs = append(errs, fmt.Errorf("error with cluster extension %s: error in bundle %s: %v", name, rel.Labels[bundleNameKey], err))
			continue
		}
		isIncompatible, checkErrs := c.checkIncompatibility(logger, props)
		if len(checkErrs) > 0 {
			errs = append(errs, fmt.Errorf("error(s) with cluster extension %s: error in bundle %s: %v", name, rel.Labels[bundleNameKey], errors.Join(checkErrs...)))
			continue
		}
		if isIncompatible {
			logger.Info(fmt.Sprintf("found incompatible bundle %q for ClusterExtension %q", rel.Labels[bundleNameKey], name))
			incompatibleOperators = append(incompatibleOperators, fmt.Sprintf("bundle %q for ClusterExtension %q", rel.Labels[bundleNameKey], name))
		}
	}

	return incompatibleOperators, errors.Join(errs...)
}

func (c *incompatibleOperatorController) getIncompatibleOperatorsFromObjectSet() ([]string, error) {
	var incompatibleOperators []string

	ceList, err := c.clusterExtensionClient.Informer().Lister().List(labels.NewSelector())
	if err != nil {
		c.logger.Error(err, "Error listing cluster extensions")
		return nil, err
	}

	var errs []error
	// Get all ClusterExtensions not upgradable to next Y-stream
	for _, obj := range ceList {
		metaObj, ok := obj.(metav1.Object)
		if !ok {
			errs = append(errs, fmt.Errorf("metav1.Object type assertion failed for object %v", obj))
			continue
		}
		name := metaObj.GetName()
		logger := c.logger.WithValues("clusterextension", name)

		// Get ClusterObjectSets owned by the extension
		selector, err := labels.Parse(fmt.Sprintf("%s=%s,%s=%s", ownerKindKey, ocv1.ClusterExtensionKind, ownerNameKey, name))
		if err != nil {
			errs = append(errs, fmt.Errorf("error parsing label selector for cluster object set %s: %v", name, err))
			continue
		}
		cosList, err := c.clusterObjectSetClient.Informer().Lister().List(selector)
		if err != nil {
			errs = append(errs, fmt.Errorf("error listing cluster object set %s: %v", name, err))
			continue
		}

		// Get most recent active object set
		cos, err := getLatestObjectSet(cosList)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if cos == nil {
			logger.Info("No active object sets found for cluster extension")
			continue
		}

		cosAnns := cos.GetAnnotations()

		if _, ok := cosAnns[olmPropertiesKey]; !ok {
			logger.V(1).Info("Bundle has no olm properties")
			continue
		}

		bundleName := cosAnns[bundleNameKey]
		logger = logger.WithValues("bundleName", bundleName)
		props, err := propertyListFromPropertiesAnnotation(cosAnns[olmPropertiesKey])
		if err != nil {
			err = fmt.Errorf("could not convert olm.properties: %v", err)
			errs = append(errs, fmt.Errorf("error with cluster extension %s: error in bundle %s: %v", name, bundleName, err))
			continue
		}

		isIncompatible, checkErrs := c.checkIncompatibility(logger, props)
		if len(checkErrs) > 0 {
			errs = append(errs, fmt.Errorf("error(s) with cluster extension %s: error in bundle %s: %v", name, bundleName, errors.Join(checkErrs...)))
			continue
		}
		if isIncompatible {
			logger.Info(fmt.Sprintf("found incompatible bundle %q for ClusterExtension %q", bundleName, name))
			incompatibleOperators = append(incompatibleOperators, fmt.Sprintf("bundle %q for ClusterExtension %q", bundleName, name))
		}
	}

	return incompatibleOperators, errors.Join(errs...)
}

func (c *incompatibleOperatorController) buildHelmStore(secretClient v1.SecretInterface) helm.Storage {
	log := func(s string, args ...interface{}) { c.logger.Info(fmt.Sprintf(s, args...)) }
	csConfig := storage.ChunkedSecretsConfig{Log: log}

	return helm.Storage{
		Driver: storage.NewChunkedSecrets(secretClient, "operator-controller", csConfig),
		Log:    log,
	}
}

func (c *incompatibleOperatorController) isBoxCutterRuntimeEnabled() bool {
	return c.featureGate.Enabled(features.FeatureGateNewOLMBoxCutterRuntime)
}

func (c *incompatibleOperatorController) checkIncompatibility(logger logr.Logger, props []property.Property) (bool, []error) {
	var (
		errs           []error
		isIncompatible = false
		numMaxOCPProps = 0
	)

	for _, p := range props {
		if p.Type == maxOpenShiftVersionProperty {
			numMaxOCPProps++
			if numMaxOCPProps > 1 {
				errs = append(errs, fmt.Errorf("more than one %s found in bundle", maxOpenShiftVersionProperty))
				break
			}

			maxOCPVersion, err := versionutils.ToAllowedSemver(p.Value)
			if err != nil {
				err = fmt.Errorf("error converting to semver for version %s: %v", string(p.Value), err)
				logger.Info(err.Error())
				errs = append(errs, err)
				continue
			}

			// 1. maxOCPVersion is 4.18, currentOCPMinorVersion is 4.17 => upgradable
			// 2. maxOCPVersion is 4.18, currentOCPMinorVersion is 4.18 => not upgradable
			// 3. maxOCPVersion is 4.18, currentOCPMinorVersion is 4.19 => not upgradable
			isIncompatible = !versionutils.IsOperatorMaxOCPVersionCompatibleWithCluster(*maxOCPVersion, *c.currentOCPMinorVersion)
		}
	}
	return isIncompatible, errs
}

func getLatestObjectSet(objList []runtime.Object) (metav1.Object, error) {
	var selected metav1.Object
	var maxRev int64
	for _, runtimeObj := range objList {
		obj, ok := runtimeObj.(*unstructured.Unstructured)
		if !ok {
			return nil, fmt.Errorf("metav1.Object type assertion failed for object %v", runtimeObj)
		}

		// avoiding using ClusterObjectSet directly in case there are breaking changes in the serialization
		// of fields that we don't care about here as we iterate while in technical preview.
		// This helps avoid deadlocks where changes coming from the upstream break the OTE tests because
		// cluster-olm-operator is suffering from deserialization errors. These issues are not completely avoidable,
		// though. Note that they can still happen if there are changes to the fields we DO care about her=
		shortRev := &struct {
			Spec struct {
				LifecycleState string `json:"lifecycleState"`
				Revision       int64  `json:"revision"`
			} `json:"spec"`
		}{}

		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, shortRev); err != nil {
			return nil, fmt.Errorf("error converting object set: %w", err)
		}

		if shortRev.Spec.LifecycleState != revisionStateActive {
			continue
		}

		// Take latest active object set (highest spec.revision)
		if selected == nil || shortRev.Spec.Revision > maxRev {
			maxRev = shortRev.Spec.Revision
			selected = obj
		}
	}
	return selected, nil
}

func propertyListFromPropertiesAnnotation(raw string) ([]property.Property, error) {
	var props []property.Property
	if err := json.Unmarshal([]byte(raw), &props); err != nil {
		return nil, fmt.Errorf("failed to unmarshal properties annotation: %w", err)
	}
	return props, nil
}
