package dataplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/kong/kubernetes-ingress-controller/v3/internal/adminapi"
	"github.com/kong/kubernetes-ingress-controller/v3/internal/annotations"
	dpconf "github.com/kong/kubernetes-ingress-controller/v3/internal/dataplane/config"
	"github.com/kong/kubernetes-ingress-controller/v3/internal/dataplane/configfetcher"
	"github.com/kong/kubernetes-ingress-controller/v3/internal/dataplane/fallback"
	"github.com/kong/kubernetes-ingress-controller/v3/internal/dataplane/sendconfig"
	"github.com/kong/kubernetes-ingress-controller/v3/internal/dataplane/translator"
	"github.com/kong/kubernetes-ingress-controller/v3/internal/store"
	"github.com/kong/kubernetes-ingress-controller/v3/internal/util"
	kongv1 "github.com/kong/kubernetes-ingress-controller/v3/pkg/apis/configuration/v1"
	"github.com/kong/kubernetes-ingress-controller/v3/test/mocks"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// referenceConfig is a reference configuration that is used to test the fallback mechanism.
// It shall include every supported resource type.
var referenceConfig = []client.Object{
	&kongv1.KongPlugin{

	}
}

// TestKongClient_FallbackIntegrationTest is an integration (in terms of using mostly real dependencies of KongClient)
// test that exercises the fallback configuration mechanism.
func TestKongClient_FallbackIntegrationTest(t *testing.T) {
	ctx := context.Background()
	adminURL := setupAdminAPIServer(t, nil)
	gwClient, err := adminapi.NewTestClient(adminURL)
	require.NoError(t, err)
	kongClient := setupKongClient(t, gwClient)

	err = kongClient.Update(ctx)
	require.Error(t, err)

	currentConfig, err := gwClient.AdminAPIClient().Config(ctx)
	require.NoError(t, err)

	// Some assertion based on the configuration in test case.
	_ = currentConfig
}

func setupKongClient(t *testing.T, gwClient *adminapi.Client) *KongClient {
	logger := logr.Discard()
	kongConfig := sendconfig.Config{
		FallbackConfiguration: true,
		InMemory:              true,
	}
	translatorFeatureFlags := translator.FeatureFlags{}
	kongWorkspace := ""
	diagnostic := util.ConfigDumpDiagnostic{}
	timeout := time.Second
	eventRecorder := mocks.NewEventRecorder()
	dbMode := dpconf.DBModeOff
	ingressClassName := annotations.DefaultIngressClass

	cache := cacheStoresFromObjs(t)
	storer := store.New(cache, ingressClassName, logger)
	configTranslator, err := translator.NewTranslator(logger, storer, kongWorkspace, translatorFeatureFlags)
	require.NoError(t, err)

	clientsManager := mockGatewayClientsProvider{
		dbMode:         dbMode,
		gatewayClients: []*adminapi.Client{gwClient},
	}
	updateStrategyResolver := sendconfig.NewDefaultUpdateStrategyResolver(kongConfig, logger)
	configurationChangeDetector := sendconfig.NewDefaultConfigurationChangeDetector(logger)
	kongConfigFetcher := configfetcher.NewDefaultKongLastGoodConfigFetcher(translatorFeatureFlags.FillIDs, kongWorkspace)
	fallbackConfigGenerator := fallback.NewGenerator(fallback.NewDefaultCacheGraphProvider(), logger)
	kongClient, err := NewKongClient(
		logger,
		timeout,
		diagnostic,
		kongConfig,
		eventRecorder,
		dbMode,
		clientsManager,
		updateStrategyResolver,
		configurationChangeDetector,
		kongConfigFetcher,
		configTranslator,
		cache,
		fallbackConfigGenerator,
	)
	require.NoError(t, err)
	return kongClient
}

func setupAdminAPIServer(t *testing.T, brokenObjects []client.Object) (url string) {
	// Start a mock Kong Admin API server.
	adminAPIHandler := mocks.NewAdminAPIHandler(t,
		mocks.WithConfigPostErrorOnlyOnFirstRequest(),
		mocks.WithConfigPostError(postConfigErrorResponse(t, brokenObjects)),
	)
	adminAPIServer := httptest.NewServer(adminAPIHandler)
	t.Cleanup(adminAPIServer.Close)
	return adminAPIServer.URL
}

func postConfigErrorResponse(t *testing.T, brokenObjects ...[]client.Object) []byte {
	type entityError struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Field   string `json:"field"`
	}

	type flattenedError struct {
		EntityTags []string      `json:"entity_tags"`
		Errors     []entityError `json:"errors"`
	}
	type responseBody struct {
		FlattenedErrors []flattenedError `json:"flattened_errors"`
	}

	var flattenedErrors []flattenedError
	for _, objs := range brokenObjects {
		var entityTags []string
		for _, obj := range objs {
			entityTags = append(entityTags, fmt.Sprintf("k8s-name:%s", obj.GetName()))
			entityTags = append(entityTags, fmt.Sprintf("k8s-namespace:%s", obj.GetNamespace()))
			entityTags = append(entityTags, fmt.Sprintf("k8s-kind:%s", obj.GetObjectKind().GroupVersionKind().Kind))
			entityTags = append(entityTags, fmt.Sprintf("k8s-uid:%s", string(obj.GetUID())))
			entityTags = append(entityTags, fmt.Sprintf("k8s-group:%s", obj.GetObjectKind().GroupVersionKind().Group))
			entityTags = append(entityTags, fmt.Sprintf("k8s-version:%s", obj.GetObjectKind().GroupVersionKind().Version))
		}

		flattenedErrors = append(flattenedErrors, flattenedError{
			EntityTags: entityTags,
			Errors: []entityError{
				{
					Message: "violated constraint",
					Type:    "field",
					Field:   "protocol",
				},
			},
		})
	}

	b, err := json.Marshal(responseBody{
		FlattenedErrors: flattenedErrors,
	})
	require.NoError(t, err)
	return b
}
