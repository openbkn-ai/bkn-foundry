// Package interfaces define interfaces.
// @file driveradapters.go
// @description: Define driver adapter interface.
package interfaces

//go:generate mockgen -source=driveradapters.go -destination=../mocks/driveradapters.go -package=mocks
import "github.com/gin-gonic/gin"

// HTTPRouterInterface routing public interface.
type HTTPRouterInterface interface {
	RegisterRouter(engine *gin.RouterGroup)
}

// MQHandler MQ processing interface.
type MQHandler interface {
	Subscribe()
}
