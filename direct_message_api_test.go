package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 漏注册或标成写工具会破坏客户端发现与只读审批。
func TestDirectMessageToolsReadOnly(t *testing.T) {
	router := setupRoutes(NewAppServer(NewXiaohongshuService(), ""))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	router.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var result struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Annotations struct {
					ReadOnly bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	found := map[string]bool{}
	for _, tool := range result.Result.Tools {
		if tool.Name == "list_dm_conversations" || tool.Name == "get_dm_messages" {
			found[tool.Name] = true
			assert.True(t, tool.Annotations.ReadOnly)
		}
	}
	assert.True(t, found["list_dm_conversations"])
	assert.True(t, found["get_dm_messages"])
}

// 非法分页必须在启动浏览器前返回，不能默默按默认参数请求。
func TestDirectMessageHTTPValidation(t *testing.T) {
	router := setupRoutes(NewAppServer(NewXiaohongshuService(), ""))
	for _, path := range []string{
		"/api/v1/dm/conversations?limit=bad",
		"/api/v1/dm/conversations?page=-1",
		"/api/v1/dm/conversations?limit=101",
		"/api/v1/dm/messages",
		"/api/v1/dm/messages?user_id=peer&last_id=-1",
		"/api/v1/dm/messages?user_id=peer&last_id=no",
		"/api/v1/dm/messages?user_id=peer&limit=101",
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusBadRequest, w.Code, path)
	}
	for _, path := range []string{"/api/v1/dm/conversations", "/api/v1/dm/messages"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	}
}

func TestDirectMessageEndpointsRequireAuth(t *testing.T) {
	router := setupRoutes(NewAppServer(NewXiaohongshuService(), "secret"))
	for _, path := range []string{"/api/v1/dm/conversations", "/api/v1/dm/messages?user_id=peer"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	}
}
