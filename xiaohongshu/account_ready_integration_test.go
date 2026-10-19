//go:build integration

package xiaohongshu

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
)

// 账号数据已就绪时，不应继续等待无关图片。
func TestAccountReadinessIgnoresSlowImages(t *testing.T) {
	b := browser.NewBrowser(true)
	defer b.Close()
	cases := []struct {
		name, html string
		wait       func(*rod.Page) error
	}{
		{"login", `<div class="main-container"><div class="user"><a class="link-wrapper" href="/user/profile/fixture"><span class="channel">我</span></a></div></div><script>window.__INITIAL_STATE__={user:{loggedIn:true}}</script>`, waitLoginPageReady},
		{"unread", `<script>window.__INITIAL_STATE__={notification:{isUnreadCountInitialized:true,notificationCount:{mentions:0,likes:0,connections:0,unreadCount:0}}}</script>`, waitUnreadPageReady},
		{"profile", `<script>window.__INITIAL_STATE__={user:{userFetchingStatus:'resolved',userNoteFetchingStatus:['resolved','resolved','resolved','resolved','resolved'],isFetchingNotes:[false,false,false,false,false],userPageData:{value:{basicInfo:{nickname:'fixture'},interactions:[]}},notes:{value:[[]]},activeTab:{value:{query:'note',index:0}}}}</script>`, func(p *rod.Page) error { return waitProfilePageReady(p, TabNotes) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/slow.png" {
					select {
					case <-time.After(3 * time.Second):
					case <-r.Context().Done():
					}
					return
				}
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprintf(w, `%s<img src="/slow.png">`, c.html)
			}))
			defer s.Close()
			p := b.NewPage()
			defer p.Close()
			p.MustNavigate(s.URL)
			started := time.Now()
			require.NoError(t, c.wait(p.Timeout(5*time.Second)))
			require.Less(t, time.Since(started), 2*time.Second)
		})
	}
}

func TestAccountReadinessAuthenticationAndRendering(t *testing.T) {
	b := browser.NewBrowser(true)
	defer b.Close()
	t.Run("初始游客随后认证成功不能误报", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<button id="login-btn">登录</button><script>
		window.__INITIAL_STATE__={user:{loggedIn:false,userInfo:{value:{guest:true}}}};
		setTimeout(()=>{window.__INITIAL_STATE__.user.loggedIn=true;document.body.insertAdjacentHTML('beforeend','<div class="main-container"><div class="user"><a class="link-wrapper"><span class="channel">我</span></a></div></div>')},250);
		</script>`)
		require.NoError(t, waitLoginPageReady(p.Timeout(2*time.Second)))
		require.True(t, p.MustEval(`()=>window.__INITIAL_STATE__.user.loggedIn`).Bool())
	})
	t.Run("主页状态先于标签控件渲染", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<script>
		window.__INITIAL_STATE__={user:{userFetchingStatus:'resolved',userNoteFetchingStatus:['resolved','pending'],isFetchingNotes:[false,true],userPageData:{value:{basicInfo:{nickname:'fixture'},interactions:[]}},notes:{value:[[],[]]},activeTab:{value:{query:'note',index:0}}}};
		setTimeout(()=>{const button=document.createElement('button');button.className='reds-tab-item sub-tab-list';button.textContent='收藏';button.onclick=()=>{const u=window.__INITIAL_STATE__.user;u.activeTab.value={query:'fav',index:1};u.userNoteFetchingStatus[1]='resolved';u.isFetchingNotes[1]=false;u.notes.value[1]=[{id:'favorite'}]};document.body.appendChild(button)},250);
		</script>`)
		require.NoError(t, NewUserProfileAction(p).selectTab(p.GetContext(), p.Timeout(5*time.Second), TabFavorites))
	})
}

// 前一页的完整状态不能使下一页的导航提前结束。
func TestAccountReadinessWaitsForNewDocument(t *testing.T) {
	b := browser.NewBrowser(true)
	defer b.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow.png" {
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<script>window.__INITIAL_STATE__={};setTimeout(()=>window.__INITIAL_STATE__={user:{userFetchingStatus:'resolved',userNoteFetchingStatus:['resolved'],isFetchingNotes:[false],userPageData:{value:{basicInfo:{nickname:'new'},interactions:[]}},notes:{value:[[]]},activeTab:{value:{query:'note',index:0}}}},250)</script><img src="/slow.png">`)
	}))
	defer s.Close()
	p := b.NewPage()
	defer p.Close()
	p.MustSetDocumentContent(`<script>window.__INITIAL_STATE__={user:{userFetchingStatus:'resolved',userNoteFetchingStatus:['resolved'],isFetchingNotes:[false],userPageData:{value:{basicInfo:{nickname:'old'},interactions:[]}},notes:{value:[[]]},activeTab:{value:{query:'note',index:0}}}}</script>`)
	started := time.Now()
	require.NoError(t, navigateProfileDocument(p.Timeout(3*time.Second), s.URL))
	require.NoError(t, waitProfilePageReady(p.Timeout(2*time.Second), TabNotes))
	require.Equal(t, "new", p.MustEval(`()=>window.__INITIAL_STATE__.user.userPageData.value.basicInfo.nickname`).Str())
	require.Less(t, time.Since(started), 2*time.Second)
}

func TestAccountReadinessPlainProfileState(t *testing.T) {
	b := browser.NewBrowser(true)
	defer b.Close()
	p := b.NewPage()
	defer p.Close()
	p.MustSetDocumentContent(`<script>window.__INITIAL_STATE__={user:{userFetchingStatus:'resolved',userNoteFetchingStatus:['resolved'],isFetchingNotes:[false],userPageData:{basicInfo:{nickname:'plain'},interactions:[]},notes:[[]],activeTab:{query:'note',index:0}}}</script>`)
	require.NoError(t, waitProfilePageReady(p.Timeout(time.Second), TabNotes))
	data, err := NewUserProfileAction(p).extractUserProfileData(p, TabNotes)
	require.NoError(t, err)
	require.NotNil(t, data)
}

func TestAccountReadinessNavigationFailureCleanup(t *testing.T) {
	b := browser.NewBrowser(true)
	defer b.Close()
	p := b.NewPage()
	defer p.Close()
	require.Error(t, navigateProfileDocument(p.Timeout(time.Second), "http://[invalid"))
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>ready</html>") }))
	defer s.Close()
	require.NoError(t, navigateProfileDocument(p.Timeout(2*time.Second), s.URL))
}

func TestAccountReadinessWaitsForCompleteState(t *testing.T) {
	b := browser.NewBrowser(true)
	defer b.Close()
	t.Run("游客界面加载完成才判定未登录", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<button id="login-btn">登录</button><script>
		setTimeout(()=>window.__INITIAL_STATE__={user:{userInfo:{value:{guest:true}}}},250);
		</script>`)
		started := time.Now()
		require.NoError(t, waitLoginPageReady(p.Timeout(2*time.Second)))
		require.GreaterOrEqual(t, time.Since(started), 200*time.Millisecond)
	})
	t.Run("加载失败不能误报未登录", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<div>loading</div>`)
		require.Error(t, waitLoginPageReady(p.Timeout(100*time.Millisecond)))
	})
	t.Run("未读数缺失字段继续等待", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<script>
		window.__INITIAL_STATE__={notification:{isUnreadCountInitialized:true,notificationCount:{mentions:0}}};
		setTimeout(()=>Object.assign(window.__INITIAL_STATE__.notification.notificationCount,{likes:0,connections:0,unreadCount:0}),250);
		</script>`)
		started := time.Now()
		require.NoError(t, waitUnreadPageReady(p.Timeout(2*time.Second)))
		require.GreaterOrEqual(t, time.Since(started), 200*time.Millisecond)
	})
	t.Run("主页等待目标标签数据", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<script>
		window.__INITIAL_STATE__={user:{userFetchingStatus:'resolved',userNoteFetchingStatus:['resolved','resolved','resolved','resolved','resolved'],isFetchingNotes:[false,false,false,false,false],userPageData:{_value:{basicInfo:{nickname:'fixture'},interactions:[]}},notes:{_value:[[],[]]},activeTab:{_value:{query:'note',index:0}}}};
		setTimeout(()=>{const u=window.__INITIAL_STATE__.user;u.notes._value[1]=[{id:'favorite'}];u.activeTab._value={query:'fav',index:1}},250);
		</script>`)
		require.NoError(t, waitProfilePageReady(p.Timeout(2*time.Second), TabFavorites))
		data, err := NewUserProfileAction(p).extractUserProfileData(p, TabFavorites)
		require.NoError(t, err)
		require.Len(t, data.Feeds, 1)
		require.Equal(t, "favorite", data.Feeds[0].ID)
	})
	t.Run("空主页可以正常返回", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<script>window.__INITIAL_STATE__={user:{userFetchingStatus:'resolved',userNoteFetchingStatus:['resolved','resolved','resolved','resolved','resolved'],isFetchingNotes:[false,false,false,false,false],userPageData:{value:{basicInfo:{nickname:'fixture'},interactions:[]}},notes:{value:[[]]},activeTab:{value:{query:'note',index:0}}}}</script>`)
		require.NoError(t, waitProfilePageReady(p.Timeout(time.Second), TabNotes))
		data, err := NewUserProfileAction(p).extractUserProfileData(p, TabNotes)
		require.NoError(t, err)
		require.Empty(t, data.Feeds)
	})
	t.Run("标签不匹配且超时返回错误", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<div>loading</div>`)
		require.Error(t, waitProfilePageReady(p.Timeout(100*time.Millisecond), TabLiked))
	})
	t.Run("初始零计数不能提前返回", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<script>
		window.__INITIAL_STATE__={notification:{isUnreadCountInitialized:false,notificationCount:{mentions:0,likes:0,connections:0,unreadCount:0}}};
		setTimeout(()=>{const n=window.__INITIAL_STATE__.notification;n.notificationCount.mentions=2;n.notificationCount.unreadCount=2;n.isUnreadCountInitialized=true},250);
		</script>`)
		require.NoError(t, waitUnreadPageReady(p.Timeout(2*time.Second)))
		require.Equal(t, 2, p.MustEval(`() => window.__INITIAL_STATE__.notification.notificationCount.unreadCount`).Int())
	})
	t.Run("切标签后初始空数组不能提前返回", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<script>
		window.__INITIAL_STATE__={user:{userFetchingStatus:'resolved',userNoteFetchingStatus:['resolved','resolved','resolved','resolved','resolved'],isFetchingNotes:[false,false,false,false,false],userPageData:{value:{basicInfo:{nickname:'fixture'},interactions:[]}},notes:{value:[[],[]]},activeTab:{value:{query:'fav',index:1}},userFetchingStatus:'resolved',userNoteFetchingStatus:['resolved','pending'],isFetchingNotes:[false,true]}};
		setTimeout(()=>{const u=window.__INITIAL_STATE__.user;u.notes.value[1]=[{id:'loaded'}];u.userNoteFetchingStatus[1]='resolved';u.isFetchingNotes[1]=false},250);
		</script>`)
		require.NoError(t, waitProfilePageReady(p.Timeout(2*time.Second), TabFavorites))
		data, err := NewUserProfileAction(p).extractUserProfileData(p, TabFavorites)
		require.NoError(t, err)
		require.Len(t, data.Feeds, 1)
	})
}
