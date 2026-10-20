//go:build integration

package xiaohongshu

import (
	"net/url"
	"path/filepath"
	"sync"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
)

// 验证页面 GET 桥接、凭据模式及 JSON 大整数；请求全部由本地夹具响应。
func TestDMReadJSONBrowser(t *testing.T) {
	t.Setenv("COOKIES_PATH", filepath.Join(t.TempDir(), "absent.json"))
	b := browser.NewBrowser(true)
	defer b.Close()
	p := b.NewPage()
	defer p.Close()
	require.NoError(t, p.Browser().SetCookies([]*proto.NetworkCookieParam{{Name: "dm_fixture", Value: "test-only", Domain: ".xiaohongshu.com", Path: "/", Secure: true, SameSite: proto.NetworkCookieSameSiteNone}}))
	var mu sync.Mutex
	var requests []string
	var credentialIncluded bool
	router := p.HijackRequests()
	require.NoError(t, router.Add("*", "", func(h *rod.Hijack) {
		path := h.Request.URL().Path
		h.Response.SetHeader("Access-Control-Allow-Origin", "https://www.xiaohongshu.com", "Access-Control-Allow-Credentials", "true")
		if path == "/explore" {
			h.Response.SetHeader("Content-Type", "text/html").SetBody("<html><body>fixture</body></html>")
			return
		}
		if path == "/favicon.ico" {
			h.Response.SetBody("")
			return
		}
		mu.Lock()
		requests = append(requests, h.Request.Method()+" "+h.Request.URL().RequestURI())
		if path == "/api/im/web/messages/history" {
			credentialIncluded = h.Request.Header("Cookie") == "dm_fixture=test-only"
		}
		mu.Unlock()
		switch path {
		case "/api/im/web/messages/history":
			h.Response.SetHeader("Content-Type", "application/json").SetBody(`{"code":0,"success":true,"data":{"out_message_list":[{"id":9007199254740993,"store_id":9007199254740993,"content":"hello"}]}}`)
		case "/denied":
			h.Response.Payload().ResponseCode = 403
			h.Response.SetBody("denied")
		case "/logged-out":
			h.Response.SetBody(`{"code":-100,"success":false,"data":{}}`)
		case "/changed":
			h.Response.SetBody("<html>login</html>")
		default:
			h.Response.SetBody("unexpected")
		}
	}))
	go router.Run()
	defer router.Stop()
	p.MustNavigate("https://www.xiaohongshu.com/explore").MustWaitLoad()
	data, err := readDMJSON(p, "/api/im/web/messages/history", url.Values{"chat_user_id": {"peer"}, "last_id": {"9007199254740994"}, "limit": {"20"}})
	require.NoError(t, err)
	result, err := parseDMHistory(data, "peer", 20)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
	assert.Equal(t, "9007199254740993", result.Messages[0].ID)
	for _, path := range []string{"/denied", "/logged-out", "/changed"} {
		_, err := readDMJSON(p, path, nil)
		assert.Error(t, err, path)
	}
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, requests, 4)
	assert.True(t, credentialIncluded, "GET 必须携带浏览器登录凭据")
	assert.Equal(t, "GET /api/im/web/messages/history?chat_user_id=peer&last_id=9007199254740994&limit=20", requests[0])
	for _, request := range requests {
		assert.Contains(t, request, "GET ")
	}
}
