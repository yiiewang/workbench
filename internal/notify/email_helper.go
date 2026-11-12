package notify

// email.go 的可注入辅助：环境变量读取、SMTP 连接、TLS 配置、base64 编码。
// 独立成文件并在测试中替换（var 函数值），使投递逻辑可离线单测。

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"net/smtp"
	"os"
	"strings"
)

// emailPass SMTP 授权码（环境变量 EMAIL_PASS，不落配置/仓库）
var emailPass = func() string { return os.Getenv("EMAIL_PASS") }

// runeStart 判断字节是否为 UTF-8 字符首字节（供 encodeWord 避免切断多字节字符）
func runeStart(b byte) bool { return b&0xC0 != 0x80 }

// smtpDial 建立 SMTP 连接（带 context 超时控制）
var smtpDial = func(ctx context.Context, addr string) (*smtp.Client, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return smtp.NewClient(conn, addr[:strings.LastIndexByte(addr, ':')])
}

// tlsConfig STARTTLS 客户端配置（校验服务器名）
var tlsConfig = func(host string) *tls.Config {
	return &tls.Config{ServerName: host}
}

// b64 base64 编码（RFC 2045：每 76 字符插入 CRLF）
func b64(s string) string {
	const lineLen = 76
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	var b strings.Builder
	for i := 0; i < len(enc); i += lineLen {
		end := i + lineLen
		if end > len(enc) {
			end = len(enc)
		}
		b.WriteString(enc[i:end])
		b.WriteString("\r\n")
	}
	return b.String()
}
