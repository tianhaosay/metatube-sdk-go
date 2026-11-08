package route

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metatube-community/metatube-sdk-go/errors"
	"github.com/metatube-community/metatube-sdk-go/route/auth"
)

// RequireToken returns the bearer-token middleware used by the private routes,
// exported for other mounted groups such as the operator console. A nil
// validator disables authentication.
func RequireToken(v auth.Validator) gin.HandlerFunc {
	return authentication(v)
}

func authentication(v auth.Validator) gin.HandlerFunc {
	return func(c *gin.Context) {
		if v != nil /* auth enabled */ {
			header := c.GetHeader("Authorization")
			bearer, token, found := strings.Cut(header, " ")

			hasInvalidHeader := bearer != "Bearer"
			hasInvalidSecret := !found || !v.Valid(token)
			if hasInvalidHeader || hasInvalidSecret {
				abortWithError(c, errors.FromCode(http.StatusUnauthorized))
				return
			}
		}
		c.Next()
	}
}
