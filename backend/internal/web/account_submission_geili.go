//go:build embed

package web

import "github.com/gin-gonic/gin"

func submissionPageHeaders(c *gin.Context) bool {
	if c.Request.URL.Path != "/submit-key" && c.Request.URL.Path != "/submit-key/" {
		return false
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("ETag", "")
	return true
}
