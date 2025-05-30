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
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-olm-operator/internal/utils"
	"github.com/openshift/cluster-olm-operator/pkg/clients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	storage "github.com/operator-framework/helm-operator-plugins/pkg/storage"
	"github.com/operator-framework/operator-registry/alpha/property"
	helm "helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"
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
)

type incompatibleOperatorController struct {
	name                   string
	currentOCPMinorVersion *semver.Version
	kubeclient             kubernetes.Interface
	clusterExtensionClient *clients.ClusterExtensionClient
	operatorClient         *clients.OperatorClient
	logger                 logr.Logger
}

func NewIncompatibleOperatorController(name string, currentOCPMinorVersion *semver.Version, kubeclient kubernetes.Interface, clusterExtensionClient *clients.ClusterExtensionClient, operatorClient *clients.OperatorClient, eventRecorder events.Recorder) factory.Controller {
	c := &incompatibleOperatorController{
		name:                   name,
		currentOCPMinorVersion: currentOCPMinorVersion,
		kubeclient:             kubeclient,
		clusterExtensionClient: clusterExtensionClient,
		operatorClient:         operatorClient,
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
		message := fmt.Sprintf("Found ClusterExtensions that require upgrades prior to upgrading cluster to version %d.%d: %s.", c.currentOCPMinorVersion.Major, c.currentOCPMinorVersion.Minor+1, strings.Join(incompatibleOperators, ","))
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
	var incompatibleOperators []string

	ceList, err := c.clusterExtensionClient.Informer().Lister().List(labels.NewSelector())
	if err != nil {
		c.logger.Error(err, "Error listing cluster extensions")
		return nil, err
	}

	store := c.buildHelmStore(c.kubeclient.CoreV1().Secrets("openshift-operator-controller"))

	var errs []error
	// Get all ClusterExtensions incompatible with next Y-stream
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
			logger.Info("Cluster Extension not yet deployed - will check again later")
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
		if _, ok := rel.Chart.Metadata.Annotations["olm.properties"]; !ok {
			logger.Info("Bundle has no properties")
			continue
		}
		logger = logger.WithValues("bundleName", rel.Labels[bundleNameKey])
		props, err := propertyListFromPropertiesAnnotation(rel.Chart.Metadata.Annotations["olm.properties"])
		if err != nil {
			err = fmt.Errorf("could not convert olm.properties: %v", err)
			logger.Info(err.Error())
			errs = append(errs, fmt.Errorf("error with cluster extension %s: error in bundle %s: %v", name, rel.Labels[bundleNameKey], err))
			continue
		}
		numMaxOCPProps := 0
		for _, p := range props {
			if p.Type == maxOpenShiftVersionProperty {
				numMaxOCPProps++
				maxOCPVersion, err := utils.ToAllowedSemver(p.Value)
				if err != nil {
					err = fmt.Errorf("error converting to semver for version %s: %v", string(p.Value), err)
					logger.Info(err.Error())
					errs = append(errs, fmt.Errorf("error with cluster extension %s: error in bundle %s: %v", name, rel.Labels[bundleNameKey], err))
					continue
				}
				if numMaxOCPProps > 1 {
					err = fmt.Errorf("more than one %s found in bundle", maxOpenShiftVersionProperty)
					logger.Info(err.Error())
					errs = append(errs, fmt.Errorf("error with cluster extension %s: error in bundle %s: %v", name, rel.Labels[bundleNameKey], err))
					continue
				}

				// 1. maxOCPVersion is 4.18, currentOCPMinorVersion is 4.17 => compatible
				// 2. maxOCPVersion is 4.18, currentOCPMinorVersion is 4.18 => incompatible
				// 3. maxOCPVersion is 4.18, currentOCPMinorVersion is 4.19 => incompatible
				if !utils.IsOperatorMaxOCPVersionCompatibleWithCluster(*maxOCPVersion, *c.currentOCPMinorVersion) {
					// Incompatible
					incompatibleOperators = append(incompatibleOperators, fmt.Sprintf("bundle %q for ClusterExtension %q", rel.Labels[bundleNameKey], name))
				}
			}
		}
	}

	// deterministic ordering
	sort.Strings(incompatibleOperators)

	return incompatibleOperators, errors.Join(errs...)
}

func propertyListFromPropertiesAnnotation(raw string) ([]property.Property, error) {
	var props []property.Property
	if err := json.Unmarshal([]byte(raw), &props); err != nil {
		return nil, fmt.Errorf("failed to unmarshal properties annotation: %w", err)
	}
	return props, nil
}

func (c *incompatibleOperatorController) buildHelmStore(secretClient v1.SecretInterface) helm.Storage {
	log := func(s string, args ...interface{}) { c.logger.Info(fmt.Sprintf(s, args...)) }
	csConfig := storage.ChunkedSecretsConfig{Log: log}

	return helm.Storage{
		Driver: storage.NewChunkedSecrets(secretClient, "operator-controller", csConfig),
		Log:    log,
	}
}
