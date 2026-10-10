package entity

import "time"

// User 是通过手机号注册并完成短信验证的账号。
type User struct {
	BaseModel

	Phone           string    `gorm:"column:phone;type:varchar(20);not null;uniqueIndex:users_phone_key;comment:已验证的 E.164 格式手机号" json:"phone"`
	PasswordHash    string    `gorm:"column:password_hash;type:varchar(255);not null;comment:密码哈希，不保存明文密码" json:"-"`
	PhoneVerifiedAt time.Time `gorm:"column:phone_verified_at;not null;comment:手机号短信验证通过的时间" json:"phone_verified_at"`
}

func (User) TableName() string { return "users" }
