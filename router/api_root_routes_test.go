package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminRootRoutesUseCanonicalPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	previousRateLimitSetting := common.GlobalApiRateLimitEnable
	common.GlobalApiRateLimitEnable = false
	t.Cleanup(func() {
		common.GlobalApiRateLimitEnable = previousRateLimitSetting
	})

	engine := gin.New()
	SetApiRouter(engine)

	routes := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	testCases := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/channel"},
		{method: http.MethodPost, path: "/api/channel"},
		{method: http.MethodPut, path: "/api/channel"},
		{method: http.MethodGet, path: "/api/data"},
		{method: http.MethodGet, path: "/api/group"},
		{method: http.MethodGet, path: "/api/log"},
		{method: http.MethodGet, path: "/api/mj"},
		{method: http.MethodGet, path: "/api/task"},
		{method: http.MethodGet, path: "/api/prefill_group"},
		{method: http.MethodPost, path: "/api/prefill_group"},
		{method: http.MethodPut, path: "/api/prefill_group"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			_, hasCanonicalPath := routes[testCase.method+" "+testCase.path]
			_, hasLegacyPath := routes[testCase.method+" "+testCase.path+"/"]
			require.True(t, hasCanonicalPath)
			require.False(t, hasLegacyPath)

			request := httptest.NewRequest(testCase.method, testCase.path, nil)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			require.Equal(t, http.StatusUnauthorized, response.Code)
			require.Contains(t, response.Body.String(), "AUTH_UNAUTHORIZED")
		})
	}
}
