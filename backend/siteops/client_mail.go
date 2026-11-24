package siteops

import (
	"context"
	"encoding/json"
	"net/http"
)

var _ MailAudienceClient = (*Client)(nil)

type mailPrivateRequestKey struct{}

func mailPage[W, T any](raw []byte, key string, project func(W) (T, error)) (MailDirectoryPage[T], error) {
	var page MailDirectoryPage[T]
	data, err := decodeEnvelopeData(raw)
	if err != nil {
		return page, err
	}
	if data[0] != '[' {
		var header struct {
			Page      *int
			PageSize  *int `json:"page_size"`
			Total     *int64
			HasMore   *bool `json:"has_more"`
			Truncated bool
		}
		if json.Unmarshal(data, &header) != nil {
			return page, errInvalidPayload
		}
		page.Page, page.PageSize, page.Total, page.HasMore, page.Truncated = header.Page, header.PageSize, header.Total, header.HasMore, header.Truncated
		data, _, err = containerData(raw, []string{"items", key, "rows", "list"})
		if err != nil {
			return page, err
		}
	}
	var rows []W
	if json.Unmarshal(data, &rows) != nil || rows == nil {
		return page, errInvalidPayload
	}
	page.Truncated = page.Truncated || len(rows) > 100
	for _, w := range rows[:min(len(rows), 100)] {
		item, err := project(w)
		if err != nil {
			return MailDirectoryPage[T]{}, err
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

func mailRequestSpec(ep, path string, page int) requestSpec {
	return requestSpec{endpoint: ep, path: adminPath + path + "?" + pageParams(page, 100).Encode(), method: http.MethodGet, policy: cachePolicyNoCache, lowPriority: true, bypassFlight: true}
}

func (c *Client) FetchMailUsersPage(ctx context.Context, inst TargetEndpoint, page int, project MailProjectAddress) (MailDirectoryPage[MailDirectoryUser], UpstreamFetchMeta, error) {
	var result MailDirectoryPage[MailDirectoryUser]
	var projectErr error
	r := c.fetchShared(ctx, inst, mailRequestSpec(EndpointUsersPage, "/users", page), func(raw []byte) ([]byte, error) {
		var err error
		result, err = mailPage(raw, "users", func(w userWire) (MailDirectoryUser, error) {
			u := MailDirectoryUser{ID: w.ID, Status: enum(w.Status, "active", "disabled", "banned", "inactive"), Role: enum(w.Role, "user", "member", "admin"), BalanceUSD: w.Balance, ConcurrencyLimit: w.Concurrency, CreatedAt: w.CreatedAt}
			if addr, e := NormalizeBareEmail(w.Email); e == nil && u.ID > 0 && u.Status == "active" && (u.Role == "member" || u.Role == "user") && project != nil {
				u.Address, projectErr = project(u, addr)
			}
			return u, projectErr
		})
		return nil, err
	})
	if projectErr != nil {
		return MailDirectoryPage[MailDirectoryUser]{}, r.meta, projectErr
	}
	if r.err != nil {
		return MailDirectoryPage[MailDirectoryUser]{}, r.meta, r.err
	}
	return result, r.meta, nil
}

func (c *Client) FetchMailSubscriptionsPage(ctx context.Context, inst TargetEndpoint, page int) (MailDirectoryPage[MailDirectorySubscription], UpstreamFetchMeta, error) {
	return fetchSpec(c, ctx, inst, mailRequestSpec(EndpointSubscriptionsPage, "/subscriptions", page), func(raw []byte) (MailDirectoryPage[MailDirectorySubscription], error) {
		return mailPage(raw, "subscriptions", func(w subWire) (MailDirectorySubscription, error) {
			w.Group.ID = w.GroupID
			return MailDirectorySubscription{ID: w.ID, UserID: w.UserID, GroupID: w.GroupID, GroupName: cleanGroup(w.Group).Name, Status: enum(w.Status, "active", "expired", "revoked", "cancelled", "inactive"), StartsAt: w.StartsAt, ExpiresAt: w.ExpiresAt, RevokedAt: w.RevokedAt}, nil
		})
	})
}
