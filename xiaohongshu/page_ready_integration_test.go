//go:build integration

package xiaohongshu

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
)

// 数据已就绪时，不应等待无关图片下载完成。
func TestReadinessDoesNotWaitForSlowImages(t *testing.T) {
	b := browser.NewBrowser(true)
	defer b.Close()
	cases := []struct {
		name, script string
		wait         func(*testing.T, string)
	}{
		{"detail", `window.__INITIAL_STATE__={note:{noteDetailMap:{fixture:{note:{noteId:"fixture",title:"ready"},comments:{list:[]}}}}}`, func(t *testing.T, url string) {
			p := b.NewPage()
			defer p.Close()
			p.MustNavigate(url)
			waitFeedPageReady(p.Timeout(5 * time.Second))
			require.Equal(t, "ready", p.MustEval(`() => window.__INITIAL_STATE__.note.noteDetailMap.fixture.note.title`).Str())
		}},
		{"search", `window.__INITIAL_STATE__={search:{feeds:{value:[{id:"fixture"}]}}}`, func(t *testing.T, url string) {
			p := b.NewPage()
			defer p.Close()
			p.MustNavigate(url)
			waitSearchPageReady(p.Timeout(5 * time.Second))
			require.Equal(t, "fixture", readFeedIDs(p))
		}},
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
				fmt.Fprintf(w, `<script>%s</script><div class="note-scroller"></div><img src="/slow.png">`, c.script)
			}))
			defer s.Close()
			started := time.Now()
			c.wait(t, s.URL)
			require.Less(t, time.Since(started), 2*time.Second, "ready data must not wait for a 3-second image")
		})
	}
}

func TestReadinessWaitsForDataAndRecognizesErrors(t *testing.T) {
	b := browser.NewBrowser(true)
	defer b.Close()
	t.Run("detail placeholder is not ready", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<div class="note-scroller"></div><script>
		window.__INITIAL_STATE__={note:{noteDetailMap:{fixture:{comments:{list:[]}}}}};
		setTimeout(() => {window.__INITIAL_STATE__.note.noteDetailMap.fixture.note={noteId:'fixture', title:'loaded'}}, 250);
		</script>`)
		waitFeedPageReady(p.Timeout(2 * time.Second))
		require.Equal(t, "loaded", p.MustEval(`() => window.__INITIAL_STATE__.note.noteDetailMap.fixture.note.title`).Str())
	})
	t.Run("initial empty search is not ready", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<script>
		window.__INITIAL_STATE__={search:{feeds:{_value:[]}}};
		setTimeout(() => {window.__INITIAL_STATE__.search.feeds._value=[{id:'loaded'}]}, 250);
		</script>`)
		waitSearchPageReady(p.Timeout(2 * time.Second))
		require.Equal(t, "loaded", readFeedIDs(p))
	})
	t.Run("error page still reported", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<div class="error-wrapper">该笔记已被删除</div>`)
		waitFeedPageReady(p.Timeout(2 * time.Second))
		require.ErrorContains(t, checkPageAccessible(p), "该笔记已被删除")
	})
	t.Run("healthy page has no error polling delay", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<div class="note-scroller">ready</div>`)
		started := time.Now()
		require.NoError(t, checkPageAccessible(p))
		require.Less(t, time.Since(started), time.Second)
	})
	t.Run("wait for initial comments", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<script>
		window.__INITIAL_STATE__={note:{noteDetailMap:{fixture:{note:{noteId:'fixture',interactInfo:{commentCount:'1'}},comments:{list:[]}}}}};
		setTimeout(() => {window.__INITIAL_STATE__.note.noteDetailMap.fixture.comments.list=[{id:'comment'}]}, 250);
		</script>`)
		waitFeedPageReady(p.Timeout(2 * time.Second))
		require.Equal(t, 1, p.MustEval(`() => window.__INITIAL_STATE__.note.noteDetailMap.fixture.comments.list.length`).Int())
	})
	t.Run("empty search has no extra eight second wait", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<script>window.__INITIAL_STATE__={search:{feeds:{value:[]}}}</script>`)
		started := time.Now()
		waitSearchPageReady(p.Timeout(12 * time.Second))
		require.Less(t, time.Since(started), 3*time.Second)
	})
	t.Run("wait for hydrated error text", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<div class="error-wrapper"></div><script>
		setTimeout(() => {document.querySelector('.error-wrapper').textContent='该笔记已被删除'},250);
		</script>`)
		waitFeedPageReady(p.Timeout(2 * time.Second))
		require.ErrorContains(t, checkPageAccessible(p), "该笔记已被删除")
	})
	t.Run("search cancellation stops both waits", func(t *testing.T) {
		p := b.NewPage()
		defer p.Close()
		p.MustSetDocumentContent(`<div>no application state</div>`)
		started := time.Now()
		require.Panics(t, func() { waitSearchPageReady(p.Timeout(100 * time.Millisecond)) })
		require.Less(t, time.Since(started), time.Second)
	})
}
