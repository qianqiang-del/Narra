package config

import "testing"

// 存储模式判定：全空 = 本地兜底（合法）、半配置 = 启动失败、全齐 = OSS。
func TestStorageValidateOSSCompleteness(t *testing.T) {
	cases := []struct {
		name    string
		oss     OSSStorageConfig
		wantErr bool
	}{
		{"全空走本地兜底", OSSStorageConfig{}, false},
		{"纯空白也算空", OSSStorageConfig{Endpoint: "  ", Bucket: "\t"}, false},
		{"只填 endpoint 要报错", OSSStorageConfig{Endpoint: "oss-cn-hangzhou.aliyuncs.com"}, true},
		{"填了地址和 bucket 没凭证要报错", OSSStorageConfig{Endpoint: "e", Bucket: "b"}, true},
		{"只填了凭证没地址要报错", OSSStorageConfig{AccessKeyID: "id", AccessKeySecret: "secret"}, true},
		{"四项齐全合法", OSSStorageConfig{Endpoint: "e", Bucket: "b", AccessKeyID: "id", AccessKeySecret: "secret"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := StorageConfig{OSS: tc.oss}.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("应当报错")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("不应报错: %v", err)
			}
		})
	}
}

// Configured 是装配层选择实现与打印模式的判据：空白字符不算填写。
func TestOSSConfigured(t *testing.T) {
	full := OSSStorageConfig{
		Endpoint: " oss-cn-hangzhou.aliyuncs.com ", Bucket: " bucket ",
		AccessKeyID: " id ", AccessKeySecret: " secret ",
	}
	if !full.Configured() {
		t.Error("四项齐全应判定为已配置")
	}
	if (OSSStorageConfig{Endpoint: "e", Bucket: "b", AccessKeyID: "id"}).Configured() {
		t.Error("缺凭证不应判定为已配置")
	}
	if (OSSStorageConfig{Bucket: "  "}).Configured() {
		t.Error("纯空白不应判定为已配置")
	}
}
