package xiaohongshu

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type NavigateAction struct {
	page *rod.Page
}

func NewNavigate(page *rod.Page) *NavigateAction {
	return &NavigateAction{page: page}
}

func (n *NavigateAction) ToExplorePage(ctx context.Context) error {
	page := n.page.Context(ctx).Timeout(60 * time.Second) // 加超时保护，避免 MustNavigate/MustWaitStable 无限挂

	started := time.Now()
	if err := page.Navigate("https://www.xiaohongshu.com/explore"); err != nil {
		return err
	}
	logPhase("profile_explore_navigate", started)
	started = time.Now()
	err := waitLoginPageReady(page)
	logPhase("profile_account_ready", started)
	return err
}

func (n *NavigateAction) ToProfilePage(ctx context.Context, tabs ...ProfileTab) error {
	page := n.page.Context(ctx).Timeout(60 * time.Second) // 加超时保护，避免 MustNavigate/MustWaitStable 无限挂

	// 先等待首页账号状态。
	if err := n.ToExplorePage(ctx); err != nil {
		return err
	}

	// 读取本人主页地址直接导航，避免点击后的异步路由重置状态。
	link, err := page.Element(`.main-container .user a.link-wrapper[href*="/user/profile/"]`)
	if err != nil {
		return err
	}
	href, err := link.Attribute("href")
	if err != nil {
		return err
	}
	if href == nil {
		return fmt.Errorf("本人主页链接为空")
	}
	base, _ := url.Parse("https://www.xiaohongshu.com")
	rel, err := url.Parse(*href)
	if err != nil {
		return err
	}
	target := base.ResolveReference(rel)
	if target.Host != base.Host || target.Scheme != "https" {
		return fmt.Errorf("本人主页链接域名不符")
	}
	if len(tabs) > 0 && tabs[0] != TabNotes && tabs[0] != "" {
		q := target.Query()
		q.Set("tab", string(tabs[0]))
		q.Set("subTab", "note")
		target.RawQuery = q.Encode()
	}
	if err := navigateDocument(page, target.String()); err != nil {
		return err
	}

	// 调用方按目标主页数据等待，避免重复等待页面资源。
	return nil
}

// 等新文档提交后的 DOMContentLoaded，不等图片等资源的 load 事件。
func navigateDocument(page *rod.Page, target string) error {
	ctx, cancel := context.WithCancel(page.GetContext())
	defer cancel()
	p := page.Context(ctx)
	tree, err := (proto.PageGetFrameTree{}).Call(p)
	if err != nil {
		return err
	}
	previous := tree.FrameTree.Frame
	if err := (proto.PageSetLifecycleEventsEnabled{Enabled: true}).Call(p); err != nil {
		return err
	}
	defer (proto.PageSetLifecycleEventsEnabled{Enabled: false}).Call(page)
	wait := p.EachEvent(func(e *proto.PageLifecycleEvent) bool {
		return e.Name == proto.PageLifecycleEventNameDOMContentLoaded && e.FrameID == previous.ID && e.LoaderID != "" && e.LoaderID != previous.LoaderID
	})
	if err := p.Navigate(target); err != nil {
		return err
	}
	wait()
	return ctx.Err()
}
