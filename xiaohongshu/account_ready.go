package xiaohongshu

import (
	"context"
	"time"

	"github.com/go-rod/rod"
)

func waitLoginPageReady(page *rod.Page) error {
	// 已认证账号直接返回；游客走页面稳定检查，避免初始 guest 状态误报。
	ctx, cancel := context.WithCancel(page.GetContext())
	defer cancel()
	p := page.Context(ctx)
	ready := `(allowGuest) => {
		const unwrap = x => x?.value !== undefined ? x.value : (x?._value !== undefined ? x._value : x);
		const loggedIn=unwrap(window.__INITIAL_STATE__?.user?.loggedIn);
		if (loggedIn === true && document.querySelector('.main-container .user .link-wrapper .channel')) return true;
		const user = window.__INITIAL_STATE__?.user?.userInfo;
		const info = unwrap(user);
		const login = document.querySelector('.login-container .qrcode-img, #login-btn');
		return allowGuest && info?.guest === true && !!login && !!login.getClientRects().length;
	}`
	done := make(chan error, 2)
	go func() { done <- p.Wait(rod.Eval(ready, false)) }()
	go func() {
		err := p.WaitLoad()
		if err == nil {
			err = p.WaitStable(time.Second)
		}
		if err == nil {
			err = p.Wait(rod.Eval(ready, true))
		}
		done <- err
	}()
	for i := 0; i < 2; i++ {
		err := <-done
		if err == nil {
			return nil
		}
		if i == 1 {
			return err
		}
	}
	return ctx.Err()
}

func waitUnreadPageReady(page *rod.Page) error {
	// 零条未读也是已加载结果，但缺失字段不能当作零。
	return page.Wait(rod.Eval(`() => {
		const n=window.__INITIAL_STATE__?.notification;
		const unwrap = x => x?.value !== undefined ? x.value : (x?._value !== undefined ? x._value : x);
		const count = n?.notificationCount;
		return unwrap(n?.isUnreadCountInitialized) === true && !!count && ['mentions','likes','connections','unreadCount'].every(k => Number.isFinite(count[k]));
	}`))
}

func waitProfilePageReady(page *rod.Page, tab ProfileTab) error {
	if tab == "" {
		tab = TabNotes
	}
	return page.Wait(rod.Eval(`(wanted) => {
		const u = window.__INITIAL_STATE__?.user;
		const unwrap = x => x?.value !== undefined ? x.value : (x?._value !== undefined ? x._value : x);
		const profile = unwrap(u?.userPageData);
		const notes = unwrap(u?.notes);
		const active = unwrap(u?.activeTab);
		const statuses=unwrap(u?.userNoteFetchingStatus);
		const fetching=unwrap(u?.isFetchingNotes);
		return !!profile?.basicInfo && Array.isArray(profile.interactions) && Array.isArray(notes) &&
			!!active && active.query === wanted && Number.isInteger(active.index) && Array.isArray(notes[active.index]) &&
			unwrap(u?.userFetchingStatus) === 'resolved' && statuses?.[active.index] === 'resolved' && fetching?.[active.index] === false;
	}`, string(tab)))
}
