package xiaohongshu

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-rod/rod"
)

// 等默认分区初始化，避免过早切换后被页面重置；不等其列表请求。
func waitNotificationPageBoot(page *rod.Page) error {
	return page.Timeout(15 * time.Second).Wait(rod.Eval(`()=>{
		const n=window.__INITIAL_STATE__?.notification;
		const unwrap=x=>x?.value!==undefined?x.value:(x?._value!==undefined?x._value:x);
		return unwrap(n?.isUnreadCountInitialized)===true && unwrap(n?.activeTabKey)===0;
	}`))
}

// 首次请求产生游标后才算就绪；翻页还需观察列表或游标变化。
func waitNotificationTabReady(page *rod.Page, tab NotificationTab, previous *notificationPayload) error {
	index := -1
	switch tab {
	case TabMentions:
		index = 0
	case TabLikes:
		index = 1
	case TabConnections:
		index = 2
	default:
		return fmt.Errorf("未知通知分区 %q", tab)
	}
	cursor, count := "", -1
	if previous != nil {
		cursor, count = previous.Cursor, len(previous.MessageList)
	}
	return page.Timeout(15 * time.Second).Wait(rod.Eval(`(tab,index,previousCursor,previousCount) => {
		const unwrap=x=>x?.value!==undefined?x.value:(x?._value!==undefined?x._value:x);
		const n=window.__INITIAL_STATE__?.notification;
		const p=n?.notificationMap?.[tab];
		if(unwrap(n?.activeTabKey)!==index || unwrap(n?.isFetching)!==false ||
			!Array.isArray(p?.messageList) || typeof p.hasMore!=='boolean' ||
			typeof p.cursor!=='string' || p.cursor==='') return false;
		return previousCount<0 || p.cursor!==previousCursor || p.messageList.length!==previousCount || p.hasMore===false;
	}`, string(tab), index, cursor, count))
}

// 尚未滚到触发点时继续滚动；请求开始后再等完整响应。
func waitNotificationScroll(page *rod.Page, tab NotificationTab, previous *notificationPayload) error {
	probe := page.Timeout(500 * time.Millisecond)
	err := waitNotificationTabReady(probe, tab, previous)
	probe.CancelTimeout()
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if err := page.GetContext().Err(); err != nil {
		return err
	}
	fetching, err := page.Eval(`() => {
		const v=window.__INITIAL_STATE__?.notification?.isFetching;
		return (v?.value!==undefined?v.value:(v?._value!==undefined?v._value:v))===true;
	}`)
	if err != nil {
		return err
	}
	if fetching.Value.Bool() {
		return waitNotificationTabReady(page, tab, previous)
	}
	return nil
}
