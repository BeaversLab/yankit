package yankit

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// Token scopes and the expected issuer, shared with YanAuth.
const (
	ScopeNormal  = "normal"  // 常规管理/学习会话
	ScopePreview = "preview" // 学生视角预览（拒绝管理类接口）
)

// Claims are the YanAuth token claims a consumer service needs.
type Claims struct {
	Subject string   // user id（preview 令牌为影子账号 id）
	Scope   string   // normal | preview
	Act     string   // 发起预览的教师 id（仅 preview）
	Perms   []string // 带服务命名空间的权限码
}

// KeySetSource supplies the verification key set. It is called per request,
// so implementations decide their own caching/refresh strategy — verification
// itself stays local (no network call inside yankit).
type KeySetSource interface {
	KeySet() (jwk.Set, error)
}

// KeySetFunc adapts a function to KeySetSource.
type KeySetFunc func() (jwk.Set, error)

func (f KeySetFunc) KeySet() (jwk.Set, error) { return f() }

// claimsContextKey 存放验签后的 claims。
const claimsContextKey = "yankit.claims"

// ParseJWKS parses a JWKS document (e.g. fetched at startup from YanAuth's
// /.well-known/jwks.json) into a key set.
func ParseJWKS(b []byte) (jwk.Set, error) {
	return jwk.Parse(b)
}

// ParseToken verifies a YanAuth JWT against a key set and extracts the
// consumer-facing claims. Signature, expiry and not-before are all enforced.
func ParseToken(raw string, set jwk.Set) (*Claims, error) {
	parsed, err := jwt.Parse([]byte(raw), jwt.WithKeySet(set), jwt.WithValidate(true))
	if err != nil {
		return nil, err
	}
	c := &Claims{}
	if sub, ok := parsed.Subject(); ok {
		c.Subject = sub
	}
	var scope string
	if err := parsed.Get("scope", &scope); err == nil {
		c.Scope = scope
	}
	var act string
	if err := parsed.Get("act", &act); err == nil {
		c.Act = act
	}
	var perms []any
	if err := parsed.Get("perms", &perms); err == nil {
		for _, e := range perms {
			if s, ok := e.(string); ok {
				c.Perms = append(c.Perms, s)
			}
		}
	}
	return c, nil
}

// ClaimsOf returns the verified claims stored by JWTAuth, or nil when the
// request was not authenticated.
func ClaimsOf(c *echo.Context) *Claims {
	claims, _ := c.Get(claimsContextKey).(*Claims)
	return claims
}

// bearerToken extracts the Bearer token from the Authorization header.
func bearerToken(c *echo.Context) string {
	h := c.Request().Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// JWTAuth verifies the Bearer JWT against keys and stores the claims in the
// request context (read them with ClaimsOf). 401 invalid_token otherwise.
func JWTAuth(keys KeySetSource) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			raw := bearerToken(c)
			if raw == "" {
				return NewAPIError(http.StatusUnauthorized, CodeInvalidToken, "missing bearer token")
			}
			set, err := keys.KeySet()
			if err != nil {
				return NewAPIError(http.StatusInternalServerError, CodeInternalError, http.StatusText(http.StatusInternalServerError))
			}
			claims, err := ParseToken(raw, set)
			if err != nil {
				return NewAPIError(http.StatusUnauthorized, CodeInvalidToken, "invalid token")
			}
			c.Set(claimsContextKey, claims)
			return next(c)
		}
	}
}

// RequireScope rejects tokens whose scope does not match — e.g. preview
// tokens are refused by management routes requiring ScopeNormal.
// 403 scope_not_allowed otherwise.
func RequireScope(scope string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			claims := ClaimsOf(c)
			if claims == nil || claims.Scope != scope {
				return NewAPIError(http.StatusForbidden, CodeScopeNotAllowed, "token scope not allowed here")
			}
			return next(c)
		}
	}
}

// RequirePerm rejects tokens whose perms do not include the fully-qualified
// permission code (e.g. "learn:course:create"). 403 missing_permission,
// with the missing code in the error message, otherwise.
func RequirePerm(code string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			claims := ClaimsOf(c)
			if claims != nil {
				for _, p := range claims.Perms {
					if p == code {
						return next(c)
					}
				}
			}
			return NewAPIError(http.StatusForbidden, CodeMissingPermission, "missing permission "+code)
		}
	}
}

// Options configure an Auth group: the verification key source and the
// service's permission namespace (e.g. "learn" for codes like
// "learn:course:create").
type Options struct {
	Keys      KeySetSource
	Namespace string // 可选；Perm 会以此为前缀
}

// Auth bundles the three middlewares with shared options, for services that
// keep keys and a permission namespace in one place.
type Auth struct {
	Options
}

// NewAuth builds an Auth group.
func NewAuth(o Options) *Auth { return &Auth{Options: o} }

// JWT returns the JWT verification middleware.
func (a *Auth) JWT() echo.MiddlewareFunc { return JWTAuth(a.Keys) }

// Scope returns the scope-check middleware.
func (a *Auth) Scope(scope string) echo.MiddlewareFunc { return RequireScope(scope) }

// Perm returns the permission middleware for a namespaced code: with
// Namespace "learn", Perm("course:create") checks "learn:course:create".
func (a *Auth) Perm(code string) echo.MiddlewareFunc {
	if a.Namespace != "" {
		code = a.Namespace + ":" + code
	}
	return RequirePerm(code)
}
