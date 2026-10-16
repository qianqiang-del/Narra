package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"narra/internal/model/entity"
	"narra/internal/repository"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type authUsersFake struct{ user *entity.User }

func (f *authUsersFake) Create(_ context.Context, user *entity.User) error {
	if f.user != nil {
		return errors.New("duplicate")
	}
	user.ID = 42
	f.user = user
	return nil
}

func (f *authUsersFake) FindByPhone(_ context.Context, phone string) (*entity.User, error) {
	if f.user == nil || f.user.Phone != phone {
		return nil, gorm.ErrRecordNotFound
	}
	return f.user, nil
}

func (f *authUsersFake) FindByID(_ context.Context, id uint64) (*entity.User, error) {
	if f.user == nil || f.user.ID != id {
		return nil, gorm.ErrRecordNotFound
	}
	return f.user, nil
}

type authCodesFake struct {
	issued   map[string]string
	issueErr error
}

func (f *authCodesFake) Issue(_ context.Context, phone, purpose, _, digest string) error {
	if f.issueErr != nil {
		return f.issueErr
	}
	if f.issued == nil {
		f.issued = make(map[string]string)
	}
	f.issued[phone+purpose] = digest
	return nil
}

func (f *authCodesFake) Exists(_ context.Context, phone, purpose string) (bool, error) {
	_, ok := f.issued[phone+purpose]
	return ok, nil
}

func (f *authCodesFake) Consume(_ context.Context, phone, purpose, digest string) (bool, error) {
	key := phone + purpose
	if f.issued[key] != digest {
		return false, nil
	}
	delete(f.issued, key)
	return true, nil
}

func (f *authCodesFake) Delete(_ context.Context, phone, purpose, digest string) error {
	key := phone + purpose
	if f.issued[key] == digest {
		delete(f.issued, key)
	}
	return nil
}

type authSenderFake struct {
	code string
	err  error
}

type authRefreshFake struct{ sessions map[string]uint64 }

func (f *authRefreshFake) Store(_ context.Context, digest string, userID uint64, _ time.Duration) error {
	if f.sessions == nil {
		f.sessions = make(map[string]uint64)
	}
	f.sessions[digest] = userID
	return nil
}

func (f *authRefreshFake) Rotate(_ context.Context, oldDigest, newDigest string, _ time.Duration) (uint64, error) {
	userID := f.sessions[oldDigest]
	if userID != 0 {
		delete(f.sessions, oldDigest)
		f.sessions[newDigest] = userID
	}
	return userID, nil
}

func (f *authRefreshFake) Revoke(_ context.Context, digest string) error {
	delete(f.sessions, digest)
	return nil
}

type managedSenderFake struct {
	authSenderFake
	valid       bool
	verifyCalls int
}

func (f *managedSenderFake) VerifyCode(_ context.Context, _, _ string) (bool, error) {
	f.verifyCalls++
	return f.valid, nil
}

func (f *authSenderFake) Send(_ context.Context, _, code string) error {
	f.code = code
	return f.err
}

func newAuthServiceFake() (*authService, *authUsersFake, *authSenderFake) {
	users := &authUsersFake{}
	sender := &authSenderFake{}
	svc := &authService{
		users: users, codes: &authCodesFake{}, refresh: &authRefreshFake{}, sender: sender,
		secret: []byte("test-secret"), expires: 15 * time.Minute, refreshExpires: 7 * 24 * time.Hour,
		sign: func(uint64, string) (string, error) { return "signed-token", nil },
	}
	return svc, users, sender
}

func TestRegisterAndBothLoginMethods(t *testing.T) {
	ctx := context.Background()
	svc, users, sender := newAuthServiceFake()
	if err := svc.SendCode(ctx, "13592209805", CodePurposeRegister, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	registered, err := svc.Register(ctx, "13592209805", "strong-password", sender.code)
	if err != nil {
		t.Fatal(err)
	}
	if registered.Token != "signed-token" || registered.RefreshToken == "" || registered.User.ID != 42 || registered.User.Phone != "+8613592209805" {
		t.Fatalf("unexpected registration result: %+v", registered)
	}
	if users.user.PasswordHash == "strong-password" || bcrypt.CompareHashAndPassword([]byte(users.user.PasswordHash), []byte("strong-password")) != nil {
		t.Fatal("registration did not store a working bcrypt hash")
	}
	if _, err := svc.Register(ctx, "13592209805", "strong-password", sender.code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("used registration code accepted: %v", err)
	}
	svc.captcha = &captchaStoreFake{}
	if _, err := svc.LoginPassword(ctx, "+8613592209805", "wrong-password", newTestCaptchaAnswer(t, svc)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password accepted: %v", err)
	}
	if result, err := svc.LoginPassword(ctx, "13592209805", "strong-password", newTestCaptchaAnswer(t, svc)); err != nil || result.Token != "signed-token" {
		t.Fatalf("password login failed: %v", err)
	}
	answer := newTestCaptchaAnswer(t, svc)
	if err := svc.SendCode(ctx, "13592209805", CodePurposeLogin, "127.0.0.1", answer); err != nil {
		t.Fatal(err)
	}
	if result, err := svc.LoginCode(ctx, "13592209805", sender.code); err != nil || result.Token != "signed-token" {
		t.Fatalf("code login failed: %v", err)
	}
	if _, err := svc.LoginCode(ctx, "13592209805", sender.code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("used login code accepted: %v", err)
	}
}

func TestRefreshTokenRotatesAndLogoutRevokes(t *testing.T) {
	ctx := context.Background()
	svc, users, _ := newAuthServiceFake()
	svc.captcha = &captchaStoreFake{}
	users.user = &entity.User{Phone: "+8613592209805", BaseModel: entity.BaseModel{ID: 42}}
	login, err := svc.LoginPassword(ctx, "13592209805", "", newTestCaptchaAnswer(t, svc))
	if !errors.Is(err, ErrInvalidCredentials) || login != nil {
		t.Fatalf("错误密码不能登录: result=%+v err=%v", login, err)
	}
	users.user.PasswordHash = string(mustPasswordHash(t, "strong-password"))
	login, err = svc.LoginPassword(ctx, "13592209805", "strong-password", newTestCaptchaAnswer(t, svc))
	if err != nil || login.RefreshToken == "" || login.ExpiresIn != 900 || login.RefreshExpiresIn != 604800 {
		t.Fatalf("登录未签发双令牌: result=%+v err=%v", login, err)
	}
	rotated, err := svc.Refresh(ctx, login.RefreshToken)
	if err != nil || rotated.Token != "signed-token" || rotated.RefreshToken == login.RefreshToken {
		t.Fatalf("刷新未轮换令牌: result=%+v err=%v", rotated, err)
	}
	if _, err := svc.Refresh(ctx, login.RefreshToken); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("旧刷新令牌仍可使用: %v", err)
	}
	if err := svc.Logout(ctx, rotated.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("注销后的刷新令牌仍可使用: %v", err)
	}
}

func mustPasswordHash(t *testing.T, password string) []byte {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestAuthRequiresConfiguredSMS(t *testing.T) {
	svc, _, _ := newAuthServiceFake()
	svc.sender = nil
	if err := svc.SendCode(context.Background(), "13592209805", CodePurposeRegister, "127.0.0.1"); !errors.Is(err, ErrSMSUnavailable) {
		t.Fatalf("expected unavailable SMS, got %v", err)
	}
}

func TestCodeRateLimitMapsToServiceError(t *testing.T) {
	svc, _, _ := newAuthServiceFake()
	svc.codes = &authCodesFake{issueErr: repository.ErrVerificationCodeRateLimited}
	if err := svc.SendCode(context.Background(), "13592209805", CodePurposeRegister, "127.0.0.1"); !errors.Is(err, ErrCodeRateLimited) {
		t.Fatalf("expected service rate limit error, got %v", err)
	}
}

func TestFailedSMSInvalidatesIssuedCode(t *testing.T) {
	svc, _, sender := newAuthServiceFake()
	sender.err = errors.New("provider rejected message")
	ctx := context.Background()
	if err := svc.SendCode(ctx, "13592209805", CodePurposeRegister, "127.0.0.1"); !errors.Is(err, ErrSMSUnavailable) {
		t.Fatalf("expected unavailable SMS, got %v", err)
	}
	if _, err := svc.Register(ctx, "13592209805", "strong-password", sender.code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("code from failed delivery was accepted: %v", err)
	}
	sender.err = nil
	if err := svc.SendCode(ctx, "13592209805", CodePurposeRegister, "127.0.0.1"); err != nil {
		t.Fatalf("retry after failed delivery: %v", err)
	}
}

func TestManagedSMSCodeUsesProviderAndRedisPurpose(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newAuthServiceFake()
	sender := &managedSenderFake{valid: true}
	svc.sender = sender
	if err := svc.SendCode(ctx, "13592209805", CodePurposeRegister, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if sender.code != "" {
		t.Fatal("managed provider must generate the SMS code")
	}
	if err := svc.verifyCode(ctx, "+8613592209805", CodePurposeLogin, "123456"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("registration code accepted for login: %v", err)
	}
	if sender.verifyCalls != 0 {
		t.Fatal("provider called without a pending code for this purpose")
	}
	if err := svc.verifyCode(ctx, "+8613592209805", CodePurposeRegister, "123456"); err != nil {
		t.Fatal(err)
	}
	if err := svc.verifyCode(ctx, "+8613592209805", CodePurposeRegister, "123456"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("used code accepted again: %v", err)
	}
	if sender.verifyCalls != 1 {
		t.Fatalf("provider verification calls = %d, want 1", sender.verifyCalls)
	}
}

func TestManagedSMSRejectsWrongCodeWithoutConsumingPurpose(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newAuthServiceFake()
	sender := &managedSenderFake{}
	svc.sender = sender
	if err := svc.SendCode(ctx, "13592209805", CodePurposeRegister, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.verifyCode(ctx, "+8613592209805", CodePurposeRegister, "000000"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong code accepted: %v", err)
	}
	sender.valid = true
	if err := svc.verifyCode(ctx, "+8613592209805", CodePurposeRegister, "123456"); err != nil {
		t.Fatal(err)
	}
}
