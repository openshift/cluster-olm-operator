package controller

import (
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/apiserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// TLSSecurityProfileConfigPath returns the path for the observed TLS security profile configuration.
func TLSSecurityProfileConfigPath() []string {
	return []string{"olmTLSSecurityProfile"}
}

// TLSMinVersionPath returns the path for the observed minimum TLS version.
func TLSMinVersionPath() []string {
	return []string{"olmTLSSecurityProfile", "minTLSVersion"}
}

// TLSCipherSuitesPath returns the path for the observed TLS cipher suites.
func TLSCipherSuitesPath() []string {
	return []string{"olmTLSSecurityProfile", "cipherSuites"}
}

// TLSCurvePreferencesPath returns the path for the observed TLS curve preferences.
func TLSCurvePreferencesPath() []string {
	return []string{"olmTLSSecurityProfile", "curvePreferences"}
}

// OLMConfigObserverListers implements the configobserver.Listers interface and
// apiserver.APIServerLister for use with the library-go ObserveTLSSecurityProfile
type OLMConfigObserverListers struct {
	APIServerListerImpl configlistersv1.APIServerLister
	ResourceSync        resourcesynccontroller.ResourceSyncer
	PreRunCachesSynced  []cache.InformerSynced
}

func (l OLMConfigObserverListers) APIServerLister() configlistersv1.APIServerLister {
	klog.Info("TLS observer: APIServerLister() called")
	return l.APIServerListerImpl
}

func (l OLMConfigObserverListers) ResourceSyncer() resourcesynccontroller.ResourceSyncer {
	klog.Info("TLS observer: ResourceSyncer() called")
	return l.ResourceSync
}

func (l OLMConfigObserverListers) PreRunHasSynced() []cache.InformerSynced {
	klog.Infof("TLS observer: PreRunHasSynced() called, returning %d synced informers", len(l.PreRunCachesSynced))
	return l.PreRunCachesSynced
}

// TLSObserverController creates a config observer controller that observes TLS security profiles
// using the library-go ObserveTLSSecurityProfile function
type TLSObserverController struct {
	factory.Controller
}

// NewTLSObserverController returns a new TLS observer controller using library-go patterns
func NewTLSObserverController(
	name string,
	operatorClient v1helpers.OperatorClient,
	configInformers configinformers.SharedInformerFactory,
	eventRecorder events.Recorder,
) *TLSObserverController {
	klog.Infof("Creating TLS observer controller: %s", name)

	informers := []factory.Informer{
		operatorClient.Informer(),
		configInformers.Config().V1().APIServers().Informer(),
	}

	klog.Infof("TLS observer controller %s: registering informers for operator client and APIServer config", name)

	c := &TLSObserverController{
		Controller: configobserver.NewConfigObserver(
			name,
			operatorClient,
			eventRecorder.WithComponentSuffix("tls-config-observer-controller"),
			OLMConfigObserverListers{
				APIServerListerImpl: configInformers.Config().V1().APIServers().Lister(),
				PreRunCachesSynced: []cache.InformerSynced{
					operatorClient.Informer().HasSynced,
					configInformers.Config().V1().APIServers().Informer().HasSynced,
				},
			},
			informers,
			observeTLSSecurityProfileForOLM,
		),
	}

	klog.Infof("TLS observer controller %s: successfully created with library-go config observer", name)
	return c
}

// observeTLSSecurityProfileForOLM uses the library-go ObserveTLSSecurityProfile function
// with OLM-specific configuration paths
func observeTLSSecurityProfileForOLM(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	klog.Info("TLS observer: starting TLS security profile observation")

	minTLSPath := TLSMinVersionPath()
	cipherSuitesPath := TLSCipherSuitesPath()
	curvePreferencesPath := TLSCurvePreferencesPath()
	klog.Infof("TLS observer: using minTLSVersion path: %v", minTLSPath)
	klog.Infof("TLS observer: using cipherSuites path: %v", cipherSuitesPath)
	klog.Infof("TLS observer: using curvePreferences path: %v", curvePreferencesPath)

	logTLSConfig("existing", existingConfig, minTLSPath, cipherSuitesPath, curvePreferencesPath)

	observedConfig, errs := apiserver.ObserveTLSSecurityProfileWithGroupPaths(
		genericListers,
		recorder,
		existingConfig,
		minTLSPath,
		cipherSuitesPath,
		curvePreferencesPath,
	)

	if len(errs) > 0 {
		klog.Warningf("TLS observer: encountered %d errors during observation", len(errs))
		for i, err := range errs {
			klog.Warningf("TLS observer: error %d: %v", i+1, err)
		}
	} else {
		klog.Info("TLS observer: observation completed without errors")
	}

	logTLSConfig("observed", observedConfig, minTLSPath, cipherSuitesPath, curvePreferencesPath)

	klog.Info("TLS observer: completed TLS security profile observation")
	return observedConfig, errs
}

func logTLSConfig(label string, config map[string]interface{}, minTLSPath, cipherSuitesPath, curvePreferencesPath []string) {
	if len(config) == 0 {
		klog.Infof("TLS observer: no %s configuration found", label)
		return
	}
	klog.Infof("TLS observer: %s config keys: %v", label, getMapKeys(config))
	if v, found, err := unstructured.NestedString(config, minTLSPath...); err != nil {
		klog.Warningf("TLS observer: error reading minTLSVersion from %s config: %v", label, err)
	} else if found {
		klog.Infof("TLS observer: %s minTLSVersion: %s", label, v)
	}
	if v, found, err := unstructured.NestedStringSlice(config, cipherSuitesPath...); err != nil {
		klog.Warningf("TLS observer: error reading cipherSuites from %s config: %v", label, err)
	} else if found {
		klog.Infof("TLS observer: %s cipherSuites count: %d", label, len(v))
	}
	if v, found, err := unstructured.NestedStringSlice(config, curvePreferencesPath...); err != nil {
		klog.Warningf("TLS observer: error reading curvePreferences from %s config: %v", label, err)
	} else if found {
		klog.Infof("TLS observer: %s curvePreferences count: %d", label, len(v))
	}
}

// getMapKeys is a helper function to extract keys from a map for logging purposes
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
