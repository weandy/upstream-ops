package connector

import "context"

const DefaultAPIKeyPageSize = 100

// MaxAPIKeyListPages 翻页上限。上游 pages 异常时避免一直请求。
const MaxAPIKeyListPages = 100

// WalkAPIKeyPages 按页拉取密钥。visit 返回 true 时停止。
// 某一页条数小于页大小，或页码已经到达 pages，也停止。pages 为 0 视为只有当前这一页。
func WalkAPIKeyPages(
	ctx context.Context,
	pageSize int,
	maxPages int,
	list func(context.Context, APIKeyQuery) (*APIKeyPage, error),
	query APIKeyQuery,
	visit func(APIKey) bool,
) error {
	if pageSize <= 0 {
		pageSize = DefaultAPIKeyPageSize
	}
	if maxPages <= 0 {
		maxPages = MaxAPIKeyListPages
	}
	query.PageSize = pageSize
	for page := 1; page <= maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		query.Page = page
		got, err := list(ctx, query)
		if err != nil {
			return err
		}
		if got == nil {
			return nil
		}
		for _, item := range got.Items {
			if visit != nil && visit(item) {
				return nil
			}
		}
		if got.Pages <= 0 || page >= got.Pages || len(got.Items) < pageSize {
			return nil
		}
	}
	return nil
}

// FindAPIKeyPage 在分页结果里找第一把匹配的密钥。
func FindAPIKeyPage(
	ctx context.Context,
	pageSize int,
	maxPages int,
	list func(context.Context, APIKeyQuery) (*APIKeyPage, error),
	query APIKeyQuery,
	match func(APIKey) bool,
) (*APIKey, error) {
	var found *APIKey
	err := WalkAPIKeyPages(ctx, pageSize, maxPages, list, query, func(item APIKey) bool {
		if match != nil && match(item) {
			copy := item
			found = &copy
			return true
		}
		return false
	})
	return found, err
}
