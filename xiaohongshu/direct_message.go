package xiaohongshu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// DMContent 保留未知类型及卡片的结构化内容，不下载附件。
type DMContent struct {
	Type int             `json:"content_type"`
	Text string          `json:"text,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

type DMConversation struct {
	UserID      string    `json:"user_id"`
	Nickname    string    `json:"nickname"`
	Avatar      string    `json:"avatar,omitempty"`
	LastTime    int64     `json:"last_time"`
	LastContent DMContent `json:"last_content"`
	UnreadCount int       `json:"unread_count"`
}

type DMConversations struct {
	Conversations []DMConversation `json:"conversations"`
	Page          int              `json:"page"`
	HasMore       bool             `json:"has_more"`
}

type DMMessage struct {
	ID         string    `json:"id"`
	SenderID   string    `json:"sender_id"`
	ReceiverID string    `json:"receiver_id"`
	CreatedAt  int64     `json:"created_at"`
	StoreID    int64     `json:"store_id,string"`
	Revoked    bool      `json:"revoked"`
	Content    DMContent `json:"content"`
}

type DMMessages struct {
	UserID     string      `json:"user_id"`
	Messages   []DMMessage `json:"messages"`
	NextLastID int64       `json:"next_last_id,string"`
	HasMore    bool        `json:"has_more"`
}

type rawDMConversation struct {
	UserID      string          `json:"chat_user_id"`
	LastTime    int64           `json:"last_msg_time"`
	LastContent json.RawMessage `json:"last_msg_content"`
	Info        struct {
		Nickname string `json:"nickname"`
		Avatar   string `json:"avatar"`
	} `json:"info"`
}

// 消息 ID 可能是数字或字符串，不能经过浮点数中转。
type dmID string

func (id *dmID) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, (*string)(id))
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*id = dmID(number.String())
	return nil
}

func decodeDMResponse(body []byte) (json.RawMessage, error) {
	var envelope struct {
		Code    *int            `json:"code"`
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("私信接口返回了无效 JSON")
	}
	if envelope.Code == nil || envelope.Success == nil {
		return nil, fmt.Errorf("私信接口响应结构已变化")
	}
	if *envelope.Code != 0 || !*envelope.Success {
		return nil, fmt.Errorf("私信接口失败（code=%d），请检查登录态或账号权限", *envelope.Code)
	}
	if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
		return nil, fmt.Errorf("私信接口缺少数据")
	}
	return envelope.Data, nil
}

func parseDMContent(raw json.RawMessage) DMContent {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return DMContent{}
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if !json.Valid([]byte(text)) {
			return DMContent{Text: text}
		}
		raw = json.RawMessage(text)
	}
	var envelope struct {
		Type    int             `json:"content_type"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Content) == 0 {
		return DMContent{Data: raw}
	}
	content := envelope.Content
	if json.Unmarshal(content, &text) == nil {
		if envelope.Type == 1 {
			return DMContent{Type: envelope.Type, Text: text}
		}
		if json.Valid([]byte(text)) {
			content = json.RawMessage(text)
		}
	}
	return DMContent{Type: envelope.Type, Data: content}
}

func dmSeconds(value int64) int64 {
	for value > 10_000_000_000 {
		value /= 1000
	}
	return value
}

func parseDMConversations(data, unreadData []byte, page, limit int) (*DMConversations, error) {
	var payload struct {
		Chats *[]rawDMConversation `json:"chats"`
		Total *int                 `json:"total"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.Chats == nil {
		return nil, fmt.Errorf("私信会话列表结构已变化")
	}
	var unread struct {
		Users map[string]int `json:"user_chat_unread_counts"`
	}
	if err := json.Unmarshal(unreadData, &unread); err != nil || unread.Users == nil {
		return nil, fmt.Errorf("私信未读数结构已变化")
	}
	result := &DMConversations{Page: page, Conversations: make([]DMConversation, 0, len(*payload.Chats))}
	for _, item := range *payload.Chats {
		if item.UserID == "" {
			return nil, fmt.Errorf("私信会话缺少用户 ID")
		}
		result.Conversations = append(result.Conversations, DMConversation{
			UserID: item.UserID, Nickname: item.Info.Nickname, Avatar: item.Info.Avatar,
			LastTime: dmSeconds(item.LastTime), LastContent: parseDMContent(item.LastContent), UnreadCount: unread.Users[item.UserID],
		})
	}
	result.HasMore = len(*payload.Chats) >= limit
	if payload.Total != nil {
		result.HasMore = page < (*payload.Total-1)/limit && *payload.Total > 0
	}
	return result, nil
}

func parseDMHistory(data []byte, userID string, limit int) (*DMMessages, error) {
	var payload struct {
		Messages *[]struct {
			ID         dmID            `json:"id"`
			UUID       string          `json:"uuid"`
			StoreID    int64           `json:"store_id"`
			SenderID   string          `json:"sender_id"`
			ReceiverID string          `json:"receiver_id"`
			CreatedAt  int64           `json:"created_at"`
			Revoked    bool            `json:"revoked"`
			Content    json.RawMessage `json:"content"`
		} `json:"out_message_list"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.Messages == nil {
		return nil, fmt.Errorf("私信历史消息结构已变化")
	}
	result := &DMMessages{UserID: userID, Messages: make([]DMMessage, 0, len(*payload.Messages))}
	for _, item := range *payload.Messages {
		id := string(item.ID)
		if id == "" {
			id = item.UUID
		}
		if id == "" && item.StoreID > 0 {
			id = strconv.FormatInt(item.StoreID, 10)
		}
		message := DMMessage{ID: id, SenderID: item.SenderID, ReceiverID: item.ReceiverID, StoreID: item.StoreID, CreatedAt: dmSeconds(item.CreatedAt), Revoked: item.Revoked}
		if !item.Revoked {
			message.Content = parseDMContent(item.Content)
		}
		result.Messages = append(result.Messages, message)
		if item.StoreID > 0 && (result.NextLastID == 0 || item.StoreID < result.NextLastID) {
			result.NextLastID = item.StoreID
		}
	}
	sort.SliceStable(result.Messages, func(i, j int) bool {
		if result.Messages[i].CreatedAt == result.Messages[j].CreatedAt {
			return result.Messages[i].StoreID < result.Messages[j].StoreID
		}
		return result.Messages[i].CreatedAt < result.Messages[j].CreatedAt
	})
	// 满页只表示可能还有更早消息，下一页允许为空。
	result.HasMore = len(*payload.Messages) >= limit && result.NextLastID > 1
	return result, nil
}

func dmLimit(limit int) (int, error) {
	if limit == 0 {
		return 20, nil
	}
	if limit < 1 || limit > 100 {
		return 0, fmt.Errorf("limit 必须在 1 到 100 之间")
	}
	return limit, nil
}

func validateDMUserID(id string) error {
	if len(id) == 0 || len(id) > 128 {
		return fmt.Errorf("user_id 不能为空且不能超过 128 字符")
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return fmt.Errorf("user_id 包含无效字符")
		}
	}
	return nil
}

func ValidateDMListOptions(page, limit int) error {
	if page < 0 {
		return fmt.Errorf("page 不能为负数")
	}
	_, err := dmLimit(limit)
	return err
}

func ValidateDMMessageOptions(userID string, limit int, lastID int64) error {
	if err := validateDMUserID(userID); err != nil {
		return err
	}
	if lastID < 0 {
		return fmt.Errorf("last_id 不能为负数")
	}
	_, err := dmLimit(limit)
	return err
}

type DirectMessageAction struct{ page *rod.Page }

func NewDirectMessageAction(page *rod.Page) *DirectMessageAction {
	return &DirectMessageAction{page: page}
}

func (a *DirectMessageAction) prepare(ctx context.Context) (*rod.Page, func(), error) {
	page := a.page.Context(ctx).Timeout(60 * time.Second)
	// 首页即可建立登录环境，不进入聊天页或具体会话，避免触发已读。
	router := page.HijackRequests()
	err := router.Add("*", "", func(h *rod.Hijack) {
		if strings.Contains(h.Request.URL().Path, "/api/im/") && h.Request.Method() != "GET" && h.Request.Method() != "OPTIONS" {
			h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}
		h.ContinueRequest(&proto.FetchContinueRequest{})
	})
	if err != nil {
		return nil, nil, fmt.Errorf("保护私信只读请求失败: %w", err)
	}
	go router.Run()
	cleanup := func() { _ = router.Stop() }
	loggedIn, err := NewLogin(page).CheckLoginStatus(ctx)
	if err != nil || !loggedIn {
		cleanup()
		return nil, nil, fmt.Errorf("私信读取需要有效登录态，请先检查登录状态")
	}
	return page, cleanup, nil
}

// 只在页面中执行固定 GET；响应以文本回到 Go，保留 64 位 ID 的精度。
func readDMJSON(page *rod.Page, path string, query url.Values) (json.RawMessage, error) {
	target := "https://edith.xiaohongshu.com" + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	response, err := page.Eval(`async (url) => {
		const response = await fetch(url, {method:'GET', credentials:'include', cache:'no-store', redirect:'error'});
		return {status:response.status, body:await response.text()};
	}`, target)
	if err != nil {
		return nil, fmt.Errorf("私信读取请求失败")
	}
	var result struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := response.Value.Unmarshal(&result); err != nil {
		return nil, fmt.Errorf("读取私信响应失败: %w", err)
	}
	if result.Status != 200 {
		return nil, fmt.Errorf("私信接口 HTTP %d，请检查登录态或账号权限", result.Status)
	}
	return decodeDMResponse([]byte(result.Body))
}

func (a *DirectMessageAction) List(ctx context.Context, pageNumber, limit int) (*DMConversations, error) {
	limit, err := dmLimit(limit)
	if err != nil {
		return nil, err
	}
	if pageNumber < 0 {
		return nil, fmt.Errorf("page 不能为负数")
	}
	page, cleanup, err := a.prepare(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	data, err := readDMJSON(page, "/api/im/web/v3/chats", url.Values{"page": {strconv.Itoa(pageNumber)}, "limit": {strconv.Itoa(limit)}, "complete": {"true"}, "source": {"pc"}})
	if err != nil {
		return nil, err
	}
	unread, err := readDMJSON(page, "/api/im/web/chat/get_unread", nil)
	if err != nil {
		return nil, err
	}
	return parseDMConversations(data, unread, pageNumber, limit)
}

func (a *DirectMessageAction) Messages(ctx context.Context, userID string, limit int, lastID int64) (*DMMessages, error) {
	if err := validateDMUserID(userID); err != nil {
		return nil, err
	}
	limit, err := dmLimit(limit)
	if err != nil {
		return nil, err
	}
	if lastID < 0 {
		return nil, fmt.Errorf("last_id 不能为负数")
	}
	if lastID == 1 {
		return &DMMessages{UserID: userID, Messages: []DMMessage{}}, nil
	}
	page, cleanup, err := a.prepare(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	data, err := readDMJSON(page, "/api/im/web/messages/history", dmHistoryQuery(userID, limit, lastID))
	if err != nil {
		return nil, err
	}
	return parseDMHistory(data, userID, limit)
}

func dmHistoryQuery(userID string, limit int, lastID int64) url.Values {
	// 上游包含边界，0 特指最新页；对外游标表示严格更早的消息。
	if lastID > 1 {
		lastID--
	}
	return url.Values{"chat_user_id": {userID}, "limit": {strconv.Itoa(limit)}, "last_id": {strconv.FormatInt(lastID, 10)}, "start_id": {"0"}}
}
