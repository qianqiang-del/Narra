package response

type AuthCodeSent struct {
	Sent bool `json:"sent"`
}

type AuthUser struct {
	ID    uint64 `json:"id"`
	Phone string `json:"phone"`
}

type AuthResult struct {
	Token            string   `json:"token"`
	ExpiresIn        int64    `json:"expires_in"`
	RefreshToken     string   `json:"refresh_token"`
	RefreshExpiresIn int64    `json:"refresh_expires_in"`
	User             AuthUser `json:"user"`
}
