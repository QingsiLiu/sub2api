package repository

import (
	"fmt"
	"strings"
)

// geili hook: account selections are ORed within this parameterized predicate;
// other dimensions (user, date, group, etc.) remain AND filters.
func appendUsageAccountIDsWhereGeili(conditions []string, args []any, ids []int64, column string) ([]string, []any) {
	if len(ids) == 0 {
		return conditions, args
	}
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", len(args)+1)
		args = append(args, id)
	}
	return append(conditions, column+" IN ("+strings.Join(placeholders, ", ")+")"), args
}

func appendUsageAccountIDsQueryGeili(query string, args []any, ids []int64, column string) (string, []any) {
	conditions, args := appendUsageAccountIDsWhereGeili(nil, args, ids, column)
	if len(conditions) > 0 {
		query += " AND " + conditions[0]
	}
	return query, args
}
