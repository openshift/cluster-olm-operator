package controller_test

import (
	"os"
	"testing"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-olm-operator/pkg/controller"
)

func TestProxyController(t *testing.T) {
	testdata := []controller.MockProxyClient{
		{
			Proxy: configv1.Proxy{
				Status: configv1.ProxyStatus{
					HTTPProxy:  controller.HTTPProxy,
					HTTPSProxy: controller.HTTPSProxy,
					NoProxy:    controller.NoProxy,
				},
			},
		},
		{
			Proxy: configv1.Proxy{},
		},
	}

	for _, test := range testdata {
		logger := logr.New(nil)
		pc := test
		err := controller.UpdateProxyEnvironment(logger, &pc)
		if err != nil {
			t.Fatalf("UpdateProxyEnvironment() failed: %v", err)
		}

		v, ok := os.LookupEnv(controller.HTTPProxy)
		if ok == (test.Status.HTTPProxy == "") {
			t.Fatalf("HttpProxy unexpected ok value: %v", ok)
		}
		if v != test.Status.HTTPProxy {
			t.Fatalf("HttpProxy expected value %q to be %q", v, test.Status.HTTPProxy)
		}

		v, ok = os.LookupEnv(controller.HTTPSProxy)
		if ok == (test.Status.HTTPSProxy == "") {
			t.Fatalf("HttpsProxy unexpected ok value: %v", ok)
		}
		if v != test.Status.HTTPSProxy {
			t.Fatalf("HttpsProxy expected value %q to be %q", v, test.Status.HTTPSProxy)
		}

		v, ok = os.LookupEnv(controller.NoProxy)
		if ok == (test.Status.NoProxy == "") {
			t.Fatalf("NoProxy unexpected ok value: %v", ok)
		}
		if v != test.Status.NoProxy {
			t.Fatalf("NoProxy expected value %q to be %q", v, test.Status.NoProxy)
		}
	}
}
