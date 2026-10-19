package xiaohongshu

import (
	"context"
	"time"

	"github.com/go-rod/rod"
	"github.com/sirupsen/logrus"
)

// feedReadyTimeout 等待详情页关键容器的上限，超时不算失败。
const feedReadyTimeout = 8 * time.Second

// 等正文和首批评论或错误提示就绪，不等待无关图片和整页加载。
func waitFeedPageReady(page *rod.Page) {
	if err := page.Timeout(feedReadyTimeout).Wait(rod.Eval(`() => {
		const error = document.querySelector('.access-wrapper, .error-wrapper, .not-found-wrapper, .blocked-wrapper');
		if (error?.textContent?.trim()) return true;
		const map = window.__INITIAL_STATE__?.note?.noteDetailMap;
		return !!map && Object.values(map).some(v => {
			const note = v?.note;
			const comments = v?.comments?.list;
			return !!note && !!(note.noteId || note.note_id) && Array.isArray(comments) &&
				(comments.length > 0 || Number(note.interactInfo?.commentCount || 0) === 0);
		});
	}`)); err != nil {
		logrus.Warnf("详情数据未在 %s 内就绪，继续尝试: %v", feedReadyTimeout, err)
	}
}

func waitSearchPageReady(page *rod.Page) {
	// 数据就绪与原有整页稳定条件竞速，避免空结果额外等待。
	// 初始空数组不能单独作为完成信号。
	ctx, cancel := context.WithCancel(page.GetContext())
	defer cancel()
	p := page.Context(ctx)
	done := make(chan error, 2)
	go func() {
		done <- p.Wait(rod.Eval(`() => {
		const feeds = window.__INITIAL_STATE__?.search?.feeds;
		const data = feeds?.value !== undefined ? feeds.value : feeds?._value;
		return Array.isArray(data) && data.some(v => !!v.id);
	}`))
	}()
	go func() {
		err := p.WaitStable(time.Second)
		if err == nil {
			err = p.Wait(rod.Eval(`() => window.__INITIAL_STATE__ !== undefined`))
		}
		done <- err
	}()
	for i := 0; i < 2; i++ {
		if err := <-done; err == nil {
			return
		} else if i == 1 {
			panic(err)
		}
	}
}
