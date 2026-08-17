// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

const legacyLanguageHeader = "X-Language"

// Language resolves Accept-Language through the platform middleware. X-Language
// remains a compatibility input only when the preferred header is absent.
func Language() gin.HandlerFunc {
	platformMiddleware := sharedrest.LanguageMiddleware()
	return func(c *gin.Context) {
		acceptLanguage := c.GetHeader(sharedrest.AcceptLanguageHeader)
		if acceptLanguage == "" {
			if legacyLanguage := c.GetHeader(legacyLanguageHeader); legacyLanguage != "" {
				acceptLanguage = legacyLanguage
			}
		}
		if acceptLanguage != "" {
			c.Request.Header.Set(sharedrest.AcceptLanguageHeader, strings.ReplaceAll(acceptLanguage, "_", "-"))
		}
		platformMiddleware(c)
	}
}
