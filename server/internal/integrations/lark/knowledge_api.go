package lark

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// Separate from APIClient so existing private chat transports and their mocks
// need no new authority. Only the opted-in source route uses these methods.
type knowledgeAPI interface {
	GetMessage(context.Context, InstallationCredentials, string) ([]LarkMessage, error)
	DownloadMessageResourceStream(context.Context, InstallationCredentials, DownloadResourceParams) (DownloadedResourceStream, error)
	SendTextMessage(context.Context, SendTextParams) (string, error)
	KnowledgeHistory(context.Context, InstallationCredentials, knowledgeHistoryParams) (knowledgeHistoryPage, error)
	KnowledgeMember(context.Context, InstallationCredentials, ChatID, OpenID) (bool, error)
}
type knowledgeHistoryParams struct {
	ChatID              ChatID
	ThreadID, PageToken string
	StartTime, EndTime  int64
}
type knowledgeHistoryPage struct {
	Messages []LarkMessage
	Next     string
	HasMore  bool
}

func (c *httpAPIClient) KnowledgeHistory(ctx context.Context, creds InstallationCredentials, p knowledgeHistoryParams) (knowledgeHistoryPage, error) {
	if p.ChatID == "" {
		return knowledgeHistoryPage{}, errors.New("knowledge history requires chat")
	}
	q := url.Values{"container_id_type": {"chat"}, "container_id": {string(p.ChatID)}, "page_size": {"50"}, "sort_type": {"ByCreateTimeAsc"}, "user_id_type": {"open_id"}}
	if p.ThreadID != "" {
		q.Set("container_id_type", "thread")
		q.Set("container_id", p.ThreadID)
	} else {
		if p.StartTime > 0 {
			q.Set("start_time", strconv.FormatInt(p.StartTime, 10))
		}
		if p.EndTime > 0 {
			q.Set("end_time", strconv.FormatInt(p.EndTime, 10))
		}
	}
	if p.PageToken != "" {
		q.Set("page_token", p.PageToken)
	}
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Items     []larkRESTMessageItem `json:"items"`
			HasMore   bool                  `json:"has_more"`
			PageToken string                `json:"page_token"`
		} `json:"data"`
	}
	if err := c.doAuthedJSON(ctx, creds, http.MethodGet, "/open-apis/im/v1/messages?"+q.Encode(), nil, &out); err != nil {
		return knowledgeHistoryPage{}, err
	}
	if out.Code != 0 {
		return knowledgeHistoryPage{}, &APIError{Op: "knowledge history", Code: out.Code, Msg: out.Msg}
	}
	page := knowledgeHistoryPage{Next: out.Data.PageToken, HasMore: out.Data.HasMore}
	for _, m := range out.Data.Items {
		page.Messages = append(page.Messages, m.normalize())
	}
	if page.HasMore && (page.Next == "" || page.Next == p.PageToken) {
		return knowledgeHistoryPage{}, errors.New("knowledge history pagination incomplete")
	}
	return page, nil
}

func (c *httpAPIClient) KnowledgeMember(ctx context.Context, creds InstallationCredentials, chat ChatID, user OpenID) (bool, error) {
	if chat == "" || user == "" {
		return false, nil
	}
	token := ""
	for page := 0; page < 100; page++ {
		q := url.Values{"member_id_type": {"open_id"}, "page_size": {"100"}}
		if token != "" {
			q.Set("page_token", token)
		}
		var out struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Items []struct {
					ID string `json:"member_id"`
				} `json:"items"`
				HasMore bool   `json:"has_more"`
				Token   string `json:"page_token"`
			} `json:"data"`
		}
		if err := c.doAuthedJSON(ctx, creds, http.MethodGet, "/open-apis/im/v1/chats/"+url.PathEscape(string(chat))+"/members?"+q.Encode(), nil, &out); err != nil {
			return false, err
		}
		if out.Code != 0 {
			return false, &APIError{Op: "knowledge membership", Code: out.Code, Msg: out.Msg}
		}
		for _, m := range out.Data.Items {
			if m.ID == string(user) {
				return true, nil
			}
		}
		if !out.Data.HasMore {
			return false, nil
		}
		if out.Data.Token == "" || out.Data.Token == token {
			return false, errors.New("knowledge membership pagination incomplete")
		}
		token = out.Data.Token
	}
	return false, errors.New("knowledge membership page limit reached")
}
