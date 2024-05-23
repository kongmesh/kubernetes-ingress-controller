package envtest

import (
	"context"
	"testing"
	"time"

	"github.com/kong/kubernetes-ingress-controller/v3/internal/adminapi"
	"github.com/kong/kubernetes-ingress-controller/v3/test/internal/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFallbackConfiguration(t *testing.T) {
	t.Parallel()

	const (
		waitTime = time.Minute
		tickTime = 100 * time.Millisecond
	)

	scheme := Scheme(t, WithKong, WithGatewayAPI)
	envcfg := Setup(t, scheme)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctrlClient := NewControllerClient(t, scheme, envcfg)
	ns := CreateNamespace(ctx, t, ctrlClient)
	ingressClassName := "kongenvtest"
	deployIngressClass(ctx, t, ingressClassName, ctrlClient)

	mgrCfg, _ := RunManager(ctx, t, envcfg,
		AdminAPIOptFns(),
		WithGatewayAPIControllers(),
		WithPublishService(ns.Name),
		WithIngressClass(ingressClassName),
		WithProxySyncSeconds(0.1),
		WithFallbackConfigurationFeatureEnabled(),
	)
	require.Len(t, mgrCfg.KongAdminURLs, 1, "expecting one Kong Admin URL")
	kongAdminURL := mgrCfg.KongAdminURLs[0]

	kongClient, err := adminapi.NewKongAPIClient(kongAdminURL, helpers.DefaultHTTPClient())
	require.NoError(t, err)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		currentConfig, err := kongClient.Config(ctx)
		assert.NoError(t, err)
		assert.NotEmptyf(t, currentConfig, "expected non-empty Kong configuration")
	}, waitTime, tickTime)

	// I want to responds to every first request to POST /config with 400 and every second request (fallback) with 200.
	// I want to capture the second request to POST /config and check if it is an expected fallback request.
}
