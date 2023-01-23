package app

import (
	"github.com/golang-jwt/jwt"
)

type Claim struct {
	LoginType int    //登录类型 1-appId / 2-client
	ClientId  string //当前用户身份
	jwt.StandardClaims
}

func CreateToken(claim *Claim, signKey string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claim)
	return token.SignedString([]byte(signKey))
}

func ValidToken(tokenStr string, signKey string) (*jwt.Token, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return []byte(signKey), nil
	})

	return token, err
}
