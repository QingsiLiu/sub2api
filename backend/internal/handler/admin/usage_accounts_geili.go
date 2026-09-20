package admin

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// geili hook: accept CSV and repeated (including Axios bracketed) account IDs.
// Reject malformed filters rather than silently querying every account.
func parseUsageAccountIDsGeili(c *gin.Context) ([]int64, error) {
	values := append(c.QueryArray("account_ids"), c.QueryArray("account_ids[]")...)
	if len(values) == 0 {
		return nil, nil
	}
	if c.Query("account_id") != "" {
		return nil, errors.New("Use either account_id or account_ids")
	}
	var ids []int64
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil || id <= 0 {
				return nil, errors.New("Invalid account_ids: expected positive integer IDs")
			}
			ids = append(ids, id)
			if len(ids) > 200 {
				return nil, errors.New("At most 200 account_ids are allowed")
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}
