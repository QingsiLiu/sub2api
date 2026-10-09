package web

import "strings"

func geiliRootTextGatewayAlias(path string) bool {
	return strings.TrimRight(path, "/") == "/chat/completions"
}
