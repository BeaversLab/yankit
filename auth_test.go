package yankit_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"

	"github.com/BeaversLab/yankit"
)

// testKeys 生成一对 Ed25519 密钥并导出 JWK set，模拟 YanAuth 的 JWKS 端点。
func testKeys(t *testing.T, kid string) (jwk.Key, jwk.Set) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	raw, err := jwk.Import(priv)
	if err != nil {
		t.Fatalf("import key: %v", err)
	}
	if err := raw.Set(jwk.KeyIDKey, kid); err != nil {
		t.Fatalf("set kid: %v", err)
	}
	pub, err := raw.PublicKey()
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	// 与 YanAuth JWKS 端点一致：公钥带 kid + alg + use。
	if err := pub.Set(jwk.AlgorithmKey, "EdDSA"); err != nil {
		t.Fatalf("set alg: %v", err)
	}
	if err := pub.Set(jwk.KeyUsageKey, "sig"); err != nil {
		t.Fatalf("set use: %v", err)
	}
	set := jwk.NewSet()
	if err := set.AddKey(pub); err != nil {
		t.Fatalf("add key: %v", err)
	}
	return raw, set
}

// signTestToken 按 YanAuth 的 claim 布局签发测试令牌。
func signTestToken(t *testing.T, key jwk.Key, sub string, now time.Time, ttl time.Duration, scope string, perms []string, act string) string {
	t.Helper()
	tok := jwt.New()
	_ = tok.Set(jwt.SubjectKey, sub)
	_ = tok.Set(jwt.IssuerKey, "yanauth")
	_ = tok.Set(jwt.IssuedAtKey, now.Unix())
	_ = tok.Set(jwt.NotBeforeKey, now.Unix())
	_ = tok.Set(jwt.ExpirationKey, now.Add(ttl).Unix())
	_ = tok.Set("scope", scope)
	if perms == nil {
		perms = []string{}
	}
	_ = tok.Set("perms", perms)
	if act != "" {
		_ = tok.Set("act", act)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.EdDSA(), key))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return string(signed)
}

// TestParseTokenValid 验证有效令牌解析出全部 claims。
func TestParseTokenValid(t *testing.T) {
	priv, set := testKeys(t, "k1")
	now := time.Now()
	raw := signTestToken(t, priv, "123", now, time.Hour, "normal", []string{"learn:course:create", "learn:preview:student"}, "42")

	c, err := yankit.ParseToken(raw, set)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if c.Subject != "123" || c.Scope != "normal" || c.Act != "42" {
		t.Fatalf("claims = %+v", c)
	}
	if len(c.Perms) != 2 || c.Perms[0] != "learn:course:create" {
		t.Fatalf("perms = %v", c.Perms)
	}
}

// TestParseTokenNoPermsAct 验证无 perms/act 的令牌解析为零值而非报错。
func TestParseTokenNoPermsAct(t *testing.T) {
	priv, set := testKeys(t, "k1")
	raw := signTestToken(t, priv, "7", time.Now(), time.Hour, "preview", nil, "")

	c, err := yankit.ParseToken(raw, set)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if len(c.Perms) != 0 || c.Act != "" {
		t.Fatalf("claims = %+v, want empty perms/act", c)
	}
}

// TestParseTokenExpired 验证过期令牌被拒绝。
func TestParseTokenExpired(t *testing.T) {
	priv, set := testKeys(t, "k1")
	raw := signTestToken(t, priv, "1", time.Now().Add(-2*time.Hour), time.Hour, "normal", nil, "")

	if _, err := yankit.ParseToken(raw, set); err == nil {
		t.Fatal("expired token accepted")
	}
}

// TestParseTokenNotYetValid 验证 nbf 在未来的令牌被拒绝。
func TestParseTokenNotYetValid(t *testing.T) {
	priv, set := testKeys(t, "k1")
	raw := signTestToken(t, priv, "1", time.Now().Add(time.Hour), 2*time.Hour, "normal", nil, "")

	if _, err := yankit.ParseToken(raw, set); err == nil {
		t.Fatal("not-yet-valid token accepted")
	}
}

// TestParseTokenWrongKey 验证由另一把密钥签名的令牌被拒绝。
func TestParseTokenWrongKey(t *testing.T) {
	other, _ := testKeys(t, "other")
	_, set := testKeys(t, "k1")
	raw := signTestToken(t, other, "1", time.Now(), time.Hour, "normal", nil, "")

	if _, err := yankit.ParseToken(raw, set); err == nil {
		t.Fatal("token signed by wrong key accepted")
	}
}

// TestParseTokenGarbage 验证垃圾输入被拒绝。
func TestParseTokenGarbage(t *testing.T) {
	_, set := testKeys(t, "k1")
	for _, raw := range []string{"", "not-a-jwt", "a.b.c"} {
		if _, err := yankit.ParseToken(raw, set); err == nil {
			t.Fatalf("garbage %q accepted", raw)
		}
	}
}

// TestParseJWKS 验证 JWKS JSON 解析。
func TestParseJWKS(t *testing.T) {
	_, set := testKeys(t, "k1")
	b, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := yankit.ParseJWKS(b)
	if err != nil {
		t.Fatalf("ParseJWKS: %v", err)
	}
	if parsed.Len() != 1 {
		t.Fatalf("keys = %d, want 1", parsed.Len())
	}
	if _, err := yankit.ParseJWKS([]byte("not json")); err == nil {
		t.Fatal("garbage JWKS accepted")
	}
}

// authEcho 组装完整鉴权链：JWTAuth → RequireScope → RequirePerm，含业务 handler。
func authEcho(t *testing.T, set jwk.Set, scope, perm string) (*echo.Echo, func(key jwk.Key)) {
	t.Helper()
	e, _ := newEcho()
	var signed string
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if signed != "" {
				c.Request().Header.Set("Authorization", "Bearer "+signed)
			}
			return next(c)
		}
	})
	src := yankit.KeySetFunc(func() (jwk.Set, error) { return set, nil })
	e.Use(yankit.JWTAuth(src))
	if scope != "" {
		e.Use(yankit.RequireScope(scope))
	}
	if perm != "" {
		e.Use(yankit.RequirePerm(perm))
	}
	e.GET("/me", func(c *echo.Context) error {
		claims := yankit.ClaimsOf(c)
		return c.JSON(200, map[string]any{"sub": claims.Subject})
	})
	return e, func(key jwk.Key) {
		signed = signTestToken(t, key, "9", time.Now(), time.Hour, "normal", []string{"learn:course:create"}, "")
	}
}

// TestMiddlewareChain 验证三中间件可独立/组合使用，错误码契约一致。
func TestMiddlewareChain(t *testing.T) {
	priv, set := testKeys(t, "k1")
	_, wrongSet := testKeys(t, "other")

	t.Run("missing bearer", func(t *testing.T) {
		e, _ := authEcho(t, set, "normal", "")
		req := httptest.NewRequest("GET", "/me", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		assertCode(t, rec.Body.String(), "invalid_token")
	})
	t.Run("bad signature", func(t *testing.T) {
		e, _ := authEcho(t, wrongSet, "normal", "")
		req := httptest.NewRequest("GET", "/me", nil)
		req.Header.Set("Authorization", "Bearer "+signTestToken(t, priv, "9", time.Now(), time.Hour, "normal", nil, ""))
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		assertCode(t, rec.Body.String(), "invalid_token")
	})
	t.Run("scope mismatch", func(t *testing.T) {
		e, _ := authEcho(t, set, "normal", "")
		preview := signTestToken(t, priv, "9", time.Now(), time.Hour, "preview", nil, "")
		req := httptest.NewRequest("GET", "/me", nil)
		req.Header.Set("Authorization", "Bearer "+preview)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		assertCode(t, rec.Body.String(), "scope_not_allowed")
	})
	t.Run("missing permission carries code", func(t *testing.T) {
		e, sign := authEcho(t, set, "normal", "learn:course:delete")
		sign(priv)
		status, _, raw := do(t, e, "GET", "/me")
		if status != 403 {
			t.Fatalf("status = %d, want 403 (body=%s)", status, raw)
		}
		assertCode(t, raw, "missing_permission")
		if !bytes.Contains([]byte(raw), []byte("learn:course:delete")) {
			t.Fatalf("missing perm code not in body: %s", raw)
		}
	})
	t.Run("pass", func(t *testing.T) {
		e, sign := authEcho(t, set, "normal", "learn:course:create")
		sign(priv)
		status, _, raw := do(t, e, "GET", "/me")
		if status != 200 {
			t.Fatalf("status = %d, body=%s", status, raw)
		}
		if !bytes.Contains([]byte(raw), []byte(`"sub":"9"`)) {
			t.Fatalf("body = %s", raw)
		}
	})
}

// TestMiddlewareStandalone 验证 Scope/Perm 不挂 JWTAuth 独立使用时，
// 对无 claims 请求收敛为 403 而非 panic（YanAuth 同场景会 panic）。
func TestMiddlewareStandalone(t *testing.T) {
	t.Run("scope without jwt", func(t *testing.T) {
		e, _ := newEcho()
		e.GET("/x", func(c *echo.Context) error { return nil }, yankit.RequireScope(yankit.ScopeNormal))
		status, _, raw := do(t, e, "GET", "/x")
		if status != 403 {
			t.Fatalf("status = %d, want 403 (body=%s)", status, raw)
		}
		assertCode(t, raw, "scope_not_allowed")
	})
	t.Run("perm without jwt", func(t *testing.T) {
		e, _ := newEcho()
		e.GET("/x", func(c *echo.Context) error { return nil }, yankit.RequirePerm("learn:course:create"))
		status, _, raw := do(t, e, "GET", "/x")
		if status != 403 {
			t.Fatalf("status = %d, want 403 (body=%s)", status, raw)
		}
		assertCode(t, raw, "missing_permission")
	})
}

// TestJWTAuthKeySetError 验证公钥来源故障收敛为 500 internal_error 且不泄露细节。
func TestJWTAuthKeySetError(t *testing.T) {
	e, _ := newEcho()
	failing := yankit.KeySetFunc(func() (jwk.Set, error) { return nil, errors.New("cache exploded") })
	e.GET("/x", func(c *echo.Context) error { return nil }, yankit.JWTAuth(failing))
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	assertCode(t, rec.Body.String(), "internal_error")
	if bytes.Contains(rec.Body.Bytes(), []byte("cache exploded")) {
		t.Fatalf("key source error leaked: %s", rec.Body.String())
	}
}

// TestAuthGroupNamespace 验证 Options 注入：公钥来源 + 权限命名空间前缀。
func TestAuthGroupNamespace(t *testing.T) {
	priv, set := testKeys(t, "k1")
	a := yankit.NewAuth(yankit.Options{
		Keys:      yankit.KeySetFunc(func() (jwk.Set, error) { return set, nil }),
		Namespace: "learn",
	})
	e, _ := newEcho()
	e.Use(a.JWT())
	e.Use(a.Scope(yankit.ScopeNormal))
	e.GET("/x", func(c *echo.Context) error { return nil })

	// 有 learn:course:create 权限 → Perm("course:create") 通过
	ok := signTestToken(t, priv, "1", time.Now(), time.Hour, "normal", []string{"learn:course:create"}, "")
	e.GET("/ok", func(c *echo.Context) error { return nil }, a.Perm("course:create"))
	req := httptest.NewRequest("GET", "/ok", nil)
	req.Header.Set("Authorization", "Bearer "+ok)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("namespaced perm pass: status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// 只有裸码 course:create（无命名空间）→ 拒绝
	bare := signTestToken(t, priv, "1", time.Now(), time.Hour, "normal", []string{"course:create"}, "")
	e.GET("/bare", func(c *echo.Context) error { return nil }, a.Perm("course:create"))
	req = httptest.NewRequest("GET", "/bare", nil)
	req.Header.Set("Authorization", "Bearer "+bare)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("bare perm should fail: status = %d", rec.Code)
	}
	assertCode(t, rec.Body.String(), "missing_permission")
}

// assertCode 校验错误体 {"code": want}。
func assertCode(t *testing.T, body, want string) {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body not JSON: %v, body=%s", err, body)
	}
	if m["code"] != want {
		t.Fatalf("code = %q, want %q (body=%s)", m["code"], want, body)
	}
}
