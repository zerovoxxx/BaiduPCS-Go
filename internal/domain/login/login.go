package login

import "github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"

type Step struct {
	State, ChallengeType, Prompt string
	Image                        []byte
	Credentials                  account.Credentials
}
type Input struct{ Method, Username, Password string }
