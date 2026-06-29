package core

import (
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var hmacSampleSecret = []byte("my-super-secret-key")

func TestJWT(t *testing.T) {
	// Create claims
	claims := jwt.MapClaims{
		"foo":  "bar",
		"user": "mrigangha",
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(time.Hour).Unix(),
	}

	// Create token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Sign token
	tokenString, err := token.SignedString(hmacSampleSecret)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Generated JWT:")
	fmt.Println(tokenString)
	fmt.Println()

	// Parse & Verify
	parsedToken, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		// Verify signing algorithm
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		return hmacSampleSecret, nil
	})

	if err != nil {
		log.Fatal("Parse Error:", err)
	}

	if !parsedToken.Valid {
		log.Fatal("Token is invalid")
	}

	// Read claims
	if claims, ok := parsedToken.Claims.(jwt.MapClaims); ok {
		fmt.Println("Claims:")
		fmt.Println("foo :", claims["foo"])
		fmt.Println("user:", claims["user"])
		fmt.Println("iat :", claims["iat"])
		fmt.Println("exp :", claims["exp"])
	} else {
		log.Fatal("Unable to parse claims")
	}

	fmt.Println("\nJWT verification successful ✅")
}
