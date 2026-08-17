package middleware

import (
	"oss-gateway/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func Recovery(log *logrus.Entry) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.WithField("error", err).Error("Panic recovered")

				response.InternalError(c, "panic recovered")
				c.Abort()
			}
		}()

		c.Next()
	}
}
