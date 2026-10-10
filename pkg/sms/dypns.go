package sms

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/dypnsapi"

	"narra/pkg/config"
)

type DypnsSender struct {
	client *dypnsapi.Client
	cfg    config.SMSConfig
}

func NewDypnsSender(cfg config.SMSConfig) (*DypnsSender, error) {
	if cfg.RegionID == "" || cfg.SignName == "" || cfg.TemplateCode == "" || cfg.SchemeName == "" ||
		cfg.TemplateParam == "" || cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" ||
		cfg.CountryCode == "" || cfg.CodeLength != 6 || cfg.ValidTime <= 0 {
		return nil, errors.New("阿里云号码认证短信配置不完整")
	}
	client, err := dypnsapi.NewClientWithAccessKey(cfg.RegionID, cfg.AccessKeyID, cfg.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("初始化阿里云号码认证客户端失败: %w", err)
	}
	client.SetConnectTimeout(5 * time.Second)
	client.SetReadTimeout(10 * time.Second)
	return &DypnsSender{client: client, cfg: cfg}, nil
}

// Dypnsapi generates the code itself; the code argument is used only by Dysmsapi.
func (s *DypnsSender) Send(ctx context.Context, phone, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	request := dypnsapi.CreateSendSmsVerifyCodeRequest()
	request.Scheme = "https"
	request.SchemeName = s.cfg.SchemeName
	request.CountryCode = s.cfg.CountryCode
	request.PhoneNumber = strings.TrimPrefix(phone, "+"+s.cfg.CountryCode)
	request.SignName = s.cfg.SignName
	request.TemplateCode = s.cfg.TemplateCode
	request.TemplateParam = s.cfg.TemplateParam
	request.CodeLength = requests.NewInteger(s.cfg.CodeLength)
	request.ValidTime = requests.NewInteger(s.cfg.ValidTime)
	request.DuplicatePolicy = requests.NewInteger(s.cfg.DuplicatePolicy)
	request.Interval = requests.NewInteger(s.cfg.Interval)
	request.CodeType = requests.NewInteger(s.cfg.CodeType)
	response, err := s.client.SendSmsVerifyCode(request)
	if err != nil {
		return fmt.Errorf("号码认证短信发送失败: %w", err)
	}
	if !response.Success {
		return fmt.Errorf("号码认证短信发送被拒绝: code=%s, message=%s", response.Code, response.Message)
	}
	return nil
}

func (s *DypnsSender) VerifyCode(ctx context.Context, phone, code string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	request := dypnsapi.CreateCheckSmsVerifyCodeRequest()
	request.Scheme = "https"
	request.SchemeName = s.cfg.SchemeName
	request.CountryCode = s.cfg.CountryCode
	request.PhoneNumber = strings.TrimPrefix(phone, "+"+s.cfg.CountryCode)
	request.CaseAuthPolicy = requests.NewInteger(s.cfg.CaseAuthPolicy)
	request.VerifyCode = code
	response, err := s.client.CheckSmsVerifyCode(request)
	if err != nil {
		return false, fmt.Errorf("号码认证验证码校验失败: %w", err)
	}
	if !response.Success {
		return false, nil
	}
	return strings.EqualFold(response.Model.VerifyResult, "PASS"), nil
}
