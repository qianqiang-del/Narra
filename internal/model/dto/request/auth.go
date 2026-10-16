package request

type AuthCode struct {
	Phone       string `json:"phone" binding:"required"`
	Purpose     string `json:"purpose" binding:"required"`
	CaptchaID   string `json:"captcha_id"`
	CaptchaCode string `json:"captcha_code"`
}

type AuthRegister struct {
	Phone    string `json:"phone" binding:"required"`
	Password string `json:"password" binding:"required"`
	Code     string `json:"code" binding:"required"`
}

type AuthPasswordLogin struct {
	Phone       string `json:"phone" binding:"required"`
	Password    string `json:"password" binding:"required"`
	CaptchaID   string `json:"captcha_id"`
	CaptchaCode string `json:"captcha_code"`
}

type AuthCodeLogin struct {
	Phone string `json:"phone" binding:"required"`
	Code  string `json:"code" binding:"required"`
}

type AuthRefresh struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}
