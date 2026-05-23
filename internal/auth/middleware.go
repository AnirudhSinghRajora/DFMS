package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	apierrors "github.com/AnirudhSinghRajora/DFMS/pkg/errors"
)

const (
	// ContextKeyUserID is the key used to store the authenticated user ID in gin.Context.
	ContextKeyUserID = "user_id"
	// ContextKeyEmail is the key used to store the authenticated user email in gin.Context.
	ContextKeyEmail = "user_email"
	// ContextKeyRole is the key used to store the authenticated user role in gin.Context.
	ContextKeyRole = "user_role"
)

// JWTAuthMiddleware returns a Gin middleware that validates JWT access tokens.
// It extracts the token from the Authorization header (Bearer scheme),
// validates it, and injects user information into the request context.
func JWTAuthMiddleware(jwtService *JWTService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			apiErr := apierrors.NewUnauthorized(apierrors.CodeAuthTokenMissing, "Authorization header is required")
			c.AbortWithStatusJSON(http.StatusUnauthorized, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Expect "Bearer <token>"
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			apiErr := apierrors.NewUnauthorized(apierrors.CodeAuthTokenInvalid, "Authorization header must use Bearer scheme")
			c.AbortWithStatusJSON(http.StatusUnauthorized, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		tokenStr := parts[1]
		claims, err := jwtService.ValidateAccessToken(tokenStr)
		if err != nil {
			apiErr := apierrors.NewUnauthorized(apierrors.CodeAuthTokenInvalid, "Invalid or expired access token")
			c.AbortWithStatusJSON(http.StatusUnauthorized, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		// Inject user info into context for downstream handlers
		c.Set(ContextKeyUserID, claims.UserID)
		c.Set(ContextKeyEmail, claims.Email)
		c.Set(ContextKeyRole, claims.Role)

		c.Next()
	}
}

// RequireRole returns a Gin middleware that checks if the authenticated user
// has one of the required roles. Must be used after JWTAuthMiddleware.
func RequireRole(roles ...string) gin.HandlerFunc {
	roleSet := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		roleSet[r] = struct{}{}
	}

	return func(c *gin.Context) {
		userRole, exists := c.Get(ContextKeyRole)
		if !exists {
			apiErr := apierrors.NewForbidden("Authentication required")
			c.AbortWithStatusJSON(http.StatusForbidden, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		if _, ok := roleSet[userRole.(string)]; !ok {
			apiErr := apierrors.NewForbidden("Insufficient permissions")
			c.AbortWithStatusJSON(http.StatusForbidden, apiErr.ToResponse(c.GetString("request_id")))
			return
		}

		c.Next()
	}
}

// GetUserID extracts the authenticated user ID from the Gin context.
func GetUserID(c *gin.Context) string {
	id, _ := c.Get(ContextKeyUserID)
	if id == nil {
		return ""
	}
	return id.(string)
}

// GetUserEmail extracts the authenticated user email from the Gin context.
func GetUserEmail(c *gin.Context) string {
	email, _ := c.Get(ContextKeyEmail)
	if email == nil {
		return ""
	}
	return email.(string)
}
