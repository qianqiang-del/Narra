package sms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"narra/pkg/config"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/dysmsapi"
)

type Sender interface {
	Send(ctx context.Context, phone, code string) error
}

func NewSender(cfg config.SMSConfig) (Sender, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	switch cfg.API {
	case "", "dysmsapi":
		return NewAliyunSender(cfg)
	case "dypnsapi":
		return NewDypnsSender(cfg)
	default:
		return nil, fmt.Errorf("不支持的阿里云短信接口: %s", cfg.API)
	}
}

type AliyunSender struct {
	client   *dysmsapi.Client
	sign     string
	template string
}

func NewAliyunSender(cfg config.SMSConfig) (*AliyunSender, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.RegionID == "" || cfg.SignName == "" || cfg.TemplateCode == "" || cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return nil, errors.New("阿里云短信配置不完整")
	}
	client, err := dysmsapi.NewClientWithAccessKey(cfg.RegionID, cfg.AccessKeyID, cfg.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("初始化阿里云短信客户端失败: %w", err)
	}
	client.SetConnectTimeout(5 * time.Second)
	client.SetReadTimeout(10 * time.Second)
	return &AliyunSender{client: client, sign: cfg.SignName, template: cfg.TemplateCode}, nil
}

func (s *AliyunSender) Send(ctx context.Context, phone, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	params, _ := json.Marshal(map[string]string{"code": code})
	request := dysmsapi.CreateSendSmsRequest()
	request.Scheme = "https"
	request.PhoneNumbers = strings.TrimPrefix(phone, "+86")
	request.SignName = s.sign
	request.TemplateCode = s.template
	request.TemplateParam = string(params)
	response, err := s.client.SendSms(request)
	if err != nil {
		return fmt.Errorf("发送短信失败: %w", err)
	}
	if response.Code != "OK" {
		return fmt.Errorf("短信服务拒绝发送: code=%s, message=%s, request_id=%s", response.Code, response.Message, response.RequestId)
	}
	return nil
}
