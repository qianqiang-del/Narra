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

func (f *authSenderFake) Send(_ context.Context, _, code string) error {
	f.code = code
	return f.err
}

func newAuthServiceFake() (*authService, *authUsersFake, *authSenderFake) {
	users := &authUsersFake{}
	sender := &authSenderFake{}
	svc := &authService{
		users: users, codes: &authCodesFake{}, sender: sender,
		secret: []byte("test-secret"), expires: 24 * time.Hour,
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
	if registered.Token != "signed-token" || registered.User.ID != 42 || registered.User.Phone != "+8613592209805" {
		t.Fatalf("unexpected registration result: %+v", registered)
	}
	if users.user.PasswordHash == "strong-password" || bcrypt.CompareHashAndPassword([]byte(users.user.PasswordHash), []byte("strong-password")) != nil {
		t.Fatal("registration did not store a working bcrypt hash")
	}
	if _, err := svc.Register(ctx, "13592209805", "strong-password", sender.code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("used registration code accepted: %v", err)
	}
	if _, err := svc.LoginPassword(ctx, "+8613592209805", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password accepted: %v", err)
	}
	if result, err := svc.LoginPassword(ctx, "13592209805", "strong-password"); err != nil || result.Token != "signed-token" {
		t.Fatalf("password login failed: %v", err)
	}
	if err := svc.SendCode(ctx, "13592209805", CodePurposeLogin, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if result, err := svc.LoginCode(ctx, "13592209805", sender.code); err != nil || result.Token != "signed-token" {
		t.Fatalf("code login failed: %v", err)
	}
	if _, err := svc.LoginCode(ctx, "13592209805", sender.code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("used login code accepted: %v", err)
	}
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
