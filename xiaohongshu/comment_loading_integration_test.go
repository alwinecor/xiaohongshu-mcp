//go:build integration

package xiaohongshu

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/humanize"
)

// 标志不存在不应等待；隐藏标志和残留空提示不能使读取提前结束。
func TestCommentLoadingMarkerChecks(t *testing.T) {
	b := browser.NewBrowser(true)
	defer b.Close()
	cases := []struct {
		name, html string
		empty, end bool
	}{
		{"普通评论", `<div class="parent-comment">first</div>`, false, false},
		{"没有评论", `<div class="no-comments-text">这是一片荒地</div>`, true, false},
		{"已经到底", `<div class="end-container">THE END</div>`, false, true},
		{"隐藏标志", `<div class="no-comments-text" hidden>这是一片荒地</div><div class="end-container" hidden>THE END</div>`, false, false},
		{"空提示残留", `<div class="no-comments-text">这是一片荒地</div><div class="parent-comment">first</div>`, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := b.NewPage()
			defer p.Close()
			p.MustSetDocumentContent(c.html)
			started := time.Now()
			require.Equal(t, c.empty, checkNoCommentsArea(p))
			require.Equal(t, c.end, checkEndContainer(p))
			require.Less(t, time.Since(started), 500*time.Millisecond)
		})
	}
}

func TestCommentLoadingDelayedResults(t *testing.T) {
	b := browser.NewBrowser(true)
	defer b.Close()
	for _, kind := range []string{"empty", "end", "pagination", "slow-pagination"} {
		t.Run(kind, func(t *testing.T) {
			p := b.NewPage()
			defer p.Close()
			p.MustSetDocumentContent(commentLoadingFixture + `<script>
			window.__INITIAL_STATE__={note:{noteDetailMap:{fixture:{note:{noteId:'fixture',interactInfo:{commentCount:'20'}},comments:{list:[],hasMore:false}}}}};
			const kind='` + kind + `';
			if(kind.includes('pagination')) document.querySelector('.comments-container').insertAdjacentHTML('afterbegin', '<div class="parent-comment">first</div>');
			let started=false;
			document.addEventListener('wheel',()=>{
				if(started) return; started=true;
				setTimeout(()=>{
					const box=document.querySelector('.comments-container');
					if(kind==='empty') box.insertAdjacentHTML('beforeend','<div class="no-comments-text">这是一片荒地</div>');
					else {
						box.insertAdjacentHTML('beforeend','<div class="parent-comment">loaded</div>');
						if(kind==='end') box.insertAdjacentHTML('beforeend','<div class="end-container">THE END</div>');
					}
				},kind==='slow-pagination'?4800:1300);
			},{passive:true});
			</script>`)
			config := DefaultCommentLoadConfig()
			config.MaxCommentItems = 2
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			started := time.Now()
			require.NoError(t, NewFeedDetailAction(p).loadAllCommentsWithConfig(ctx, p.Context(ctx), config))
			wantCount := map[string]int{"empty": 0, "end": 1, "pagination": 2, "slow-pagination": 2}[kind]
			require.Equal(t, wantCount, getCommentCount(p))
			require.True(t, p.MustEval(`()=>started`).Bool(), "初始 hasMore=false 不能当成加载完成")
			if kind == "slow-pagination" {
				require.GreaterOrEqual(t, time.Since(started), 4800*time.Millisecond)
				require.Less(t, time.Since(started), 9*time.Second)
			} else {
				require.Less(t, time.Since(started), 5*time.Second)
			}
		})
	}
}

// 已到底的页面也必须完成用户要求的子回复展开。
func TestCommentLoadingEndStillExpandsReplies(t *testing.T) {
	testCommentLoadingReplies(t, 2500)
}

func TestCommentLoadingSlowReplies(t *testing.T) {
	testCommentLoadingReplies(t, 6500)
}

func TestCommentLoadingRepliesTimeout(t *testing.T) {
	testCommentLoadingReplies(t, 20000)
}

func testCommentLoadingReplies(t *testing.T, delayMS int) {
	t.Helper()
	humanize.SetProvider(commentTestTiming{})
	defer humanize.SetProvider(humanize.DefaultProvider{})
	b := browser.NewBrowser(true)
	defer b.Close()
	p := b.NewPage()
	defer p.Close()
	p.MustSetDocumentContent(commentLoadingFixture + `<script>
	document.querySelector('.comments-container').insertAdjacentHTML('afterbegin', '<div class="parent-comment"><div class="comment-item">parent</div><button class="show-more" style="width:160px;height:30px">展开 1 条回复</button></div><div class="end-container">THE END</div>');
	document.querySelector('button').onclick=function(){const parent=this.parentElement;this.remove();setTimeout(()=>{parent.insertAdjacentHTML('beforeend','<div class="comment-item reply">child</div>')},` + fmt.Sprint(delayMS) + `)};
	</script>`)
	config := DefaultCommentLoadConfig()
	config.ClickMoreReplies = true
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := NewFeedDetailAction(p).loadAllCommentsWithConfig(ctx, p.Context(ctx), config)
	if delayMS > 10000 {
		require.ErrorIs(t, err, context.DeadlineExceeded, "子回复超时不能按已到底返回成功")
		return
	}
	require.NoError(t, err)
	replies, err := p.Elements(".reply")
	require.NoError(t, err)
	require.Len(t, replies, 1, "加载器返回时子回复应已存在，断言本身不能等待")
}

func TestCommentLoadingCancellation(t *testing.T) {
	b := browser.NewBrowser(true)
	defer b.Close()
	p := b.NewPage()
	defer p.Close()
	p.MustSetDocumentContent(commentLoadingFixture)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	require.ErrorIs(t, NewFeedDetailAction(p).loadAllCommentsWithConfig(ctx, p, DefaultCommentLoadConfig()), context.Canceled)
	require.Less(t, time.Since(started), 500*time.Millisecond)
	t.Run("等待中取消", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		started := time.Now()
		require.ErrorIs(t, waitForCommentProgress(ctx, p, 0), context.DeadlineExceeded)
		require.Less(t, time.Since(started), 500*time.Millisecond)
	})
}

const commentLoadingFixture = `<style>.note-scroller{width:500px;height:500px;overflow:auto}.comments-container{min-height:1800px}.parent-comment{height:70px}</style><div class="note-scroller"><div class="comments-container"></div></div>`

type commentTestTiming struct{}

func (commentTestTiming) Timing() humanize.TimingProfile {
	profile := humanize.DefaultProvider{}.Timing()
	reading := profile[humanize.Reading]
	reading.Min, reading.Max = time.Millisecond, time.Millisecond
	profile[humanize.Reading] = reading
	return profile
}
