//go:build integration

package xiaohongshu

import (
	"context"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
)

func notificationTestPage(t *testing.T, html string, navigationDelay ...time.Duration) *rod.Page {
	t.Helper()
	b := browser.NewBrowser(true)
	t.Cleanup(func() { b.Close() })
	p := b.NewPage()
	t.Cleanup(func() { p.Close() })
	router := p.HijackRequests()
	require.NoError(t, router.Add("*", "", func(h *rod.Hijack) {
		delay := time.Duration(0)
		if h.Request.URL().Path == "/slow.png" || h.Request.URL().Query().Get("slow") != "" {
			delay = 3 * time.Second
		} else if h.Request.IsNavigation() && len(navigationDelay) > 0 {
			delay = navigationDelay[0]
		}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-h.Request.Req().Context().Done():
			}
		}
		h.Response.SetHeader("Content-Type", "text/html; charset=utf-8").SetBody(html)
	}))
	go router.Run()
	t.Cleanup(func() { router.Stop() })
	return p
}

const notificationTestState = `<script>
window.__INITIAL_STATE__={notification:{isFetching:false,isUnreadCountInitialized:true,activeTabKey:0,notificationMap:{mentions:{messageList:[],hasMore:false,cursor:'done'},likes:{messageList:[],hasMore:true,cursor:''},connections:{messageList:[],hasMore:true,cursor:''}}}};
const n=window.__INITIAL_STATE__.notification;
</script>`

// 有效空结果已到达时，不应再等图片和固定导航延时。
func TestNotificationReadinessListIgnoresImages(t *testing.T) {
	p := notificationTestPage(t, notificationTestState+`<img src="/slow.png">`)
	started := time.Now()
	result, err := NewNotificationAction(p).List(context.Background(), TabMentions, 20)
	require.NoError(t, err)
	require.Empty(t, result.Items)
	require.Less(t, time.Since(started), time.Second)
}

func TestNotificationReadinessRejectsPreviousDocument(t *testing.T) {
	p := notificationTestPage(t, notificationTestState+`<script>n.isFetching=true;setTimeout(()=>{n.notificationMap.mentions.messageList=[{id:'fresh'}];n.isFetching=false},250)</script><img src="/slow.png">`, 200*time.Millisecond)
	p.MustSetDocumentContent(notificationTestState + `<script>n.notificationMap.mentions.messageList=[{id:'stale'}]</script>`)
	result, err := NewNotificationAction(p).List(context.Background(), TabMentions, 20)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, "fresh", result.Items[0].ID)
}

// 初始 false 和空数组均不是已完成请求。
func TestNotificationReadinessInitialEmptyIsPending(t *testing.T) {
	p := notificationTestPage(t, "")
	p.MustSetDocumentContent(notificationTestState + `<script>
n.notificationMap.mentions.cursor='';
setTimeout(()=>{n.isFetching=true},100);
setTimeout(()=>{n.notificationMap.mentions={messageList:[{id:'loaded'}],hasMore:false,cursor:'next'};n.isFetching=false},400);
</script>`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	action := NewNotificationAction(p)
	require.NoError(t, action.loadUntil(ctx, p.Context(ctx), TabMentions, 20))
	payload, err := action.readTab(p, TabMentions)
	require.NoError(t, err)
	require.Len(t, payload.MessageList, 1)
	require.Equal(t, "loaded", payload.MessageList[0].ID)
}

func TestNotificationReadinessDelayedEmptyAndRefs(t *testing.T) {
	for _, wrapper := range []string{"value", "_value"} {
		t.Run(wrapper, func(t *testing.T) {
			p := notificationTestPage(t, notificationTestState+`<script>
n.isFetching={`+wrapper+`:false};n.activeTabKey={`+wrapper+`:0};n.notificationMap.mentions.cursor='';
setTimeout(()=>{n.isFetching.`+wrapper+`=true},100);
setTimeout(()=>{n.notificationMap.mentions.cursor='end';n.isFetching.`+wrapper+`=false;window.finished=true},500);
</script><img src="/slow.png">`)
			started := time.Now()
			result, err := NewNotificationAction(p).List(context.Background(), TabMentions, 20)
			require.NoError(t, err)
			require.Empty(t, result.Items)
			require.True(t, p.MustEval(`()=>window.finished===true`).Bool())
			require.Less(t, time.Since(started), 2*time.Second)
		})
	}
}

func TestNotificationReadinessDelayedTab(t *testing.T) {
	p := notificationTestPage(t, "")
	p.MustSetDocumentContent(notificationTestState + `<script>
setTimeout(()=>{const button=document.createElement('button');button.className='reds-tab-item';button.textContent='赞和收藏';button.onclick=()=>{n.activeTabKey=1;n.isFetching=true;setTimeout(()=>{n.notificationMap.likes={messageList:[{id:'like'}],hasMore:false,cursor:'like-page'};n.isFetching=false},500)};document.body.appendChild(button)},200);
</script>`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	action := NewNotificationAction(p)
	require.NoError(t, action.switchTab(ctx, p.Context(ctx), TabLikes))
	require.NoError(t, action.loadUntil(ctx, p.Context(ctx), TabLikes, 20))
	payload, err := action.readTab(p, TabLikes)
	require.NoError(t, err)
	require.Len(t, payload.MessageList, 1)
	require.Equal(t, "like", payload.MessageList[0].ID)
}

func TestNotificationReadinessSkipsUnrelatedDefaultRequest(t *testing.T) {
	p := notificationTestPage(t, notificationTestState+`<button class="reds-tab-item">赞和收藏</button><script>
n.isFetching=true;n.notificationMap.mentions.cursor='';
document.querySelector('button').onclick=()=>{n.activeTabKey=1;n.isFetching=true;setTimeout(()=>{n.notificationMap.likes={messageList:[{id:'target'}],hasMore:false,cursor:'likes'};n.isFetching=false},400)};
</script>`)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	result, err := NewNotificationAction(p).List(ctx, TabLikes, 20)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, "target", result.Items[0].ID)
}

// 原始条数达到 limit，但被过滤的通知不应占返回名额。
func TestNotificationReadinessVisibleLimitPaginates(t *testing.T) {
	p := notificationTestPage(t, "")
	p.MustSetDocumentContent(notificationTestState + `<style>body{height:3000px}</style><script>
n.notificationMap.mentions={messageList:[{id:'first'},{id:'deleted',commentInfo:{id:'removed',illegalInfo:{illegalStatus:'DELETED'}}}],hasMore:true,cursor:'page1'};
let started=false;
document.addEventListener('wheel',()=>{if(started)return;started=true;n.isFetching=true;setTimeout(()=>{n.notificationMap.mentions.messageList.push({id:'second'});n.notificationMap.mentions.cursor='page2';n.notificationMap.mentions.hasMore=false;n.isFetching=false},1800)},{passive:true});
</script>`)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	action := NewNotificationAction(p)
	require.NoError(t, action.loadUntil(ctx, p.Context(ctx), TabMentions, 2))
	payload, err := action.readTab(p, TabMentions)
	require.NoError(t, err)
	items, filtered := convertNotifications(payload.MessageList, 2)
	require.Len(t, items, 2)
	require.Equal(t, "first", items[0].ID)
	require.Equal(t, "second", items[1].ID)
	require.Equal(t, 1, filtered)
}

// 长列表要滚动多次才会触发下一页，首轮没有请求不应报错。
func TestNotificationReadinessScrollsToPagination(t *testing.T) {
	p := notificationTestPage(t, "")
	p.MustSetDocumentContent(notificationTestState + `<style>body{height:3500px}</style><script>
n.notificationMap.mentions={messageList:[{id:'first'}],hasMore:true,cursor:'page1'};
let started=false;
document.addEventListener('scroll',()=>{
if(started || document.documentElement.scrollHeight-window.scrollY-window.innerHeight>100)return;
started=true;n.isFetching=true;
setTimeout(()=>{n.notificationMap.mentions.messageList.push({id:'second'});n.notificationMap.mentions.cursor='page2';n.notificationMap.mentions.hasMore=false;n.isFetching=false},200);
});
</script>`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	action := NewNotificationAction(p)
	require.NoError(t, action.loadUntil(ctx, p.Context(ctx), TabMentions, 2))
	payload, err := action.readTab(p, TabMentions)
	require.NoError(t, err)
	require.Len(t, payload.MessageList, 2)
}

func TestNotificationReadinessCancellation(t *testing.T) {
	t.Run("导航和加载", func(t *testing.T) {
		p := notificationTestPage(t, notificationTestState+`<img src="/slow.png">`)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		started := time.Now()
		_, err := NewNotificationAction(p).List(ctx, TabMentions, 20)
		require.ErrorIs(t, err, context.Canceled)
		require.Less(t, time.Since(started), 500*time.Millisecond)
	})
	t.Run("导航中途取消", func(t *testing.T) {
		p := notificationTestPage(t, notificationTestState, 3*time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		started := time.Now()
		_, err := NewNotificationAction(p).List(ctx, TabMentions, 20)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Less(t, time.Since(started), time.Second)
	})
	t.Run("请求尚未完成", func(t *testing.T) {
		p := notificationTestPage(t, "")
		p.MustSetDocumentContent(notificationTestState + `<script>n.isFetching=true;n.notificationMap.mentions.cursor='';</script>`)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		started := time.Now()
		err := NewNotificationAction(p).loadUntil(ctx, p.Context(ctx), TabMentions, 20)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Less(t, time.Since(started), 500*time.Millisecond)
	})
}
