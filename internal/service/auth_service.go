package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	responsedto "narra/internal/model/dto/response"
	"narra/internal/model/entity"
	"narra/internal/repository"
	"narra/pkg/jwt"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	CodePurposeRegister = "register"
	CodePurposeLogin    = "login"
)

var (
	ErrInvalidPhone       = errors.New("请输入有效的中国大陆手机号")
	ErrInvalidPassword    = errors.New("密码须为 8 到 72 个字符")
	ErrInvalidCode        = errors.New("验证码错误或已过期")
	ErrAccountExists      = errors.New("手机号已注册")
	ErrInvalidCredentials = errors.New("手机号或密码错误")
	ErrAccountNotFound    = errors.New("用户不存在")
	ErrSMSUnavailable     = errors.New("短信服务暂不可用")
	ErrCodeRateLimited    = errors.New("验证码发送过于频繁，请稍后再试")
)

var mainlandPhone = regexp.MustCompile(`^1[3-9][0-9]{9}$`)

type SMSSender interface {
	Send(ctx context.Context, phone, code string) error
}

type authService struct {
	users   repository.UserRepository
	codes   repository.VerificationCodeRepository
	sender  SMSSender
	secret  []byte
	expires time.Duration
	sign    func(uint64, string) (string, error)
}

func NewAuthService(users repository.UserRepository, codes repository.VerificationCodeRepository, sender SMSSender, secret string, expires time.Duration) AuthService {
	return &authService{users: users, codes: codes, sender: sender, secret: []byte(secret), expires: expires, sign: jwt.GenerateToken}
}

func normalizePhone(input string) (string, error) {
	phone := strings.TrimSpace(input)
	phone = strings.TrimPrefix(phone, "+86")
	if !mainlandPhone.MatchString(phone) {
		return "", ErrInvalidPhone
	}
	return "+86" + phone, nil
}

func (s *authService) SendCode(ctx context.Context, phoneInput, purpose, ip string) error {
	phone, err := normalizePhone(phoneInput)
	if err != nil {
		return err
	}
	if purpose != CodePurposeRegister && purpose != CodePurposeLogin {
		return errors.New("验证码用途无效")
	}
	if s.codes == nil || s.sender == nil {
		return ErrSMSUnavailable
	}
	_, err = s.users.FindByPhone(ctx, phone)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if purpose == CodePurposeRegister && err == nil {
		return ErrAccountExists
	}
	if purpose == CodePurposeLogin && errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrAccountNotFound
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	digest := s.codeDigest(phone, purpose, code)
	if err := s.codes.Issue(ctx, phone, purpose, ip, digest); err != nil {
		if errors.Is(err, repository.ErrVerificationCodeRateLimited) {
			return ErrCodeRateLimited
		}
		return err
	}
	if err := s.sender.Send(ctx, phone, code); err != nil {
		_ = s.codes.Delete(ctx, phone, purpose, digest)
		return ErrSMSUnavailable
	}
	return nil
}

func (s *authService) Register(ctx context.Context, phoneInput, password, code string) (*responsedto.AuthResult, error) {
	phone, err := normalizePhone(phoneInput)
	if err != nil {
		return nil, err
	}
	if len(password) < 8 || len(password) > 72 {
		return nil, ErrInvalidPassword
	}
	if err := s.verifyCode(ctx, phone, CodePurposeRegister, code); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	user := &entity.User{Phone: phone, PasswordHash: string(hash), PhoneVerifiedAt: time.Now().UTC()}
	if err := s.users.Create(ctx, user); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrAccountExists
		}
		return nil, err
	}
	return s.tokenFor(user)
}

func (s *authService) LoginPassword(ctx context.Context, phoneInput, password string) (*responsedto.AuthResult, error) {
	phone, err := normalizePhone(phoneInput)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	user, err := s.users.FindByPhone(ctx, phone)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	return s.tokenFor(user)
}

func (s *authService) LoginCode(ctx context.Context, phoneInput, code string) (*responsedto.AuthResult, error) {
	phone, err := normalizePhone(phoneInput)
	if err != nil {
		return nil, err
	}
	user, err := s.users.FindByPhone(ctx, phone)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.verifyCode(ctx, phone, CodePurposeLogin, code); err != nil {
		return nil, err
	}
	return s.tokenFor(user)
}

func (s *authService) GetUser(ctx context.Context, id uint64) (*responsedto.AuthUser, error) {
	user, err := s.users.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &responsedto.AuthUser{ID: user.ID, Phone: user.Phone}, nil
}

func (s *authService) verifyCode(ctx context.Context, phone, purpose, code string) error {
	if s.codes == nil {
		return ErrSMSUnavailable
	}
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return ErrInvalidCode
	}
	ok, err := s.codes.Consume(ctx, phone, purpose, s.codeDigest(phone, purpose, code))
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCode
	}
	return nil
}

func (s *authService) codeDigest(phone, purpose, code string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(purpose + ":" + phone + ":" + code))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *authService) tokenFor(user *entity.User) (*responsedto.AuthResult, error) {
	token, err := s.sign(user.ID, user.Phone)
	if err != nil {
		return nil, err
	}
	return &responsedto.AuthResult{Token: token, ExpiresIn: int64(s.expires.Seconds()), User: responsedto.AuthUser{ID: user.ID, Phone: user.Phone}}, nil
}
