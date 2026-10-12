package jwt

import (
	"errors"
	"time"

	"narra/pkg/config"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrTokenInvalid = errors.New("令牌无效")
	ErrTokenExpired = errors.New("令牌已过期")
)

// GenerateToken 生成 JWT Token
func GenerateToken(userID uint64, username string) (string, error) {
	cfg := config.Get().JWT
	ttl := cfg.AccessTTL
	if ttl <= 0 {
		ttl = cfg.ExpireHours
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}

	claims := CustomClaims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "narra",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(cfg.Secret))
}

// ParseToken 解析 JWT Token
func ParseToken(tokenString string) (*CustomClaims, error) {
	cfg := config.Get().JWT

	token, err := jwt.ParseWithClaims(tokenString, &CustomClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(cfg.Secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer("narra"))

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}

	if claims, ok := token.Claims.(*CustomClaims); ok && token.Valid && claims.UserID != 0 {
		return claims, nil
	}

	return nil, ErrTokenInvalid
}
