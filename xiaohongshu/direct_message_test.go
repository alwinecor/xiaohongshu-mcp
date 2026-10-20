package xiaohongshu

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 防止把接口失败或改版误报成空收件箱。
func TestDMResponseValidation(t *testing.T) {
	for _, body := range []string{
		`{"code":-100,"success":false,"msg":"登录失效","data":{}}`,
		`{"code":0,"success":true,"data":null}`,
		`<html>登录</html>`,
		`{"data":{}}`,
	} {
		_, err := decodeDMResponse([]byte(body))
		assert.Error(t, err, body)
	}
	data, err := decodeDMResponse([]byte(`{"code":0,"success":true,"data":{"chats":[]}}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"chats":[]}`, string(data))
}

// 保留卡片、未知消息和大整数，不能将它们当作普通文本丢弃。
func TestDMContentAndHistory(t *testing.T) {
	data := []byte(`{"out_message_list":[
		{"id":9007199254740993,"store_id":9007199254740993,"sender_id":"peer","receiver_id":"self","created_at":1800000000000,"content":"{\"content_type\":1,\"content\":\"你好\"}"},
		{"id":"m2","store_id":12,"created_at":1800000001000,"content":"{\"content_type\":3,\"content\":\"{\\\"title\\\":\\\"一张卡片\\\"}\"}"},
		{"id":"m3","store_id":13,"created_at":1800000002,"content":"{\"content_type\":99,\"content\":{\"unknown\":true}}"}
	]}`)
	result, err := parseDMHistory(data, "peer", 3)
	require.NoError(t, err)
	require.Len(t, result.Messages, 3)
	assert.Equal(t, "9007199254740993", result.Messages[0].ID)
	assert.EqualValues(t, 1800000000, result.Messages[0].CreatedAt)
	assert.Equal(t, "你好", result.Messages[0].Content.Text)
	assert.JSONEq(t, `{"title":"一张卡片"}`, string(result.Messages[1].Content.Data))
	assert.Equal(t, 99, result.Messages[2].Content.Type)
	assert.JSONEq(t, `{"unknown":true}`, string(result.Messages[2].Content.Data))
	assert.EqualValues(t, 12, result.NextLastID)
	assert.True(t, result.HasMore)

	_, err = parseDMHistory([]byte(`{}`), "peer", 20)
	assert.Error(t, err)
	empty, err := parseDMHistory([]byte(`{"out_message_list":[]}`), "peer", 20)
	require.NoError(t, err)
	assert.NotNil(t, empty.Messages)
	assert.False(t, empty.HasMore)
}

// 已撤回消息不能泄漏接口中残存的内容。
func TestDMRevokedContentHidden(t *testing.T) {
	result, err := parseDMHistory([]byte(`{"out_message_list":[{"id":"m1","revoked":true,"content":"{\"content_type\":1,\"content\":\"已撤回的秘密\"}"}]}`), "peer", 20)
	require.NoError(t, err)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "已撤回的秘密")
	assert.True(t, result.Messages[0].Revoked)
}

// 未读字段按实测结构读取，空收件箱也应返回数组。
func TestDMConversations(t *testing.T) {
	data := []byte(`{"page":0,"total":1,"chats":[{"chat_user_id":"peer","user_id":"self","last_msg_time":1800000000000,"last_msg_content":"{\"content_type\":1,\"content\":\"预览\"}","info":{"nickname":"甲","avatar":"https://example.test/a.png"}}]}`)
	result, err := parseDMConversations(data, []byte(`{"user_chat_unread_counts":{"peer":2},"group_chat_unread_counts":{}}`), 0, 20)
	require.NoError(t, err)
	require.Len(t, result.Conversations, 1)
	assert.Equal(t, "peer", result.Conversations[0].UserID)
	assert.Equal(t, "甲", result.Conversations[0].Nickname)
	assert.Equal(t, 2, result.Conversations[0].UnreadCount)
	assert.Equal(t, "预览", result.Conversations[0].LastContent.Text)
	assert.False(t, result.HasMore)
	_, err = parseDMConversations([]byte(`{}`), []byte(`{}`), 0, 20)
	assert.Error(t, err)
}

func TestDMOptionsRejectInvalidInput(t *testing.T) {
	for _, limit := range []int{-1, 101} {
		_, err := dmLimit(limit)
		assert.Error(t, err)
	}
	limit, err := dmLimit(0)
	require.NoError(t, err)
	assert.Equal(t, 20, limit)
	for _, id := range []string{"", "../other", "peer?x=1", "a/b", "  "} {
		assert.Error(t, validateDMUserID(id))
	}
	assert.NoError(t, validateDMUserID("6493a87e00000000110034df"))
}

// 游标经过 JSON 客户端时也不能损失 64 位精度。
func TestDMCursorJSONPrecision(t *testing.T) {
	result, err := parseDMHistory([]byte(`{"out_message_list":[{"id":"m1","store_id":9007199254740993,"content":"文本"}]}`), "peer", 1)
	require.NoError(t, err)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"next_last_id":"9007199254740993"`)
	assert.Contains(t, string(encoded), `"store_id":"9007199254740993"`)
}

// store_id=1 已到历史起点，不能把0游标再当成“最新消息”循环读取。
func TestDMHistoryStopsAtFirstStore(t *testing.T) {
	result, err := parseDMHistory([]byte(`{"out_message_list":[{"id":"first","store_id":1,"content":"最早消息"}]}`), "peer", 1)
	require.NoError(t, err)
	assert.False(t, result.HasMore)
	page, err := NewDirectMessageAction(nil).Messages(context.Background(), "peer", 1, 1)
	require.NoError(t, err)
	assert.Empty(t, page.Messages)
	assert.False(t, page.HasMore)
}

func TestDMHistoryRequestExcludesBoundary(t *testing.T) {
	assert.Equal(t, "0", dmHistoryQuery("peer", 20, 0).Get("last_id"))
	assert.Equal(t, "16", dmHistoryQuery("peer", 20, 17).Get("last_id"))
	assert.Equal(t, "9007199254740992", dmHistoryQuery("peer", 20, 9007199254740993).Get("last_id"))
}
