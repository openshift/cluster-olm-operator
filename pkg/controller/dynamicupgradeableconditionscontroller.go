package controller

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const (
	incompatibleOperatorsInstalled = "IncompatibleOperatorsInstalled"
	MaxOpenShiftVersionProperty    = "olm.maxOpenShiftVersion"
)

type dynamicUpgradeableConditionController struct {
	informer       cache.SharedIndexInformer
	client         kubernetes.Interface
	name           string
	operatorClient *clients.OperatorClient
	prefixes       []string
}

type Chart struct {
	Metadata struct {
		Annotations map[string]string `yaml:"annotations"`
	} `yaml:"metadata"`
}

type SecretData struct {
	Chart struct {
		Chart
	} `yaml:"chart"`
}

func NewDynamicUpgradeableConditionController(client kubernetes.Interface, name string, operatorClient *clients.OperatorClient, eventRecorder events.Recorder, prefixes []string) factory.Controller {
	informer := informers.NewSharedInformerFactoryWithOptions(client, 0).Core().V1().Secrets().Informer()

	c := &dynamicUpgradeableConditionController{
		informer:       informer,
		client:         client,
		name:           name,
		operatorClient: operatorClient,
		prefixes:       prefixes,
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.handleSecret,
		UpdateFunc: func(oldObj, newObj interface{}) { c.handleSecret(newObj) },
		DeleteFunc: c.handleSecret,
	})

	return factory.New().WithInformers(informer).ToController(name, eventRecorder)
}

func (c *dynamicUpgradeableConditionController) handleSecret(obj interface{}) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		log.Println("Error: could not cast to Secret object")
		return
	}

	if _, exists := secret.Labels["olm.operatorframework.io/owner-kind"]; exists {
		if secret.Labels["olm.operatorframework.io/owner-kind"] == "ClusterExtension" {
			c.processSecret(secret)
		}
	}
}

func (c *dynamicUpgradeableConditionController) processSecret(secret *corev1.Secret) {
	current, err := getCurrentRelease()
	if err != nil {
		log.Printf("Error processing secrets: %v", err)
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
		log.Printf("Error processing secrets: %v", err)
		return
	}

	var secretsWithAnnotation []string

	// List all secrets with the label
	secrets, err := c.client.CoreV1().Secrets(secret.Namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: "olm.operatorframework.io/owner-kind=ClusterExtension",
	})
	if err != nil {
		log.Printf("Error listing secrets: %v", err)
		return
	}

	for _, s := range secrets.Items {
		if data, exists := s.Data["data"]; exists {
			decodedData, err := base64.StdEncoding.DecodeString(string(data))
			if err != nil {
				log.Printf("Error decoding base64 data for secret %s: %v", s.Name, err)
				continue
			}

			gunzippedData, err := gunzipData(decodedData)
			if err != nil {
				log.Printf("Error gunzipping data for secret %s: %v", s.Name, err)
				continue
			}

			version, found := getAnnotationValue(gunzippedData, MaxOpenShiftVersionProperty)
			if found {
				maxOCPVersion, err := toSemver(version)
				if err != nil {
					log.Printf("Error converting to semver for secret %s: %v", s.Name, err)
					continue
				}
				if maxOCPVersion == nil || maxOCPVersion.GTE(next) {
					// All good
				} else {
					// Incompatible
					name := secret.Labels["olm_operatorframework_io_bundle_name"]
					secretsWithAnnotation = append(secretsWithAnnotation, name)
				}
			}
		}
	}

	updateStatusFuncs := make([]v1helpers.UpdateStatusFunc, 0, 1)
	if len(secretsWithAnnotation) > 0 {
		updateStatusFuncs = append(updateStatusFuncs, v1helpers.UpdateConditionFn(operatorv1.OperatorCondition{
			Type:    "Upgradeable",
			Status:  operatorv1.ConditionFalse,
			Reason:  incompatibleOperatorsInstalled,
			Message: strings.Join(secretsWithAnnotation, ","),
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

func getAnnotationValue(data string, annotationKey string) (string, bool) {
	var chart Chart

	err := json.Unmarshal([]byte(data), &chart)
	if err != nil {
		log.Printf("Error unmarshaling JSON data: %v", err)
		return "", false
	}

	value, found := chart.Metadata.Annotations[annotationKey]
	return value, found
}

func gunzipData(data []byte) (string, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer reader.Close()

	unzippedData, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read gunzipped data: %w", err)
	}

	return string(unzippedData), nil
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
