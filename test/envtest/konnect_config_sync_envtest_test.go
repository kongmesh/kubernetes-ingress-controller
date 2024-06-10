//go:build envtest

package envtest

import (
	"context"
	"testing"
)

// TestGatewayAPIControllersMayBeDynamicallyStarted ensures that in case of missing CRDs installation in the
// cluster, specific controllers are not started until the CRDs are installed.
func TestKonnectConfigSync(t *testing.T) {
	t.Parallel()

	scheme := Scheme(t, WithKong)
	envcfg := Setup(t, scheme, WithInstallGatewayCRDs(false))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	RunManager(ctx, t, envcfg,
		AdminAPIOptFns(),
		WithGatewayFeatureEnabled,
		WithGatewayAPIControllers(),
		WithPublishService("ns"),
	)
}
