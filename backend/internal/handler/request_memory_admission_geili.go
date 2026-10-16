package handler

import "github.com/gin-gonic/gin"

// MarkOpsRequestMemoryCapacityGeili records local ingress overload as routing
// capacity, before selecting or charging any supplier account.
func MarkOpsRequestMemoryCapacityGeili(c *gin.Context) { markOpsRoutingCapacityLimited(c) }
