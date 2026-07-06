package dfmsclient

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Tokens is the access/refresh token pair issued by the DFMS API. The JSON tags
// match the API wire format and double as the on-disk storage format.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// TokenStore persists the Tokens for the single server a Client targets.
// The CLI supplies an implementation bound to the active context; the auth
// transport uses it to read the bearer token and to save rotated tokens.
type TokenStore interface {
	// Load returns the stored tokens, or ErrNoCredentials if none are stored.
	Load() (Tokens, error)
	// Save persists the tokens, replacing any existing ones.
	Save(Tokens) error
	// Delete removes the stored tokens. It is a no-op if none exist.
	Delete() error
}

// tokenPair mirrors the server's TokenPair envelope. Only the access and
// refresh tokens are retained; expires_in is informational (the access token
// carries its own exp claim).
type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// authResponse is the shape of register/login/refresh responses, which wrap the
// token pair (and, for register/login, a user object the client does not need).
type authResponse struct {
	Tokens tokenPair `json:"tokens"`
}

func (r authResponse) toTokens() Tokens {
	return Tokens{AccessToken: r.Tokens.AccessToken, RefreshToken: r.Tokens.RefreshToken}
}

// Register creates an account and returns its initial token pair.
func (c *Client) Register(ctx context.Context, email, password, displayName string) (Tokens, error) {
	payload := map[string]string{
		"email":        email,
		"password":     password,
		"display_name": displayName,
	}
	req, err := c.newJSONRequest(ctx, http.MethodPost, pathRegister, payload)
	if err != nil {
		return Tokens{}, err
	}
	var resp authResponse
	if err := c.doJSON(req, &resp); err != nil {
		return Tokens{}, err
	}
	return resp.toTokens(), nil
}

// Login authenticates with email and password and returns a token pair.
func (c *Client) Login(ctx context.Context, email, password string) (Tokens, error) {
	payload := map[string]string{"email": email, "password": password}
	req, err := c.newJSONRequest(ctx, http.MethodPost, pathLogin, payload)
	if err != nil {
		return Tokens{}, err
	}
	var resp authResponse
	if err := c.doJSON(req, &resp); err != nil {
		return Tokens{}, err
	}
	return resp.toTokens(), nil
}

// Identity is the subset of access-token claims useful for display.
type Identity struct {
	UserID    string
	Email     string
	Role      string
	ExpiresAt time.Time
}

// Expired reports whether the access token's expiry has passed.
func (i Identity) Expired() bool {
	return !i.ExpiresAt.IsZero() && time.Now().After(i.ExpiresAt)
}

// Identify decodes an access token's claims without verifying its signature
// (the client does not hold the server's key). It is used by `auth status` to
// show who is logged in and when the session expires.
func Identify(accessToken string) (Identity, error) {
	claims, err := parseAccessClaims(accessToken)
	if err != nil {
		return Identity{}, err
	}
	id := Identity{
		UserID: claims.Subject,
		Email:  claims.Email,
		Role:   claims.Role,
	}
	if claims.ExpiresAt != nil {
		id.ExpiresAt = claims.ExpiresAt.Time
	}
	return id, nil
}

// accessClaims is the subset of the DFMS access-token payload the client reads.
type accessClaims struct {
	Email string `json:"email"`
	Role  string `json:"role"`
	jwt.RegisteredClaims
}

// parseAccessClaims decodes (without verifying) the claims of a JWT access token.
func parseAccessClaims(token string) (*accessClaims, error) {
	var claims accessClaims
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(token, &claims); err != nil {
		return nil, fmt.Errorf("decoding access token: %w", err)
	}
	return &claims, nil
}

// tokenExpiry returns the access token's expiry and whether it could be read.
func tokenExpiry(token string) (time.Time, bool) {
	claims, err := parseAccessClaims(token)
	if err != nil || claims.ExpiresAt == nil {
		return time.Time{}, false
	}
	return claims.ExpiresAt.Time, true
}
