package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// contextKey is a private type for context values.
type contextKey int

const userIDKey contextKey = 1

// Claims is the JWT payload.
type Claims struct {
	UserID string `json:"uid"`
	jwt.RegisteredClaims
}

// TokenService handles JWT issuance and validation.
type TokenService struct {
	secret  []byte
	expiry  time.Duration
	enabled bool
}

func NewTokenService(secret string, expiry time.Duration, enabled bool) *TokenService {
	return &TokenService{secret: []byte(secret), expiry: expiry, enabled: enabled}
}

// Issue creates a signed JWT for the given user ID.
func (s *TokenService) Issue(userID uuid.UUID) (string, error) {
	claims := Claims{
		UserID: userID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.expiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// Validate parses and validates a JWT string.
func (s *TokenService) Validate(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}

// Middleware extracts the JWT from the Authorization header.
// If JWT is disabled, it injects a nil user ID and lets the request through.
func (s *TokenService) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled {
			next.ServeHTTP(w, r)
			return
		}

		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
			return
		}

		claims, err := s.Validate(strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, claims.UserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserIDFromContext retrieves the user ID string from context.
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	raw, ok := ctx.Value(userIDKey).(string)
	if !ok || raw == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}
