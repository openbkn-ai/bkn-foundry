package driveradapters

import "github.com/gin-gonic/gin"

// observabilityRoute returns the registered route, never a raw URL or object ID.
func observabilityRoute(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return "<unmatched>"
}
